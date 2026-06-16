package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Lakr233/minis/server/internal/model"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	database := &DB{Pool: pool}
	if err := database.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return database, nil
}

func (d *DB) Close() {
	d.Pool.Close()
}

// ─── Workers ───

func (d *DB) UpsertWorker(ctx context.Context, w *model.Worker) error {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO workers (id, hostname, hardware_uuid, cpu_cores, memory_bytes, tart_version, worker_version, pool_size, base_image, status, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (id) DO UPDATE SET
			hostname = EXCLUDED.hostname,
			cpu_cores = EXCLUDED.cpu_cores,
			memory_bytes = EXCLUDED.memory_bytes,
			tart_version = EXCLUDED.tart_version,
			worker_version = EXCLUDED.worker_version,
			status = 'active',
			last_heartbeat = now()
	`, w.ID, w.Hostname, w.HardwareUUID, w.CPUCores, w.MemoryBytes, w.TartVersion, w.WorkerVersion, w.PoolSize, w.BaseImage, model.WorkerStatusActive)
	return err
}

func (d *DB) UpdateHeartbeat(ctx context.Context, workerID string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE workers SET last_heartbeat = now(), status = 'active' WHERE id = $1`, workerID)
	return err
}

func (d *DB) SetWorkerOffline(ctx context.Context, workerID string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE workers SET status = 'offline' WHERE id = $1`, workerID)
	return err
}

func (d *DB) ListWorkers(ctx context.Context) ([]model.Worker, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, hostname, hardware_uuid, cpu_cores, memory_bytes, tart_version, worker_version, pool_size, base_image, status, registered_at, last_heartbeat FROM workers ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		var w model.Worker
		if err := rows.Scan(&w.ID, &w.Hostname, &w.HardwareUUID, &w.CPUCores, &w.MemoryBytes, &w.TartVersion, &w.WorkerVersion, &w.PoolSize, &w.BaseImage, &w.Status, &w.RegisteredAt, &w.LastHeartbeat); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return workers, nil
}

func (d *DB) AssignHostname(ctx context.Context, hardwareUUID string) (string, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x6d696e696374726c)); err != nil {
		return "", err
	}

	var hostname string
	err = tx.QueryRow(ctx, `SELECT hostname FROM workers WHERE hardware_uuid = $1`, hardwareUUID).Scan(&hostname)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return hostname, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}

	var nextNum int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(
			CASE WHEN hostname ~ '^mac-\d+$'
			THEN CAST(SUBSTRING(hostname FROM 'mac-(\d+)') AS INT)
			ELSE 0 END
		), 0) + 1 FROM workers
	`).Scan(&nextNum); err != nil {
		return "", err
	}

	hostname = fmt.Sprintf("mac-%d", nextNum)
	workerID := model.GenerateWorkerID(hardwareUUID)

	if _, err := tx.Exec(ctx, `
		INSERT INTO workers (
			id, hostname, hardware_uuid, cpu_cores, memory_bytes,
			tart_version, worker_version, pool_size, base_image, status, registered_at
		) VALUES ($1, $2, $3, 0, 0, '', '', 2, 'minictl-tahoe-base', 'offline', now())
		ON CONFLICT (id) DO UPDATE SET hostname = EXCLUDED.hostname
	`, workerID, hostname, hardwareUUID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return hostname, nil
}

// ─── VMs ───

func (d *DB) UpsertVM(ctx context.Context, vm *model.VM) error {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO vms (id, worker_id, state, ip_address, state_since, created_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			ip_address = EXCLUDED.ip_address,
			state_since = now()
	`, vm.ID, vm.WorkerID, vm.State, vm.IPAddress)
	return err
}

func (d *DB) UpdateVMState(ctx context.Context, vmID string, state model.VMState) error {
	_, err := d.Pool.Exec(ctx, `UPDATE vms SET state = $1, state_since = now() WHERE id = $2`, state, vmID)
	return err
}

func (d *DB) DeleteVM(ctx context.Context, vmID string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM vms WHERE id = $1`, vmID)
	return err
}

func (d *DB) SetWorkerVMsOffline(ctx context.Context, workerID string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM vms WHERE worker_id = $1`, workerID)
	return err
}

func (d *DB) DeleteMissingVMsForWorker(ctx context.Context, workerID string, activeVMIDs []string) ([]string, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if len(activeVMIDs) == 0 {
		deleted, err := collectVMIDsForWorker(ctx, tx, workerID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM vms WHERE worker_id = $1`, workerID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return deleted, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT id
		FROM vms
		WHERE worker_id = $1 AND NOT (id = ANY($2))
	`, workerID, activeVMIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deleted []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		deleted = append(deleted, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(deleted) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM vms WHERE id = ANY($1)`, deleted); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return deleted, nil
}

func collectVMIDsForWorker(ctx context.Context, tx pgx.Tx, workerID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM vms WHERE worker_id = $1`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vmIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		vmIDs = append(vmIDs, id)
	}
	return vmIDs, rows.Err()
}

func (d *DB) ListVMsByWorker(ctx context.Context, workerID string) ([]model.VM, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, worker_id, state, ip_address, state_since, created_at FROM vms WHERE worker_id = $1`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vms []model.VM
	for rows.Next() {
		var vm model.VM
		if err := rows.Scan(&vm.ID, &vm.WorkerID, &vm.State, &vm.IPAddress, &vm.StateSince, &vm.CreatedAt); err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return vms, nil
}

func (d *DB) GetVM(ctx context.Context, vmID string) (*model.VM, error) {
	var vm model.VM
	err := d.Pool.QueryRow(ctx, `SELECT id, worker_id, state, ip_address, state_since, created_at FROM vms WHERE id = $1`, vmID).Scan(
		&vm.ID, &vm.WorkerID, &vm.State, &vm.IPAddress, &vm.StateSince, &vm.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &vm, nil
}

func (d *DB) ListAllVMs(ctx context.Context) ([]model.VM, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id, worker_id, state, ip_address, state_since, created_at FROM vms ORDER BY worker_id, state_since`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vms []model.VM
	for rows.Next() {
		var vm model.VM
		if err := rows.Scan(&vm.ID, &vm.WorkerID, &vm.State, &vm.IPAddress, &vm.StateSince, &vm.CreatedAt); err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return vms, nil
}
