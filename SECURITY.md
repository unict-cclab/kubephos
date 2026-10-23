# Security

Report vulnerabilities privately through the repository's
[security advisory form](https://github.com/unict-cclab/kubephos/security/advisories/new).
If private reporting is unavailable, contact a repository maintainer privately;
do not put credentials or exploit details in a public issue.

Include the affected version, reproduction steps and expected impact. Use
synthetic data and redact credentials, access tokens and kubeconfig contents.

The installer generates independent database and authentication secrets. The
installation `.env`, initial `credentials.json`, PostgreSQL data, SeaweedFS data
and credential encryption key are private operational data. Never commit them.
The development Compose override uses public local credentials and separate
volumes; it is not an installation configuration.

Use HTTPS through a reverse proxy for remote access outside a trusted network.
Proxmox tokens and imported strategies can make privileged infrastructure changes:
only install plugins and strategies from sources you trust.

Security fixes target the latest stable release. Older release branches do not
currently have a separate maintenance commitment.
