package model

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// GenerateWorkerID creates a deterministic worker ID from the hardware UUID.
func GenerateWorkerID(hardwareUUID string) string {
	h := sha256.Sum256([]byte(hardwareUUID))
	return fmt.Sprintf("w-%x", h[:6])
}

type WorkerStatus string

const (
	WorkerStatusActive   WorkerStatus = "active"
	WorkerStatusDraining WorkerStatus = "draining"
	WorkerStatusOffline  WorkerStatus = "offline"
)

type Worker struct {
	ID                  string       `json:"id" db:"id"`
	Hostname            string       `json:"hostname" db:"hostname"`
	HardwareUUID        string       `json:"hardware_uuid" db:"hardware_uuid"`
	CPUCores            int          `json:"cpu_cores" db:"cpu_cores"`
	MemoryBytes         int64        `json:"memory_bytes" db:"memory_bytes"`
	TartVersion         string       `json:"tart_version" db:"tart_version"`
	WorkerVersion       string       `json:"worker_version" db:"worker_version"`
	PoolSize            int          `json:"pool_size" db:"pool_size"`
	BaseImage           string       `json:"base_image" db:"base_image"`
	Status              WorkerStatus `json:"status" db:"status"`
	RegisteredAt        time.Time    `json:"registered_at" db:"registered_at"`
	LastHeartbeat       *time.Time   `json:"last_heartbeat,omitempty" db:"last_heartbeat"`
	AuthTokenHash       *string      `json:"-" db:"auth_token_hash"`
	AuthTokenLastUsedAt *time.Time   `json:"-" db:"auth_token_last_used_at"`
	AuthTokenRevokedAt  *time.Time   `json:"-" db:"auth_token_revoked_at"`
}
