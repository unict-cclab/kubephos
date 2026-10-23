# Third-party notices

The root MIT license covers original KubePhos code. It does not replace the
licenses of dependencies, research artifacts, models or container images.

- Mentat includes code with an existing MIT notice. Preserve
  [components/mentat/LICENSE](components/mentat/LICENSE).
- Geist fonts retain their bundled notices in `frontend/public/fonts/` and their
  npm package. The frontend build includes the font assets it uses.
- Go and npm dependencies are recorded in `go.mod`, `go.sum` and the component
  modules, and in `frontend/package.json` and `frontend/package-lock.json`.
- Catalog applications refer to upstream manifests and images. These applications
  remain subject to their upstream licenses; they are not relicensed by KubePhos.
- PostgreSQL, Keycloak, SeaweedFS, Liquibase and the Kubernetes tools run as separate
  components and retain their own licenses and notices.
