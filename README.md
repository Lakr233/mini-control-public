# minis

Mac mini cluster management system for browser-controlled macOS VMs.

## Architecture

- **Frontend (React + TypeScript)**: browser console for inventory, terminal, and remote desktop.
- **Server (Go)**: control plane that serves the frontend, validates Cloudflare Access JWTs, and brokers proxy sessions.
- **Worker (Swift)**: daemon that runs on each Mac mini host, manages Tart VMs, and relays VNC/SSH traffic through the server.

```text
                  ┌─────────────────────┐
                  │   Server (Go)       │
                  │   Linux / Public    │
                  │                     │
                  │  Web UI  (:5496)    │
                  │  REST API           │
                  │  Postgres           │
                  │  Proxy relay        │
                  └──────────┬──────────┘
                             │ HTTPS / WebSocket via Cloudflare
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───┐  ┌──────▼─────┐  ┌─────▼──────┐
     │ Mac mini 1 │  │ Mac mini 2 │  │ Mac mini N │
     │  Worker    │  │  Worker    │  │  Worker    │
     │  ┌──┐┌──┐ │  │  ┌──┐┌──┐ │  │  ┌──┐┌──┐ │
     │  │VM││VM│ │  │  └──┘└──┘ │  │  └──┘└──┘ │
     │  └──┘└──┘ │  └────────────┘  └────────────┘
     └────────────┘
```

## Quick Start

Install frontend dependencies and build everything:

```bash
make frontend-install
make all
make test
```

Create local configuration files from the examples:

```bash
cp server-config.example.yaml server-config.yaml
cp deploy/config.example.yaml deploy/config.yaml
cp install/config.example.yaml install/config.yaml
cp .env.example .env
```

Generate production tokens before deploying:

```bash
openssl rand -hex 32 # admin_token
openssl rand -hex 32 # enrollment_token
```

### Server

For a Docker Compose deployment, fill in `deploy/config.yaml` and `.env`, then run:

```bash
docker compose up --build -d
```

For a local server build:

```bash
cd frontend && pnpm install && pnpm build
cd ../server
go build -o minicontrol-server ./cmd/minicontrol-server
./minicontrol-server --config ../server-config.yaml
```

### Worker

On a Mac mini host, put the enrollment token in a local file and run the installer from your worker-facing hostname:

```bash
printf '%s\n' '<enrollment_token>' > ~/mini-control-enroll.key
chmod 600 ~/mini-control-enroll.key
sudo bash -c "$(curl -fsSL https://minis-api.example.com/install.sh)"
```

For local development:

```bash
cd worker
swift build
.build/debug/MiniControlWorker --config ../install/config.yaml
```

## Deployment Notes

Production routing is expected to use two hostnames behind Cloudflare Tunnel:

- `minis.example.com`: browser UI, protected by Cloudflare Access.
- `minis-api.example.com`: install script, worker APIs, release upload/download, and token-authenticated machine traffic.

`compose.yml` expects:

- `deploy/config.yaml`: real server configuration, copied from `deploy/config.example.yaml`.
- `.env`: contains `CLOUDFLARED_TOKEN`.
- `deploy/mnt/pgdata`: PostgreSQL data.
- `deploy/mnt/releases`: uploaded worker binaries.

Real configuration files and runtime data are ignored by Git.

## Worker Releases

Use the release script instead of manually uploading binaries:

```bash
CODE_SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
NOTARY_KEYCHAIN_PROFILE="notary-profile" \
./scripts/build-and-publish.sh \
  --server https://minis-api.example.com \
  --token <admin_token> \
  --version <VERSION>
```

The script builds the Swift worker, signs it, optionally notarizes it, and uploads the raw binary to:

```text
PUT /api/v1/releases/upload/<VERSION>
```

## Security

Do not commit real tokens, tunnel credentials, host passwords, private keys, or production configuration files. Browser entry should be protected by Cloudflare Access; the origin validates the Access JWT before serving member endpoints.

## API

See [docs/api.md](docs/api.md) for the REST API.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
