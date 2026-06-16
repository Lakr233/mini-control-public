package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Lakr233/minis/server/internal/model"
)

var ErrReleaseVersionExists = errors.New("release version already exists")

// InsertRelease stores a new release record.
func (d *DB) InsertRelease(ctx context.Context, r *model.Release) error {
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO releases (version, sha256, size_bytes, signing_identity, file_path)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, uploaded_at
	`, r.Version, r.SHA256, r.SizeBytes, r.SigningIdentity, r.FilePath).Scan(&r.ID, &r.UploadedAt)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrReleaseVersionExists
	}
	return err
}

// GetLatestRelease returns the most recently uploaded release, or nil if none exist.
func (d *DB) GetLatestRelease(ctx context.Context) (*model.Release, error) {
	var r model.Release
	err := d.Pool.QueryRow(ctx, `
		SELECT id, version, sha256, size_bytes, signing_identity, file_path, uploaded_at
		FROM releases ORDER BY uploaded_at DESC LIMIT 1
	`).Scan(&r.ID, &r.Version, &r.SHA256, &r.SizeBytes, &r.SigningIdentity, &r.FilePath, &r.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetReleaseByVersion returns a specific release version.
func (d *DB) GetReleaseByVersion(ctx context.Context, version string) (*model.Release, error) {
	var r model.Release
	err := d.Pool.QueryRow(ctx, `
		SELECT id, version, sha256, size_bytes, signing_identity, file_path, uploaded_at
		FROM releases WHERE version = $1
	`, version).Scan(&r.ID, &r.Version, &r.SHA256, &r.SizeBytes, &r.SigningIdentity, &r.FilePath, &r.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
