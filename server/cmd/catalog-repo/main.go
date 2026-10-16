// catalog-repo provisions and publishes the small official TUF catalog using
// go-tuf's typed metadata and signature verification APIs. Run init explicitly;
// it never runs during builds or application startup.
package main

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

const maxArtifactSize = 1 << 30

type engineManifest struct {
	Engine struct {
		Artifact *struct {
			URL         string `json:"url"`
			DownloadURL string `json:"downloadUrl"`
			SizeBytes   int64  `json:"sizeBytes"`
			SHA256      string `json:"sha256"`
		} `json:"artifact"`
	} `json:"engine"`
}

func main() {
	if len(os.Args) < 2 {
		fatal("usage: catalog-repo validate|init|publish [flags]")
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = validateCatalog(os.Args[2:])
	case "init":
		err = initRoot(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal("%v", err)
	}
}

func validateCatalog(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	catalogRoot := flags.String("catalog-root", "catalog", "catalog directory containing targets/catalog/index.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	targetRoot := filepath.Join(*catalogRoot, "targets")
	readTarget := func(name string, into any) error {
		if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
			return fmt.Errorf("unsafe target path %q", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("unsafe target path %q", name)
			}
		}
		full := filepath.Join(targetRoot, filepath.FromSlash(name))
		rootAbs, err := filepath.Abs(targetRoot)
		if err != nil {
			return err
		}
		fullAbs, err := filepath.Abs(full)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootAbs, fullAbs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("target escapes catalog: %q", name)
		}
		info, err := os.Lstat(fullAbs)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target %q must be a regular file, not a symlink", name)
		}
		data, err := os.ReadFile(fullAbs)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, into); err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		return nil
	}
	var index repositorymanager.CatalogIndex
	if err := readTarget("catalog/index.json", &index); err != nil {
		return fmt.Errorf("read catalog index: %w", err)
	}
	if index.SchemaVersion != 1 {
		return fmt.Errorf("unsupported catalog schema version %d", index.SchemaVersion)
	}
	if len(index.Engines)+len(index.Profiles) == 0 {
		return errors.New("catalog must contain an engine or profile")
	}
	engines := make(map[string][]engine.Definition)
	seen := make(map[string]bool)
	for _, entry := range index.Engines {
		key := "engine\x00" + entry.ID + "\x00" + entry.Version
		if entry.ID == "" || entry.Version == "" || seen[key] {
			return fmt.Errorf("missing or duplicate engine release %q %q", entry.ID, entry.Version)
		}
		seen[key] = true
		var manifest repositorymanager.EngineManifest
		if err := readTarget(entry.ManifestTarget, &manifest); err != nil {
			return fmt.Errorf("engine %s %s: %w", entry.ID, entry.Version, err)
		}
		if manifest.Engine.ID != entry.ID || manifest.Engine.Version != entry.Version {
			return fmt.Errorf("engine index entry and manifest disagree for %q %s", entry.ID, entry.Version)
		}
		if err := manifest.Engine.Validate(); err != nil {
			return fmt.Errorf("engine %q: %w", entry.ID, err)
		}
		engines[entry.ID] = append(engines[entry.ID], manifest.Engine)
	}
	for _, entry := range index.Profiles {
		key := "profile\x00" + entry.ID + "\x00" + entry.Version
		if entry.ID == "" || entry.Version == "" || seen[key] {
			return fmt.Errorf("missing or duplicate profile release %q %q", entry.ID, entry.Version)
		}
		seen[key] = true
		var manifest repositorymanager.ProfileManifest
		if err := readTarget(entry.ManifestTarget, &manifest); err != nil {
			return fmt.Errorf("profile %s %s: %w", entry.ID, entry.Version, err)
		}
		p := manifest.Profile
		if p.ID != entry.ID || p.Version != entry.Version {
			return fmt.Errorf("profile index entry and manifest disagree for %q %s", entry.ID, entry.Version)
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", p.ID, err)
		}
		for i, rule := range p.Rules {
			if err := profile.ValidateWorkerArguments(rule.Arguments); err != nil {
				return fmt.Errorf("profile %q rule %d: %w", p.ID, i, err)
			}
		}
		for _, req := range p.Engines {
			candidates := engines[req.EngineID]
			if len(candidates) == 0 {
				continue
			} // It may be supplied by a different trusted source or manually.
			compatible := false
			for _, candidate := range candidates {
				if !req.Accepts(candidate) {
					continue
				}
				providesRules := true
				for _, rule := range p.Rules {
					if rule.EngineID == req.EngineID {
						if _, ok := candidate.Executables[rule.Executable]; !ok {
							providesRules = false
						}
					}
				}
				if providesRules {
					compatible = true
					break
				}
			}
			if !compatible {
				return fmt.Errorf("profile %q has no catalog engine %q matching its version, capabilities, and executable requirements", p.ID, req.EngineID)
			}
		}
	}
	fmt.Printf("Catalog valid: %d engine releases, %d profile releases\n", len(index.Engines), len(index.Profiles))
	return nil
}

