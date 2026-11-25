package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/timisah/101conv/server/internal/document"
)

// SubmitDocumentBuildFor validates a Document Creator manifest, verifies
// ownership and integrity metadata of every referenced upload, freezes an
// immutable ExecutionSpec, and queues a durable build job for the isolated Worker.
func (s *Service) SubmitDocumentBuildFor(ctx context.Context, ownerID string, manifest document.Manifest, idempotencyKey string) (Job, bool, error) {
	if err := manifest.Validate(); err != nil {
		return Job{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !idempotencyRE.MatchString(idempotencyKey) {
		return Job{}, false, fmt.Errorf("%w: idempotencyKey must be 8-128 safe characters", ErrInvalidInput)
	}
	format, ok := document.LookupFormat(manifest.OutputFormat)
	if !ok {
		return Job{}, false, fmt.Errorf("%w: unsupported output format %q", ErrInvalidInput, manifest.OutputFormat)
	}
	manifest.OutputFormat = format.ID
	manifest.Name = strings.TrimSpace(manifest.Name)

	// Verify ownership and collect frozen source metadata for every unique upload.
	seen := make(map[string]bool)
	sources := make([]document.SourceSpec, 0, len(manifest.Pages))
	for _, page := range manifest.Pages {
		if seen[page.UploadID] {
			continue
		}
		seen[page.UploadID] = true
		upload, _, err := s.GetUploadFor(ctx, page.UploadID, ownerID)
		if err != nil {
			return Job{}, false, err
		}
		if document.ClassifySourceExtension(upload.Extension) == "" {
			return Job{}, false, fmt.Errorf("%w: upload %q (%s) is not a supported document build source", ErrInvalidInput, upload.FileName, upload.Extension)
		}
		sources = append(sources, document.SourceSpec{
			UploadID:  upload.ID,
			FileName:  upload.FileName,
			Extension: upload.Extension,
			Size:      upload.Size,
			SHA256:    upload.SHA256,
		})
	}

	buildSpec := &document.BuildExecutionSpec{
		Manifest: manifest,
		Sources:  sources,
	}

	// Attach managed LibreOffice / Poppler executable references if installed.
	if s.management != nil {
		if engines, listErr := s.management.ListEngines(ctx); listErr == nil {
			for _, eng := range engines {
				if eng.Definition.ID == "libreoffice" && buildSpec.LibreOfficeEngineID == "" {
					for name, relPath := range eng.Definition.Executables {
						if name == "office-convert" || name == "soffice" || name == "libreoffice" {
							buildSpec.LibreOfficeEngineID = eng.Definition.ID
							buildSpec.LibreOfficeEngineVersion = eng.Definition.Version
							buildSpec.LibreOfficeRelativePath = relPath
							break
						}
					}
				}
				if eng.Definition.ID == "poppler" && buildSpec.PopplerEngineID == "" {
					for name, relPath := range eng.Definition.Executables {
						if name == "pdftoppm" {
							buildSpec.PopplerEngineID = eng.Definition.ID
							buildSpec.PopplerEngineVersion = eng.Definition.Version
							buildSpec.PopplerRelativePath = relPath
							break
						}
					}
				}
			}
		}
	}

	execSpec := ExecutionSpec{
		SourceExtension: "pages",
		TargetExtension: format.Extension,
		DocumentBuild:   buildSpec,
	}
	specBytes, err := json.Marshal(execSpec)
	if err != nil {
		return Job{}, false, err
	}

	// Compute deterministic request hash for idempotency conflict detection.
	reqHashSum := sha256.Sum256(specBytes)
	reqHash := hex.EncodeToString(reqHashSum[:])

	effectiveOwner := ownerID
	if effectiveOwner == "" {
		effectiveOwner = "admin"
	}

	// Check for existing idempotent job first.
	var existingID, existingHash string
	err = s.db.QueryRowContext(ctx, `SELECT id, request_hash FROM conv_jobs WHERE idempotency_key=$1 AND owner_id=$2`, idempotencyKey, effectiveOwner).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != reqHash {
			return Job{}, false, fmt.Errorf("%w: idempotencyKey was already used with a different document build request", ErrConflict)
		}
		job, err := s.GetFor(ctx, existingID, ownerID)
		if err != nil {
			return Job{}, false, err
		}
		return job, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}

	jobID, err := randomID()
	if err != nil {
		return Job{}, false, err
	}

	primaryUploadID := manifest.Pages[0].UploadID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO conv_jobs(id, upload_id, profile_id, profile_version, source_format, target_format, mode, execution, idempotency_key, request_hash, state, max_attempts, owner_id)
		 VALUES($1, $2, 'document-creator', '1.0.0', 'pages', $3, 'build', $4, $5, $6, 'queued', $7, $8)`,
		jobID, primaryUploadID, format.ID, specBytes, idempotencyKey, reqHash, MaxAttempts, effectiveOwner,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			job, getErr := s.getJobByIdempotencyKey(ctx, idempotencyKey, effectiveOwner, reqHash, ownerID)
			if getErr != nil {
				return Job{}, false, getErr
			}
			return job, true, nil
		}
		return Job{}, false, err
	}

	// Mark referenced uploads as consumed so abandoned-upload cleanup does not
	// delete sources while the document build job is queued or running.
	for _, src := range sources {
		if _, err := tx.ExecContext(ctx, `UPDATE conv_uploads SET consumed=true WHERE id=$1`, src.UploadID); err != nil {
			return Job{}, false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	job, err := s.GetFor(ctx, jobID, ownerID)
	if err != nil {
		return Job{}, false, err
	}
	return job, false, nil
}

func (s *Service) getJobByIdempotencyKey(ctx context.Context, key, effectiveOwner, reqHash, ownerID string) (Job, error) {
	var existingID, existingHash string
	if err := s.db.QueryRowContext(ctx, `SELECT id, request_hash FROM conv_jobs WHERE idempotency_key=$1 AND owner_id=$2`, key, effectiveOwner).Scan(&existingID, &existingHash); err != nil {
		return Job{}, err
	}
	if existingHash != reqHash {
		return Job{}, fmt.Errorf("%w: idempotencyKey was already used with a different document build request", ErrConflict)
	}
	return s.GetFor(ctx, existingID, ownerID)
}
