KUBEPHOS_IMAGE ?= kubephos
KUBEPHOS_VERSION ?= dev

export KUBEPHOS_IMAGE
export KUBEPHOS_VERSION

.PHONY: up down ps logs build test cluster-lens-image doctor migrate

up: build
	docker compose up -d

down:
	docker compose down

ps:
	docker compose ps

logs:
	docker compose logs -f app worker migrate

build:
	docker build --build-arg VERSION=$(KUBEPHOS_VERSION) -t $(KUBEPHOS_IMAGE):$(KUBEPHOS_VERSION) .

test:
	go test ./...
	cd components/cluster-lens/backend && go test ./...
	npm --prefix frontend test -- --run

cluster-lens-image:
	docker build -t kubephos/cluster-lens:dev components/cluster-lens

doctor:
	docker compose exec app kubephos doctor

migrate:
	docker compose run --rm migrate update
