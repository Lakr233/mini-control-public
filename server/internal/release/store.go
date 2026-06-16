package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/model"
)

var releaseVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var ErrReleaseVersionExists = errors.New("release version already exists")

// Store manages release binaries on disk and metadata in the database.
type Store struct {
	db                *db.DB
	storagePath       string
	requiredSigningID string
	logger            *slog.Logger
}

// NewStore creates a release store. If requiredSigningID is empty, signature validation is disabled.
func NewStore(database *db.DB, storagePath string, requiredSigningID string, logger *slog.Logger) (*Store, error) {
	rootPath, err := filepath.Abs(storagePath)
	if err != nil {
		return nil, fmt.Errorf("resolve release storage dir: %w", err)
	}
	if err := os.MkdirAll(rootPath, 0750); err != nil {
		return nil, fmt.Errorf("create release storage dir: %w", err)
	}
	return &Store{
		db:                database,
		storagePath:       rootPath,
		requiredSigningID: requiredSigningID,
		logger:            logger,
	}, nil
}

// Upload validates and stores a new release binary.
// The binary is written to disk, its Mach-O code signature is verified against
// the required signing identity, and metadata is stored in the database.
func (s *Store) Upload(ctx context.Context, version string, binary io.Reader) (*model.Release, error) {
	versionDir, finalPath, err := s.releasePaths(version)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.GetReleaseByVersion(ctx, version); err == nil {
		return nil, ErrReleaseVersionExists
	} else if !db.IsNotFound(err) {
		return nil, fmt.Errorf("check existing release: %w", err)
	}

	// Write to temp file while computing SHA256
	tmpFile, err := os.CreateTemp(s.storagePath, "upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		// Clean up temp file on any error
		os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), binary)
	if err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write binary: %w", err)
	}
	tmpFile.Close()

	hash := hex.EncodeToString(hasher.Sum(nil))

	// Validate Mach-O code signature
	if s.requiredSigningID != "" {
		if err := ValidateSigningIdentity(tmpPath, s.requiredSigningID); err != nil {
			return nil, fmt.Errorf("signature validation failed: %w", err)
		}
		s.logger.Info("signature validated", "version", version, "identity", s.requiredSigningID)
	}

	// Extract signing info for metadata
	sigInfo, err := ExtractSigningInfo(tmpPath)
	if err != nil && s.requiredSigningID != "" {
		return nil, fmt.Errorf("extract signing info: %w", err)
	}

	signingIdentity := ""
	if sigInfo != nil {
		signingIdentity = sigInfo.CommonName
	}

	// Move to final location
	if err := os.MkdirAll(versionDir, 0750); err != nil {
		return nil, fmt.Errorf("create version dir: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, fmt.Errorf("move binary to final location: %w", err)
	}
	if err := os.Chmod(finalPath, 0755); err != nil {
		return nil, fmt.Errorf("chmod binary: %w", err)
	}

	// Store in database
	release := &model.Release{
		Version:         version,
		SHA256:          hash,
		SizeBytes:       written,
		SigningIdentity: signingIdentity,
		FilePath:        finalPath,
	}
	if err := s.db.InsertRelease(ctx, release); err != nil {
		os.Remove(finalPath)
		if errors.Is(err, db.ErrReleaseVersionExists) {
			return nil, ErrReleaseVersionExists
		}
		return nil, fmt.Errorf("insert release record: %w", err)
	}

	s.logger.Info("release uploaded",
		"version", version,
		"sha256", hash,
		"size", written,
		"signing_identity", signingIdentity,
	)

	return release, nil
}

// GetLatest returns the most recently uploaded release, or nil if none exist.
func (s *Store) GetLatest(ctx context.Context) (*model.Release, error) {
	return s.db.GetLatestRelease(ctx)
}

// GetByVersion returns a specific release version.
func (s *Store) GetByVersion(ctx context.Context, version string) (*model.Release, error) {
	return s.db.GetReleaseByVersion(ctx, version)
}

// BinaryPath returns the on-disk path for a release binary.
func (s *Store) BinaryPath(r *model.Release) (string, error) {
	if r == nil || r.FilePath == "" {
		return "", fmt.Errorf("release binary path is empty")
	}

	absPath, err := filepath.Abs(r.FilePath)
	if err != nil {
		return "", fmt.Errorf("resolve release binary path: %w", err)
	}
	if !s.isWithinStorage(absPath) {
		return "", fmt.Errorf("release binary path escapes storage root")
	}
	return absPath, nil
}

// ValidateVersion rejects path separators and unexpected characters in release versions.
func ValidateVersion(version string) error {
	if !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid version")
	}
	return nil
}

func (s *Store) releasePaths(version string) (string, string, error) {
	if err := ValidateVersion(version); err != nil {
		return "", "", err
	}

	versionDir := filepath.Join(s.storagePath, version)
	if !s.isWithinStorage(versionDir) {
		return "", "", fmt.Errorf("release version escapes storage root")
	}

	finalPath := filepath.Join(versionDir, "minicontrol-worker")
	if !s.isWithinStorage(finalPath) {
		return "", "", fmt.Errorf("release binary path escapes storage root")
	}

	return versionDir, finalPath, nil
}

func (s *Store) isWithinStorage(path string) bool {
	relPath, err := filepath.Rel(s.storagePath, path)
	if err != nil {
		return false
	}
	if relPath == ".." {
		return false
	}
	return !strings.HasPrefix(relPath, ".."+string(os.PathSeparator))
}
