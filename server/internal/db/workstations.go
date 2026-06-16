package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Lakr233/minis/server/internal/model"
)

const workstationAllocationLock = int64(0x6d696e69637773)

// Workers whose last heartbeat is older than this are not schedulable.
const schedulableHeartbeatWindow = 30 * time.Second

// Claim and scheduling failures, surfaced so API handlers can match them
// with errors.Is instead of comparing error message text.
var (
	ErrWorkstationBeingDeleted  = errors.New("workstation is being deleted")
	ErrNoActiveWorkers          = errors.New("no active workers available")
	ErrNoSlotsAvailable         = errors.New("no workstation slots available")
	ErrTargetWorkerFull         = errors.New("no workstation slots available on target worker")
	ErrTargetWorkerUnavailable  = errors.New("target worker is not available")
)

type DesiredWorkstation struct {
	ID                string
	VMName            string
	Slot              int
	DesiredPowerState model.WorkstationDesiredPowerState
}

type WorkstationStatusReport struct {
	WorkstationID string
	PowerState    model.WorkstationPowerState
	IPAddress     string
	LastError     string
}

func scanWorkstation(scanner interface {
	Scan(dest ...any) error
}) (*model.Workstation, error) {
	var workstation model.Workstation
	err := scanner.Scan(
		&workstation.ID,
		&workstation.MemberID,
		&workstation.WorkerID,
		&workstation.Slot,
		&workstation.VMName,
		&workstation.PowerState,
		&workstation.DesiredPowerState,
		&workstation.IPAddress,
		&workstation.LastError,
		&workstation.CreatedAt,
		&workstation.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &workstation, nil
}

func (d *DB) GetWorkstationByMemberID(ctx context.Context, memberID string) (*model.Workstation, error) {
	return scanWorkstation(d.Pool.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1
	`, memberID))
}

func (d *DB) GetWorkstationByIDForMember(ctx context.Context, memberID, workstationID string) (*model.Workstation, error) {
	return scanWorkstation(d.Pool.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1 AND id = $2
	`, memberID, workstationID))
}

func (d *DB) GetWorkstationByMemberAndWorkerID(ctx context.Context, memberID, workerID string) (*model.Workstation, error) {
	return scanWorkstation(d.Pool.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1 AND worker_id = $2
	`, memberID, workerID))
}

func (d *DB) ListWorkstationsByMemberID(ctx context.Context, memberID string) ([]model.Workstation, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1
		ORDER BY worker_id, slot, created_at
	`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workstations []model.Workstation
	for rows.Next() {
		workstation, err := scanWorkstation(rows)
		if err != nil {
			return nil, err
		}
		workstations = append(workstations, *workstation)
	}
	return workstations, rows.Err()
}

func (d *DB) EnsureWorkstationRunningByMemberID(ctx context.Context, memberID string) (*model.Workstation, error) {
	workstation, err := d.GetWorkstationByMemberID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	return d.EnsureWorkstationRunningByIDForMember(ctx, memberID, workstation.ID)
}

func (d *DB) EnsureWorkstationRunningByIDForMember(ctx context.Context, memberID, workstationID string) (*model.Workstation, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	workstation, err := scanWorkstation(tx.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1 AND id = $2
		FOR UPDATE
	`, memberID, workstationID))
	if err != nil {
		return nil, err
	}

	switch {
	case workstation.DesiredPowerState == model.WorkstationDesiredPowerStateDeleted:
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return workstation, nil
	case workstation.PowerState == model.WorkstationPowerStateDeleting, workstation.PowerState == model.WorkstationPowerStateDeleted:
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return workstation, nil
	default:
		workstation, err = claimExistingWorkstation(ctx, tx, workstation)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return workstation, nil
}

func (d *DB) GetWorkstationByID(ctx context.Context, workstationID string) (*model.Workstation, error) {
	return scanWorkstation(d.Pool.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE id = $1
	`, workstationID))
}

func (d *DB) ListAllWorkstations(ctx context.Context) ([]model.Workstation, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		ORDER BY worker_id, slot
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workstations []model.Workstation
	for rows.Next() {
		workstation, err := scanWorkstation(rows)
		if err != nil {
			return nil, err
		}
		workstations = append(workstations, *workstation)
	}
	return workstations, rows.Err()
}