func initRoot(args []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	keysDir := flags.String("keys-dir", "", "private key directory (create outside Git and back up offline)")
	rootFile := flags.String("root-file", "", "public root metadata output, e.g. catalog/metadata/root.json")
	embedFile := flags.String("embed-root", "", "public root embedded by the Go Core, e.g. server/internal/repositorymanager/official-root.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *keysDir == "" || *rootFile == "" || *embedFile == "" {
		return errors.New("init requires --keys-dir, --root-file, and --embed-root")
	}
	if err := requireEmptyDirectory(*keysDir); err != nil {
		return err
	}
	for _, path := range []string{*rootFile, *embedFile} {
		if info, err := os.Stat(path); err == nil && info.Size() != 0 {
			return fmt.Errorf("refusing to replace existing public root file %s", path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(*keysDir, 0700); err != nil {
		return err
	}
	root := metadata.Root(time.Now().UTC().AddDate(10, 0, 0))
	root.Signed.ConsistentSnapshot = false
	privateKeys := make(map[string]ed25519.PrivateKey)
	for _, role := range []string{metadata.ROOT, metadata.TARGETS, metadata.SNAPSHOT, metadata.TIMESTAMP} {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		key, err := metadata.KeyFromPublicKey(pub)
		if err != nil {
			return err
		}
		if err = root.Signed.AddKey(key, role); err != nil {
			return err
		}
		privateKeys[role] = priv
	}
	if err := sign(root, privateKeys[metadata.ROOT]); err != nil {
		return err
	}
	rootBytes, err := root.ToBytes(true)
	if err != nil {
		return err
	}
	for role, key := range privateKeys {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		path := filepath.Join(*keysDir, role+".key")
		if err = writeNew(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
			return err
		}
	}
	if err = writeAtomic(*rootFile, rootBytes, 0644); err != nil {
		return err
	}
	if err = writeAtomic(*embedFile, rootBytes, 0644); err != nil {
		return err
	}
	fingerprint := sha256.Sum256(rootBytes)
	fmt.Printf("Created TUF root (sha256:%s). Private keys are in %s; move root.key to offline custody and keep all private keys out of Git.\n", hex.EncodeToString(fingerprint[:]), *keysDir)
	return nil
}

func publish(args []string) error {
	return publishWithVerifier(args, verifyExternalPackage)
}

func publishWithVerifier(args []string, verify func(string, int64, string) error) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	catalogRoot := flags.String("catalog-root", "catalog", "catalog directory containing targets/ and metadata/root.json")
	keysDir := flags.String("keys-dir", "", "private signing keys directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *keysDir == "" {
		return errors.New("publish requires --keys-dir")
	}
	metadataDir := filepath.Join(*catalogRoot, "metadata")
	root, err := metadata.Root().FromFile(filepath.Join(metadataDir, "root.json"))
	if err != nil {
		return fmt.Errorf("read provisioned root: %w", err)
	}
	if err = root.VerifyDelegate(metadata.ROOT, root); err != nil {
		return fmt.Errorf("provisioned root self-signature is invalid: %w", err)
	}
	targets := metadata.Targets(time.Now().UTC().AddDate(0, 0, 30))
	targets.Signed.Version, err = nextVersion(filepath.Join(metadataDir, "targets.json"))
	if err != nil {
		return err
	}
	targetRoot := filepath.Join(*catalogRoot, "targets")
	err = filepath.WalkDir(targetRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := metadata.TargetFile().FromFile(path, "sha256")
		if err != nil {
			return err
		}
		name, err := filepath.Rel(targetRoot, path)
		if err != nil {
			return err
		}
		targets.Signed.Targets[filepath.ToSlash(name)] = info
		return nil
	})
	if err != nil {
		return err
	}
	if err = addExternalPackages(targetRoot, targets, verify); err != nil {
		return err
	}
	if err = signRole(targets, filepath.Join(*keysDir, "targets.key")); err != nil {
		return err
	}
	if err = root.VerifyDelegate(metadata.TARGETS, targets); err != nil {
		return fmt.Errorf("targets key is not authorized by root: %w", err)
	}
	targetBytes, err := targets.ToBytes(true)
	if err != nil {
		return err
	}

	snapshot := metadata.Snapshot(time.Now().UTC().AddDate(0, 0, 30))
	snapshot.Signed.Version, err = nextVersion(filepath.Join(metadataDir, "snapshot.json"))
	if err != nil {
		return err
	}
	snapshot.Signed.Meta["targets.json"] = metaFile(targetBytes, targets.Signed.Version)
	if err = signRole(snapshot, filepath.Join(*keysDir, "snapshot.key")); err != nil {
		return err
	}
	if err = root.VerifyDelegate(metadata.SNAPSHOT, snapshot); err != nil {
		return fmt.Errorf("snapshot key is not authorized by root: %w", err)
	}
	snapshotBytes, err := snapshot.ToBytes(true)
	if err != nil {
		return err
	}

	timestamp := metadata.Timestamp(time.Now().UTC().AddDate(0, 0, 7))
	timestamp.Signed.Version, err = nextVersion(filepath.Join(metadataDir, "timestamp.json"))
	if err != nil {
		return err
	}
	timestamp.Signed.Meta["snapshot.json"] = metaFile(snapshotBytes, snapshot.Signed.Version)
	if err = signRole(timestamp, filepath.Join(*keysDir, "timestamp.key")); err != nil {
		return err
	}
	if err = root.VerifyDelegate(metadata.TIMESTAMP, timestamp); err != nil {
		return fmt.Errorf("timestamp key is not authorized by root: %w", err)
	}
	timestampBytes, err := timestamp.ToBytes(true)
	if err != nil {
		return err
	}

	if err = os.MkdirAll(metadataDir, 0755); err != nil {
		return err
	}
	if err = writeAtomic(filepath.Join(metadataDir, "targets.json"), targetBytes, 0644); err != nil {
		return err
	}
	if err = writeAtomic(filepath.Join(metadataDir, "snapshot.json"), snapshotBytes, 0644); err != nil {
		return err
	}
	if err = writeAtomic(filepath.Join(metadataDir, "timestamp.json"), timestampBytes, 0644); err != nil {
		return err
	}
	fmt.Printf("Wrote TUF targets v%d, snapshot v%d, timestamp v%d. Commit catalog/metadata and catalog/targets together. Timestamp expires in 7 days.\n", targets.Signed.Version, snapshot.Signed.Version, timestamp.Signed.Version)
	return nil
}

func addExternalPackages(targetRoot string, targets *metadata.Metadata[metadata.TargetsType], verify func(string, int64, string) error) error {
	indexBytes, err := os.ReadFile(filepath.Join(targetRoot, "catalog", "index.json"))
	if err != nil {
		return err
	}
	var index struct {
		Engines []struct {
			ManifestTarget string `json:"manifestTarget"`
		} `json:"engines"`
	}
	if err = json.Unmarshal(indexBytes, &index); err != nil {
		return err
	}
	for _, entry := range index.Engines {
		manifestPath := filepath.Join(targetRoot, filepath.FromSlash(entry.ManifestTarget))
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			return err
		}
		var manifest engineManifest
		if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
			return err
		}
		a := manifest.Engine.Artifact
		if a == nil || a.DownloadURL == "" {
			continue
		}
		if a.SizeBytes <= 0 || a.SizeBytes > maxArtifactSize || len(a.SHA256) != 64 || !safeTarget(a.URL) {
			return fmt.Errorf("invalid external package metadata in %s", entry.ManifestTarget)
		}
		if err := verify(a.DownloadURL, a.SizeBytes, a.SHA256); err != nil {
			return fmt.Errorf("verify package in %s: %w", entry.ManifestTarget, err)
		}
		hash, err := hex.DecodeString(a.SHA256)
		if err != nil {
			return fmt.Errorf("invalid package SHA-256 in %s", entry.ManifestTarget)
		}
		custom, err := json.Marshal(map[string]string{"downloadUrl": a.DownloadURL})
		if err != nil {
			return err
		}
		customRaw := json.RawMessage(custom)
		targets.Signed.Targets[a.URL] = &metadata.TargetFiles{Length: a.SizeBytes, Hashes: metadata.Hashes{"sha256": metadata.HexBytes(hash)}, Custom: &customRaw}
	}
	return nil
}

