# KubePhos

## Requirements

- Docker Engine
- Docker Compose

## Start

```bash
cp .env.example .env
docker compose up -d --build
docker compose ps
```

Open <http://localhost:8080>.

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
docker compose build
```