func (d *DB) ListDesiredWorkstationsByWorker(ctx context.Context, workerID string) ([]DesiredWorkstation, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, vm_name, slot, desired_power_state
		FROM workstations
		WHERE worker_id = $1
		ORDER BY slot
	`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var desired []DesiredWorkstation
	for rows.Next() {
		var item DesiredWorkstation
		if err := rows.Scan(&item.ID, &item.VMName, &item.Slot, &item.DesiredPowerState); err != nil {
			return nil, err
		}
		desired = append(desired, item)
	}
	return desired, rows.Err()
}

func (d *DB) ClaimWorkstation(ctx context.Context, memberID string, preferredWorkerID string) (*model.Workstation, bool, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, workstationAllocationLock); err != nil {
		return nil, false, err
	}

	workers, err := listSchedulableWorkers(ctx, tx, time.Now().Add(-schedulableHeartbeatWindow))
	if err != nil {
		return nil, false, err
	}
	if len(workers) == 0 {
		return nil, false, ErrNoActiveWorkers
	}

	existing, err := listAllocatedSlots(ctx, tx)
	if err != nil {
		return nil, false, err
	}

	best, err := selectWorkstationCandidate(workers, existing, preferredWorkerID)
	if err != nil {
		return nil, false, err
	}

	workstation, err := insertWorkstation(ctx, tx, memberID, best.workerID, best.slot)
	if err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return workstation, true, nil
}

type workstationCandidate struct {
	workerID string
	slot     int
	count    int
}

func selectWorkstationCandidate(workers []model.Worker, existing map[string]map[int]bool, preferredWorkerID string) (workstationCandidate, error) {
	if preferredWorkerID != "" {
		for _, worker := range workers {
			if worker.ID != preferredWorkerID {
				continue
			}

			slot, ok := firstAvailableSlot(worker, existing[worker.ID])
			if !ok {
				return workstationCandidate{}, ErrTargetWorkerFull
			}
			return workstationCandidate{
				workerID: worker.ID,
				slot:     slot,
				count:    len(existing[worker.ID]),
			}, nil
		}
		return workstationCandidate{}, ErrTargetWorkerUnavailable
	}

	best := workstationCandidate{slot: -1}
	for _, worker := range workers {
		slot, ok := firstAvailableSlot(worker, existing[worker.ID])
		if !ok {
			continue
		}

		count := len(existing[worker.ID])
		isFirstCandidate := best.slot == -1
		if isFirstCandidate || count < best.count || (count == best.count && (worker.ID < best.workerID || (worker.ID == best.workerID && slot < best.slot))) {
			best = workstationCandidate{workerID: worker.ID, slot: slot, count: count}
		}
	}
	if best.slot == -1 {
		return workstationCandidate{}, ErrNoSlotsAvailable
	}
	return best, nil
}

func firstAvailableSlot(worker model.Worker, occupied map[int]bool) (int, bool) {
	for slot := 0; slot < worker.PoolSize; slot++ {
		if occupied[slot] {
			continue
		}
		return slot, true
	}
	return 0, false
}

func insertWorkstation(ctx context.Context, tx pgx.Tx, memberID, workerID string, slot int) (*model.Workstation, error) {
	workstationID := uuid.NewString()
	vmName := generateWorkstationVMName(workstationID)

	return scanWorkstation(tx.QueryRow(ctx, `
		INSERT INTO workstations (
			id, member_id, worker_id, slot, vm_name,
			power_state, desired_power_state, ip_address, last_error, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 'starting', 'running', '', '', now(), now())
		RETURNING id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		          ip_address, last_error, created_at, updated_at
	`, workstationID, memberID, workerID, slot, vmName))
}

func claimExistingWorkstation(ctx context.Context, tx pgx.Tx, workstation *model.Workstation) (*model.Workstation, error) {
	switch workstation.PowerState {
	case model.WorkstationPowerStateDeleting, model.WorkstationPowerStateDeleted:
		return nil, ErrWorkstationBeingDeleted
	case model.WorkstationPowerStateStopped, model.WorkstationPowerStateError, model.WorkstationPowerStateStopping:
		return scanWorkstation(tx.QueryRow(ctx, `
			UPDATE workstations
			SET desired_power_state = 'running',
			    power_state = CASE
			        WHEN power_state IN ('stopped', 'error') THEN 'starting'::workstation_power_state
			        ELSE power_state
			    END,
			    last_error = '',
			    updated_at = now()
			WHERE id = $1
			RETURNING id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
			          ip_address, last_error, created_at, updated_at
		`, workstation.ID))
	default:
		if workstation.DesiredPowerState != model.WorkstationDesiredPowerStateRunning {
			return scanWorkstation(tx.QueryRow(ctx, `
				UPDATE workstations
				SET desired_power_state = 'running',
				    updated_at = now()
				WHERE id = $1
				RETURNING id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
				          ip_address, last_error, created_at, updated_at
			`, workstation.ID))
		}
		return workstation, nil
	}
}

func (d *DB) UpdateWorkstationDesiredPowerState(ctx context.Context, memberID string, desired model.WorkstationDesiredPowerState) (*model.Workstation, error) {
	workstation, err := d.GetWorkstationByMemberID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	return d.UpdateWorkstationDesiredPowerStateByID(ctx, memberID, workstation.ID, desired)
}

func (d *DB) UpdateWorkstationDesiredPowerStateByID(ctx context.Context, memberID, workstationID string, desired model.WorkstationDesiredPowerState) (*model.Workstation, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	workstation, err := scanWorkstation(tx.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE member_id = $1 AND id = $2
		FOR UPDATE
	`, memberID, workstationID))
	if err != nil {
		return nil, err
	}

	if workstation.PowerState == model.WorkstationPowerStateDeleting || workstation.PowerState == model.WorkstationPowerStateDeleted {
		return nil, ErrWorkstationBeingDeleted
	}

	nextPowerState := workstation.PowerState
	switch desired {
	case model.WorkstationDesiredPowerStateRunning:
		if workstation.PowerState == model.WorkstationPowerStateStopped || workstation.PowerState == model.WorkstationPowerStateError {
			nextPowerState = model.WorkstationPowerStateStarting
		}
	case model.WorkstationDesiredPowerStateStopped:
		if workstation.PowerState == model.WorkstationPowerStateStarting || workstation.PowerState == model.WorkstationPowerStateRunning {
			nextPowerState = model.WorkstationPowerStateStopping
		}
	case model.WorkstationDesiredPowerStateDeleted:
		nextPowerState = model.WorkstationPowerStateDeleting
	default:
		return nil, fmt.Errorf("unsupported desired power state %q", desired)
	}

	workstation, err = scanWorkstation(tx.QueryRow(ctx, `
		UPDATE workstations
		SET desired_power_state = $2,
		    power_state = $3,
		    updated_at = now()
		WHERE member_id = $1 AND id = $4
		RETURNING id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		          ip_address, last_error, created_at, updated_at
	`, memberID, desired, nextPowerState, workstationID))
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return workstation, nil
}

