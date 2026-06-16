FROM node:22-alpine AS frontend-builder
RUN corepack enable
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /minicontrol-server ./cmd/minicontrol-server/

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /minicontrol-server /usr/local/bin/minicontrol-server
COPY --from=frontend-builder /src/frontend/dist /var/lib/minicontrol/frontend

EXPOSE 5496
ENTRYPOINT ["minicontrol-server"]
CMD ["--config", "/etc/minicontrol/config.yaml"]
