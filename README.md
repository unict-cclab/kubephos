# KubePhos

KubePhos is a web platform for creating Kubernetes environments on Proxmox, deploying applications and scheduling strategies, generating controlled load, and running reproducible experiments.

It can also be used as a development environment: create a cluster, export its kubeconfig, deploy an application, inspect logs and dashboards, start or stop load, and iterate on schedulers, deschedulers, or autoscalers.

## Features

- Manage Proxmox connections and VM templates.
- Provision Harbor registries and NFS servers on dedicated VMs.
- Create, recreate, inspect, and delete K3s clusters with zonal node pools.
- Install the managed Kubernetes platform, observability, service mesh, chaos injection, storage, and experiment components.
- Export kubeconfig files for external development tools.
- Use a versioned application catalog without adding application-specific logic to the core.
- Import scheduler, descheduler, and autoscaler images and mirror them through Harbor.
- Define temporal and geographic load profiles with an input preview.
- Configure zonal network injection and experiment monitoring.
- Run experiments and comparison suites in background workers.
- Repeat runs sequentially and calculate aggregate statistics.
- Inspect live progress, logs, metrics, application topology, and cluster topology.
- Export plots and application graphs as publication-quality PNG or PDF files.
- Preserve experiment configurations, run history, results, and artifacts.

Every lifecycle step is validated before execution. KubePhos verifies preconditions, waits for the expected health state, and checks postconditions before continuing.

## Workflow

1. Add a Proxmox connection.
2. Create a Proxmox VM template.
3. Provision Harbor and NFS services.
4. Create a Kubernetes cluster and its node pools.
5. Select or import an application.
6. Create an experiment configuration.
7. Run an experiment or a comparison suite.
8. Inspect and export results.

Applications, infrastructure providers, deployment components, and experiment strategies use versioned plugin contracts. New integrations should not require changes to the KubePhos core.

## Requirements

- Linux server
- Docker Engine
- Docker Compose
- Network access from the KubePhos server to Proxmox and the provisioned VMs

## Quick start

### 1. Copy Compose

Copy `compose.yaml` to the server that will run KubePhos:

```bash
mkdir -p /opt/kubephos
scp compose.yaml user@server:/opt/kubephos/compose.yaml
```

### 2. Configure

Create `/opt/kubephos/.env`:

```dotenv
KUBEPHOS_IMAGE=ghcr.io/unict-cclab/kubephos
KUBEPHOS_VERSION=latest
KUBEPHOS_PORT=8080
KUBEPHOS_BIND_ADDRESS=0.0.0.0
KUBEPHOS_WORKER_CONCURRENCY=4
KUBEPHOS_WORKER_POLL_INTERVAL=500ms
POSTGRES_PASSWORD=replace-with-a-strong-password
```

Use a release tag such as `v1.0.0` instead of `latest` when a fixed deployment version is required.

### 3. Start

```bash
cd /opt/kubephos
docker compose pull
docker compose up -d
docker compose ps
```

### 4. Open KubePhos

Open `http://SERVER_IP:8080` and create the initial administrator account.

Use a TLS reverse proxy or restrict access to a trusted network when KubePhos is reachable outside the local machine.

## First experiment

From the web interface:

1. Open **Infrastructure** and add a Proxmox connection.
2. Create a VM template, Harbor registry, and NFS server.
3. Open **Kubernetes** and create a cluster.
4. Wait until every cluster component is healthy.
5. Open **Configurations** and create an experiment configuration.
6. Select the cluster, application, load profile, and optional strategies.
7. Run the configuration and choose the number of sequential runs.
8. Open **Experiments** to follow progress and inspect results.

## Update

Set `KUBEPHOS_VERSION` in `/opt/kubephos/.env`, then run:

```bash
cd /opt/kubephos
docker compose pull
docker compose up -d
docker compose ps
```

Database changes are applied automatically by Liquibase before the application starts.

## Maintenance

```bash
docker compose ps
docker compose logs -f app worker
docker compose exec app kubephos doctor
docker compose restart app worker
docker compose down
```

PostgreSQL data, SeaweedFS objects, credentials, and migration state are stored in Docker volumes. Do not remove the volumes unless all KubePhos data should be deleted.

## Architecture

- **Application image**: API server, React frontend, CLI, built-in plugins, application catalog, and database changesets.
- **Worker**: executes validated operations and experiments in the background.
- **PostgreSQL**: stores configuration, lifecycle state, logs, and experiment history.
- **SeaweedFS**: stores manifests, results, plots, and other artifacts.
- **Plugins**: provide infrastructure, application, strategy, load, monitoring, and lifecycle capabilities.

The application and worker use the same versioned KubePhos image. PostgreSQL and SeaweedFS use tagged images defined in `compose.yaml`.

## Development

Run the test suites:

```bash
make test
```

Build and start a local image:

```bash
make build
make up
```

Useful commands:

```bash
make ps
make logs
make doctor
make down
```

## Release

Pushing a `v*` Git tag starts the release workflow. The workflow builds the KubePhos image and publishes the release tag and `latest` to GitHub Container Registry.

```bash
git tag v1.0.0
git push origin v1.0.0
```