func (d *DB) UpdateWorkstationStatus(ctx context.Context, workerID string, report WorkstationStatusReport) error {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	workstation, err := scanWorkstation(tx.QueryRow(ctx, `
		SELECT id, member_id, worker_id, slot, vm_name, power_state, desired_power_state,
		       ip_address, last_error, created_at, updated_at
		FROM workstations
		WHERE id = $1
		FOR UPDATE
	`, report.WorkstationID))
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	if workstation.WorkerID != workerID {
		return fmt.Errorf("workstation does not belong to worker")
	}

	ipAddress := strings.TrimSpace(report.IPAddress)
	lastError := strings.TrimSpace(report.LastError)

	if report.PowerState == model.WorkstationPowerStateDeleted && workstation.DesiredPowerState == model.WorkstationDesiredPowerStateDeleted {
		if _, err := tx.Exec(ctx, `DELETE FROM workstations WHERE id = $1`, workstation.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if workstation.DesiredPowerState == model.WorkstationDesiredPowerStateDeleted {
		if _, err := tx.Exec(ctx, `
			UPDATE workstations
			SET last_error = $2,
			    updated_at = now()
			WHERE id = $1
		`, workstation.ID, lastError); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE workstations
		SET power_state = $2,
		    ip_address = $3,
		    last_error = $4,
		    updated_at = now()
		WHERE id = $1
	`, workstation.ID, report.PowerState, ipAddress, lastError); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func listSchedulableWorkers(ctx context.Context, tx pgx.Tx, cutoff time.Time) ([]model.Worker, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, hostname, hardware_uuid, cpu_cores, memory_bytes, tart_version, worker_version,
		       pool_size, base_image, status, registered_at, last_heartbeat
		FROM workers
		WHERE status = 'active' AND last_heartbeat IS NOT NULL AND last_heartbeat >= $1
		ORDER BY hostname, id
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		var worker model.Worker
		if err := rows.Scan(
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
		); err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func listAllocatedSlots(ctx context.Context, tx pgx.Tx) (map[string]map[int]bool, error) {
	rows, err := tx.Query(ctx, `SELECT worker_id, slot FROM workstations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	allocated := make(map[string]map[int]bool)
	for rows.Next() {
		var workerID string
		var slot int
		if err := rows.Scan(&workerID, &slot); err != nil {
			return nil, err
		}
		if allocated[workerID] == nil {
			allocated[workerID] = make(map[int]bool)
		}
		allocated[workerID][slot] = true
	}
	return allocated, rows.Err()
}

func generateWorkstationVMName(workstationID string) string {
	trimmed := strings.ReplaceAll(workstationID, "-", "")
	if len(trimmed) > 12 {
		trimmed = trimmed[:12]
	}
	return "ws-" + trimmed
}
