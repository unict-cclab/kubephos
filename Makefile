KUBEPHOS_IMAGE ?= kubephos
KUBEPHOS_VERSION ?= dev

export KUBEPHOS_IMAGE
export KUBEPHOS_VERSION

.PHONY: up down ps logs build test managed-component-images cluster-lens-image mon-agent-image mentat-image doctor migrate

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
	cd components/mon-agent && go test ./...
	cd components/mentat && go test ./...
	npm --prefix frontend test -- --run

managed-component-images: cluster-lens-image mon-agent-image mentat-image

cluster-lens-image:
	docker build -t kubephos/cluster-lens:v1.2.6 components/cluster-lens

mon-agent-image:
	docker build -t kubephos/mon-agent:v0.0.8 components/mon-agent

mentat-image:
	docker build -t kubephos/mentat:v0.3.5 components/mentat

doctor:
	docker compose exec app kubephos doctor

migrate:
	docker compose run --rm migrate update
