package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
)

type SetupStatus struct {
	Completed            bool `json:"completed"`
	AdministratorCreated bool `json:"administratorCreated"`
}

func (s *Service) InitialSetupStatus(ctx context.Context) (SetupStatus, error) {
	var status SetupStatus
	err := s.db.QueryRowContext(ctx, `SELECT completed,EXISTS(SELECT 1 FROM conv_users WHERE role='admin' AND enabled) FROM conv_instance_setup WHERE id=1`).Scan(&status.Completed, &status.AdministratorCreated)
	return status, err
}

// CreateInitialAdmin serializes the one-time claim on the setup row. A failed
// transaction leaves the instance unclaimed, including its audit record.
func (s *Service) CreateInitialAdmin(ctx context.Context, username, password string) (Principal, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) || len(password) < 12 || len(password) > 256 {
		return Principal{}, ErrInvalidInput
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Principal{}, err
	}
	id, err := randomID()
	if err != nil {
		return Principal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Principal{}, err
	}
	defer tx.Rollback()
	var completed bool
	if err = tx.QueryRowContext(ctx, `SELECT completed FROM conv_instance_setup WHERE id=1 FOR UPDATE`).Scan(&completed); err != nil {
		return Principal{}, err
	}
	var existing bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_users)`).Scan(&existing); err != nil {
		return Principal{}, err
	}
	if completed || existing {
		return Principal{}, fmt.Errorf("%w: initial administrator has already been claimed", ErrConflict)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conv_users(id,username,password_salt,password_hash,role,enabled,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes,role_id,permission_overrides,max_file_mode,max_file_limit,monthly_jobs_mode,monthly_bytes_mode)
	VALUES($1,$2,$3,$4,'admin',true,true,true,104857600,0,0,'custom','{}'::jsonb,'limited',104857600,'unlimited','unlimited')`, id, username, salt, passwordHash(password, salt))
	if err != nil {
		return Principal{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_audit_events(actor_id,action,subject_type,subject_id) VALUES($1,'setup.administrator_created','user',$1)`, id); err != nil {
		return Principal{}, err
	}
	if err = tx.Commit(); err != nil {
		return Principal{}, err
	}
	return s.Principal(ctx, id)
}

func (s *Service) CompleteInitialSetup(ctx context.Context, actorID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var completed bool
	if err = tx.QueryRowContext(ctx, `SELECT completed FROM conv_instance_setup WHERE id=1 FOR UPDATE`).Scan(&completed); err != nil {
		return err
	}
	if completed {
		return tx.Commit()
	}
	var admin bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_users WHERE id=$1 AND role='admin' AND enabled)`, actorID).Scan(&admin); err != nil {
		return err
	}
	if !admin {
		return ErrInvalidInput
	}
	// Fresh installations opt into guest mode only when the administrator has
	// completed the deliberate first-run setup. Existing deployments are already
	// marked complete by migration and retain the private default (disabled).
	if _, err = tx.ExecContext(ctx, `UPDATE conv_anonymous_settings SET enabled=true,updated_at=now() WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_instance_setup SET completed=true,updated_at=now() WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_audit_events(actor_id,action,subject_type,subject_id) VALUES($1,'setup.completed','instance','1')`, actorID); err != nil {
		return err
	}
	return tx.Commit()
}
