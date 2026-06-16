package model

import "time"

type Release struct {
	ID              string    `json:"id" db:"id"`
	Version         string    `json:"version" db:"version"`
	SHA256          string    `json:"sha256" db:"sha256"`
	SizeBytes       int64     `json:"size_bytes" db:"size_bytes"`
	SigningIdentity string    `json:"signing_identity" db:"signing_identity"`
	FilePath        string    `json:"-" db:"file_path"`
	UploadedAt      time.Time `json:"uploaded_at" db:"uploaded_at"`
}
