package db

import (
	"context"
	_ "embed"
	"fmt"
)

//go:embed migrations/00001_initial.sql
var bootstrapSchemaSQL string

func (d *DB) ensureSchema(ctx context.Context) error {
	if _, err := d.Pool.Exec(ctx, bootstrapSchemaSQL); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	if _, err := d.Pool.Exec(ctx, `
DO $$
BEGIN
	ALTER TABLE workstations DROP CONSTRAINT IF EXISTS workstations_member_id_key;
	ALTER TABLE workstations DROP CONSTRAINT IF EXISTS workstations_member_id_worker_id_key;
	ALTER TABLE workstations DROP CONSTRAINT IF EXISTS workstations_member_worker_id_key;

	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conrelid = 'workstations'::regclass
		  AND conname = 'workstations_slot_check'
		  AND pg_get_constraintdef(oid) = 'CHECK ((slot >= 0))'
	) THEN
		ALTER TABLE workstations DROP CONSTRAINT IF EXISTS workstations_slot_check;
		ALTER TABLE workstations ADD CONSTRAINT workstations_slot_check CHECK (slot >= 0);
	END IF;
END $$;
`); err != nil {
		return fmt.Errorf("ensure workstation constraints: %w", err)
	}
	return nil
}
