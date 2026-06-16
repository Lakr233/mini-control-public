package model

import "time"

type WorkstationPowerState string

const (
	WorkstationPowerStateStarting WorkstationPowerState = "starting"
	WorkstationPowerStateRunning  WorkstationPowerState = "running"
	WorkstationPowerStateStopping WorkstationPowerState = "stopping"
	WorkstationPowerStateStopped  WorkstationPowerState = "stopped"
	WorkstationPowerStateDeleting WorkstationPowerState = "deleting"
	WorkstationPowerStateDeleted  WorkstationPowerState = "deleted"
	WorkstationPowerStateError    WorkstationPowerState = "error"
)

type WorkstationDesiredPowerState string

const (
	WorkstationDesiredPowerStateRunning WorkstationDesiredPowerState = "running"
	WorkstationDesiredPowerStateStopped WorkstationDesiredPowerState = "stopped"
	WorkstationDesiredPowerStateDeleted WorkstationDesiredPowerState = "deleted"
)

type Workstation struct {
	ID                string                       `json:"id" db:"id"`
	MemberID          string                       `json:"member_id" db:"member_id"`
	WorkerID          string                       `json:"worker_id" db:"worker_id"`
	Slot              int                          `json:"slot" db:"slot"`
	VMName            string                       `json:"vm_name" db:"vm_name"`
	PowerState        WorkstationPowerState        `json:"power_state" db:"power_state"`
	DesiredPowerState WorkstationDesiredPowerState `json:"desired_power_state" db:"desired_power_state"`
	IPAddress         string                       `json:"ip_address,omitempty" db:"ip_address"`
	LastError         string                       `json:"last_error,omitempty" db:"last_error"`
	CreatedAt         time.Time                    `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at" db:"updated_at"`
}
