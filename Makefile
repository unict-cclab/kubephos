.PHONY: up down ps logs build test doctor migrate

up:
	docker compose up -d --build

down:
	docker compose down

ps:
	docker compose ps

logs:
	docker compose logs -f app worker migrate

build:
	docker compose build

test:
	go test ./...

doctor:
	docker compose exec app kubephos doctor

migrate:
	docker compose run --rm migrate update
