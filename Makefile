.PHONY: all build-server build-worker build-frontend frontend-install clean test

all: build-server build-worker

# ─── Server (Go) ───

build-server:
	cd server && go build -o ../bin/minicontrol-server ./cmd/minicontrol-server

test-server:
	cd server && go test ./...

# ─── Worker (Swift) ───

build-worker:
	cd worker && swift build -c release
	mkdir -p bin
	cp worker/.build/release/MiniControlWorker bin/minicontrol-worker

# ─── Frontend (React + pnpm) ───

frontend-install:
	cd frontend && pnpm install

build-frontend:
	cd frontend && pnpm build

test-worker:
	cd worker && swift test

# ─── DB ───

db-migrate:
	psql $${DATABASE_URL:-postgres://localhost:5432/minicontrol} -f server/internal/db/migrations/00001_initial.sql

db-create:
	createdb minicontrol 2>/dev/null || true
	$(MAKE) db-migrate

# ─── Combined ───

test: test-server test-worker

clean:
	rm -rf bin/
	cd server && go clean
	cd worker && swift package clean

# ─── Install ───

install-worker: build-worker
	install -m 755 bin/minicontrol-worker /usr/local/bin/minicontrol-worker
