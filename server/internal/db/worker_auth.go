package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Lakr233/minis/server/internal/model"
)

func GenerateWorkerToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate worker token: %w", err)
	}
	return "wkt_" + hex.EncodeToString(tokenBytes), nil
}

func HashWorkerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (d *DB) GetWorkerByHardwareUUID(ctx context.Context, hardwareUUID string) (*model.Worker, error) {
	var worker model.Worker
	err := d.Pool.QueryRow(ctx, `
		SELECT id, hostname, hardware_uuid, cpu_cores, memory_bytes, tart_version, worker_version,
		       pool_size, base_image, status, registered_at, last_heartbeat,
		       auth_token_hash, auth_token_last_used_at, auth_token_revoked_at
		FROM workers
		WHERE hardware_uuid = $1
	`, hardwareUUID).Scan(
		&worker.ID,
		&worker.Hostname,
		&worker.HardwareUUID,
		&worker.CPUCores,
		&worker.MemoryBytes,
		&worker.TartVersion,
		&worker.WorkerVersion,
		&worker.PoolSize,
		&worker.BaseImage,
		&worker.Status,
		&worker.RegisteredAt,
		&worker.LastHeartbeat,
		&worker.AuthTokenHash,
		&worker.AuthTokenLastUsedAt,
		&worker.AuthTokenRevokedAt,
	)
	if err != nil {
		return nil, err
	}
	return &worker, nil
}

func (d *DB) AuthenticateWorkerToken(ctx context.Context, token string) (*model.Worker, error) {
	var worker model.Worker
	err := d.Pool.QueryRow(ctx, `
		SELECT id, hostname, hardware_uuid, cpu_cores, memory_bytes, tart_version, worker_version,
		       pool_size, base_image, status, registered_at, last_heartbeat,
		       auth_token_hash, auth_token_last_used_at, auth_token_revoked_at
		FROM workers
		WHERE auth_token_hash = $1 AND auth_token_revoked_at IS NULL
	`, HashWorkerToken(token)).Scan(
		&worker.ID,
		&worker.Hostname,
		&worker.HardwareUUID,
		&worker.CPUCores,
		&worker.MemoryBytes,
		&worker.TartVersion,
		&worker.WorkerVersion,
		&worker.PoolSize,
		&worker.BaseImage,
		&worker.Status,
		&worker.RegisteredAt,
		&worker.LastHeartbeat,
		&worker.AuthTokenHash,
		&worker.AuthTokenLastUsedAt,
		&worker.AuthTokenRevokedAt,
	)
	if err != nil {
		return nil, err
	}
	return &worker, nil
}

func (d *DB) TouchWorkerTokenUsage(ctx context.Context, workerID string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE workers
		SET auth_token_last_used_at = now()
		WHERE id = $1
	`, workerID)
	return err
}

func (d *DB) IssueWorkerToken(ctx context.Context, workerID string) (string, error) {
	token, err := GenerateWorkerToken()
	if err != nil {
		return "", err
	}

	_, err = d.Pool.Exec(ctx, `
		UPDATE workers
		SET auth_token_hash = $1,
		    auth_token_last_used_at = now(),
		    auth_token_revoked_at = NULL
		WHERE id = $2
	`, HashWorkerToken(token), workerID)
	if err != nil {
		return "", err
	}

	return token, nil
}

func IsNotFound(err error) bool {
	return err == pgx.ErrNoRows
}