func verifyExternalPackage(rawURL string, expectedSize int64, expectedHash string) error {
	if expectedSize <= 0 || expectedSize > maxArtifactSize || len(expectedHash) != 64 {
		return fmt.Errorf("invalid signed size or SHA-256")
	}
	if _, err := hex.DecodeString(expectedHash); err != nil {
		return fmt.Errorf("invalid signed SHA-256: %w", err)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("release must use a plain HTTPS GitHub URL")
	}
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || len(via) == 0 || via[0].URL.Scheme != "https" || !strings.EqualFold(via[0].URL.Hostname(), "github.com") || req.URL.Scheme != "https" {
			return fmt.Errorf("release redirect is not allowed")
		}
		switch strings.ToLower(req.URL.Hostname()) {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-cloud.s3.amazonaws.com":
			return nil
		default:
			return fmt.Errorf("release redirected to an unapproved host")
		}
	}}
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != expectedSize {
		return fmt.Errorf("upstream size %d differs from manifest size %d", response.ContentLength, expectedSize)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(response.Body, expectedSize+1))
	if err != nil {
		return err
	}
	if n != expectedSize || n > maxArtifactSize {
		return fmt.Errorf("download size %d differs from manifest size %d", n, expectedSize)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedHash) {
		return errors.New("download SHA-256 differs from manifest")
	}
	return nil
}

