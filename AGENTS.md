# AGENTS.md

This file provides guidance when working with code in this repository.

## Project Overview

Mac mini cluster management system for browser-controlled macOS VMs. A Go server coordinates Swift workers running on Mac mini hosts and serves a React + TypeScript frontend for browser terminal and desktop access.

## Build & Test Commands

```bash
make all              # Build server and worker
make build-server     # Go server -> bin/minicontrol-server
make build-worker     # Swift worker -> bin/minicontrol-worker
make frontend-install # cd frontend && pnpm install
make build-frontend   # cd frontend && pnpm build
make test             # Run all tests
make test-server      # cd server && go test ./...
make test-worker      # cd worker && swift test
make clean            # Remove build artifacts
```

## Running Locally

Create local configuration files from examples before running services:

```bash
cp server-config.example.yaml server-config.yaml
cp deploy/config.example.yaml deploy/config.yaml
cp install/config.example.yaml install/config.yaml
cp .env.example .env
```

```bash
docker compose up -d
cd worker && swift build && .build/debug/MiniControlWorker --config ../install/config.yaml
```

## Architecture

```text
Repo Root
├── frontend/                       React + TypeScript browser UI (pnpm + Vite)
├── server/                         Go control plane + static frontend serving
└── worker/                         Swift daemon running on each Mac mini
```

## Key Design Decisions

- Communication is REST-based. Workers talk to the server over HTTPS REST endpoints plus WebSocket proxy sessions.
- Browser UI is served by the Go server. Docker builds the React frontend and bakes it into the server image.
- Browser auth is Cloudflare Access based. The origin validates `Cf-Access-Jwt-Assertion` before serving member endpoints.
- Workers check for new releases on heartbeat. The server stores signed binaries and validates Mach-O code signatures on upload.
- VNC stays on the WebSocket relay; browser terminal uses a server-side SSH bridge with `xterm.js`.

## Deployment

- `compose.yml` is the Docker Compose file.
- `deploy/config.example.yaml` is the public example; copy it to ignored `deploy/config.yaml` for real deployments.
- `.env.example` is the public example; copy it to ignored `.env` and set `CLOUDFLARED_TOKEN`.
- `deploy/mnt/` stores runtime data and is ignored.
- Public hostnames, Cloudflare Access settings, admin tokens, enrollment tokens, tunnel tokens, and host passwords must stay out of Git.

## Worker Releases

Use `scripts/build-and-publish.sh` for release builds:

```bash
CODE_SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
NOTARY_KEYCHAIN_PROFILE="notary-profile" \
./scripts/build-and-publish.sh \
  --server https://minis-api.example.com \
  --token <admin_token> \
  --version <VERSION>
```

## Database

PostgreSQL 17. The bootstrap schema lives in `server/internal/db/migrations/00001_initial.sql`. Docker auto-applies `deploy/migrations/00001_initial.sql` only on a fresh Postgres init; existing DBs should be reset and recreated instead of trying to replay incremental migrations.

## Important Rules

- Server changes require rebuilding/restarting the server deployment.
- Worker changes should be published through `scripts/build-and-publish.sh`.
- Workers auto-update via heartbeat when a newer release exists on the server.
- Install script placeholder `__SERVER_ADDR__` is replaced by the server handler at serve time.
- Default all code, comments, identifiers, and user-facing UI copy to English unless explicitly requested otherwise.
- SwiftUI layout must use explicit stack spacing; do not rely on default `VStack`/`HStack` spacing.
- SwiftUI spacing must be limited to 4, 8, and 16 points.
- SwiftUI typography must be limited to title, body, caption, and footnote; use bold for emphasis instead of introducing extra font styles.

## API Reference

Full REST API documentation is in `docs/api.md`. Key endpoints:

- `GET /api/v1/workers` — list workers and VMs
- `GET /install.sh` — host setup script
