# KubePhos

KubePhos is a web platform for reproducible Kubernetes scheduling experiments on
Proxmox. Provision infrastructure, configure workloads and network conditions,
compare placement strategies, and inspect the results from one interface.

## What it does

- Provisions VM templates, Harbor, NFS and K3s clusters on Proxmox.
- Supports scheduler, descheduler, autoscaler integrations.
- Provides an application catalog and Locust load generation, including external
  HTTP destinations and Redis Cluster workloads.
- Runs experiments and comparison suites with sequential repetitions.
- Collects response times, throughput, resource usage and scheduling metrics.
- Compares strategies and exports datasets and publication-quality figures.
- Keeps configurations and experiments private, with explicit read sharing.

Multi cluster provisioning supports Cilium ClusterMesh. Experiment execution on
multi cluster groups is not yet available.

## Install on Linux

Requirements: Linux, Bash, OpenSSL, Docker Engine and Docker Compose **2.23.1 or
newer**. The host needs network access to Proxmox, the provisioned VMs and the image
registries used by your experiments. The installer does not modify Docker or your
host's package configuration.

From a release archive or a checkout:

```bash
git clone https://github.com/unict-cclab/kubephos.git
cd kubephos
./install.sh
```

Enter the administrator email when prompted. The installer creates a separate installation in `~/.local/share/kubephos`,
generates unique credentials, creates the initial `admin` account and starts the
services. Open **http://localhost:8080**. Read your generated login credentials:

```bash
cat ~/.local/share/kubephos/credentials.json
```

To install for a server URL or select a published release:

```bash
./install.sh --url http://SERVER_IP:8080 --version <release-tag>
```

The first account is created before the application port is published. Repeating
`./install.sh` resumes the installation without rotating secrets or deleting data.
For an existing installation with a custom path, repeat the same `--directory`.
To apply changes to `~/.local/share/kubephos/.env`, run from your clone or extracted
release directory:

```bash
./update.sh
```

For a custom installation path, use `./update.sh --directory /path/to/installation`.
The updater applies configuration changes, including Keycloak redirect URLs.
Both scripts can also be invoked by absolute path from any working directory.
They display high-level progress; detailed output is saved privately in
`install.log` and `update.log` inside the installation directory.

See [installation and operations](docs/installation.md) for HTTPS, updates, backups
and troubleshooting. Published releases are available on
[GitHub Releases](https://github.com/unict-cclab/kubephos/releases).

## First experiment

1. Add a Proxmox connection under **Infrastructure**.
2. Create a VM template, Harbor registry and NFS server.
3. Create a Kubernetes cluster and wait for it to become ready.
4. Create a configuration with an application, load profile and optional strategies.
5. Run the configuration or combine configurations in a comparison suite.
6. Inspect **Experiments → Statistics / Plots** and export the results.

An experiment records its execution configuration. Later edits to a reusable
configuration do not rewrite the settings of completed runs.

## Development

Development is separate from an installation:

```bash
make up
```

This builds the local image and uses `compose.dev.yaml` with `deploy/dev.env`,
public development credentials, the `kubephos-dev` Compose project and separate
volumes. Open **http://127.0.0.1:8088** and create a local administrator in the UI.
Do not apply the development override to an installed instance.

## Documentation

- [Installation, updates and backups](docs/installation.md)
- [Development and checks](docs/development.md)
- [Architecture and configuration](docs/architecture.md)
- [Experiments, applications and metrics](docs/experiments.md)
- [Repository review and verification](docs/review.md)
- [Contributing](CONTRIBUTING.md)
- [Security reporting](SECURITY.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)

## License

Original KubePhos code is licensed under the [MIT License](LICENSE). Dependencies
and upstream applications retain their respective licenses.
