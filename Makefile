CLUSTER_LENS_VERSION := $(shell awk '/^[[:space:]]*ClusterLensVersion[[:space:]]*=/ {gsub(/"/, "", $$3); print $$3}' internal/managedcomponents/components.go)
MON_AGENT_VERSION := $(shell awk '/^[[:space:]]*MonAgentVersion[[:space:]]*=/ {gsub(/"/, "", $$3); print $$3}' internal/managedcomponents/components.go)
MENTAT_VERSION := $(shell awk '/^[[:space:]]*MentatVersion[[:space:]]*=/ {gsub(/"/, "", $$3); print $$3}' internal/managedcomponents/components.go)

DEV_COMPOSE = docker compose --env-file deploy/dev.env -f compose.yaml -f compose.dev.yaml

.PHONY: up down ps logs build test managed-component-images cluster-lens-image mon-agent-image mentat-image doctor migrate

up: build
	$(DEV_COMPOSE) up -d --wait --wait-timeout 300

down:
	$(DEV_COMPOSE) down

ps:
	$(DEV_COMPOSE) ps

logs:
	$(DEV_COMPOSE) logs -f app worker migrate

build: managed-image-archives
	docker build --build-arg VERSION=dev -t kubephos:dev .

test:
	python3 scripts/test_install.py
	python3 scripts/test_update.py
	python3 scripts/test_release.py
	python3 scripts/test_release_checks.py
	node --test components/cluster-lens/tests/*.test.cjs
	go test ./...
	cd components/cluster-lens/backend && go test ./...
	cd components/mon-agent && go test ./...
	cd components/mentat && go test ./...
	npm --prefix frontend test -- --run

managed-component-images: cluster-lens-image mon-agent-image mentat-image

cluster-lens-image:
	docker build -t kubephos/cluster-lens:$(CLUSTER_LENS_VERSION) components/cluster-lens

mon-agent-image:
	docker build -t kubephos/mon-agent:$(MON_AGENT_VERSION) components/mon-agent

mentat-image:
	docker build -t kubephos/mentat:$(MENTAT_VERSION) components/mentat

doctor:
	$(DEV_COMPOSE) exec app kubephos doctor

migrate:
	$(DEV_COMPOSE) run --rm migrate update

.PHONY: managed-image-archives
managed-image-archives: managed-component-images
	mkdir -p managed-images
	docker image save -o managed-images/cluster-lens-$(CLUSTER_LENS_VERSION).tar kubephos/cluster-lens:$(CLUSTER_LENS_VERSION)
	docker image save -o managed-images/mon-agent-$(MON_AGENT_VERSION).tar kubephos/mon-agent:$(MON_AGENT_VERSION)
	docker image save -o managed-images/mentat-$(MENTAT_VERSION).tar kubephos/mentat:$(MENTAT_VERSION)
