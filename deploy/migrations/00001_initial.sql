-- Single bootstrap schema for fresh installs.
-- No incremental migration chain is maintained.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

DO $$
BEGIN
    CREATE TYPE worker_status AS ENUM ('active', 'draining', 'offline');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE vm_state AS ENUM ('starting', 'bootstrapping', 'ready', 'busy', 'destroying', 'failed');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE workstation_power_state AS ENUM (
        'starting',
        'running',
        'stopping',
        'stopped',
        'deleting',
        'deleted',
        'error'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE workstation_desired_power_state AS ENUM (
        'running',
        'stopped',
        'deleted'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS workers (
    id                       TEXT PRIMARY KEY,
    hostname                 TEXT NOT NULL,
    hardware_uuid            TEXT UNIQUE NOT NULL,
    cpu_cores                INT NOT NULL,
    memory_bytes             BIGINT NOT NULL,
    tart_version             TEXT NOT NULL DEFAULT '',
    worker_version           TEXT NOT NULL DEFAULT '',
    pool_size                INT NOT NULL DEFAULT 2,
    base_image               TEXT NOT NULL DEFAULT 'minictl-tahoe-base',
    status                   worker_status NOT NULL DEFAULT 'active',
    registered_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat           TIMESTAMPTZ,
    auth_token_hash          TEXT,
    auth_token_last_used_at  TIMESTAMPTZ,
    auth_token_revoked_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS releases (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    version          TEXT NOT NULL UNIQUE,
    sha256           TEXT NOT NULL,
    size_bytes       BIGINT NOT NULL,
    signing_identity TEXT NOT NULL,
    file_path        TEXT NOT NULL,
    uploaded_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_releases_uploaded ON releases(uploaded_at DESC);

CREATE TABLE IF NOT EXISTS members (
    id           TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    access_sub   TEXT NOT NULL UNIQUE,
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'member',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_members_email ON members(email);

CREATE TABLE IF NOT EXISTS workstations (
    id                  TEXT PRIMARY KEY,
    member_id           TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    worker_id           TEXT NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    slot                INT NOT NULL CHECK (slot >= 0),
    vm_name             TEXT NOT NULL UNIQUE,
    power_state         workstation_power_state NOT NULL,
    desired_power_state workstation_desired_power_state NOT NULL,
    ip_address          TEXT NOT NULL DEFAULT '',
    last_error          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (worker_id, slot)
);

CREATE INDEX IF NOT EXISTS idx_workstations_worker_id ON workstations(worker_id);
CREATE INDEX IF NOT EXISTS idx_workstations_member_id ON workstations(member_id);

CREATE TABLE IF NOT EXISTS vms (
    id              TEXT PRIMARY KEY,
    worker_id       TEXT NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    state           vm_state NOT NULL DEFAULT 'starting',
    ip_address      TEXT,
    state_since     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_vms_worker_state ON vms(worker_id, state);
