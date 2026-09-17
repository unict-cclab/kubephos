# KubePhos

## Requirements

- Linux server
- Docker Engine with Docker Compose

## Install

Copy `compose.yaml` to the server:

```bash
mkdir -p /opt/kubephos
scp compose.yaml user@server:/opt/kubephos/compose.yaml
```

On the server, create `/opt/kubephos/.env`:

```bash
KUBEPHOS_IMAGE=ghcr.io/unict-cclab/kubephos
KUBEPHOS_VERSION=latest
KUBEPHOS_PORT=8080
KUBEPHOS_BIND_ADDRESS=0.0.0.0
KUBEPHOS_WORKER_CONCURRENCY=4
KUBEPHOS_WORKER_POLL_INTERVAL=500ms
POSTGRES_PASSWORD=replace-with-a-strong-password
```

Start KubePhos:

```bash
cd /opt/kubephos
docker compose pull
docker compose up -d
docker compose ps
```

Open `http://SERVER_IP:8080`.

## Update

Set `KUBEPHOS_VERSION` in `.env`, then run:

```bash
docker compose pull
docker compose up -d
```

## Commands

```bash
make up
make ps
make logs
make doctor
make down
```

## Development

```bash
go test ./...
make build
make up
```