func safeTarget(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func nextVersion(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	var doc struct {
		Signed struct {
			Version int64 `json:"version"`
		} `json:"signed"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("decode existing metadata %s: %w", path, err)
	}
	if doc.Signed.Version < 1 || doc.Signed.Version == int64(^uint64(0)>>1) {
		return 0, fmt.Errorf("existing metadata %s has invalid version", path)
	}
	return doc.Signed.Version + 1, nil
}

func metaFile(data []byte, version int64) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	return &metadata.MetaFiles{Version: version, Length: int64(len(data)), Hashes: metadata.Hashes{"sha256": metadata.HexBytes(sum[:])}}
}

func signRole[T metadata.Roles](meta *metadata.Metadata[T], path string) error {
	key, err := readPrivateKey(path)
	if err != nil {
		return err
	}
	return sign(meta, key)
}

func sign[T metadata.Roles](meta *metadata.Metadata[T], key ed25519.PrivateKey) error {
	signer, err := signature.LoadSigner(key, crypto.Hash(0))
	if err != nil {
		return err
	}
	_, err = meta.Sign(signer)
	return err
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("private signing key %s must be a regular file", path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private signing key %s must have permissions 0600", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key %s", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key %s is not Ed25519", path)
	}
	return key, nil
}

func requireEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("refusing to generate keys in non-empty directory %s", path)
	}
	return nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := writeNew(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fatal(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(2) }
