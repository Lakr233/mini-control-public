package model

import "time"

type VMState string

const (
	VMStateStarting      VMState = "starting"
	VMStateBootstrapping VMState = "bootstrapping"
	VMStateReady         VMState = "ready"
	VMStateBusy          VMState = "busy"
	VMStateDestroying    VMState = "destroying"
	VMStateFailed        VMState = "failed"
)

type VM struct {
	ID        string  `json:"id" db:"id"`
	WorkerID  string  `json:"worker_id" db:"worker_id"`
	State     VMState `json:"state" db:"state"`
	IPAddress string  `json:"ip_address,omitempty" db:"ip_address"`

	StateSince time.Time `json:"state_since" db:"state_since"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}
