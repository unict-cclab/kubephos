FROM node:24.20.0-alpine3.23 AS web

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend ./
COPY internal/webui/static/styles.css /src/internal/webui/static/styles.css
RUN npm run build

FROM docker:29.8.0-cli AS dockercli

FROM golang:1.25.5-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kubephos ./cmd/kubephos
RUN set -eu; \
    for manifest in plugins/*/plugin.yaml; do \
      plugin_dir="${manifest%/plugin.yaml}"; \
      plugin_name="${plugin_dir##*/}"; \
      executable="${plugin_name}-plugin"; \
      test -d "cmd/${executable}"; \
      target="/out/plugins/${plugin_name}"; \
      mkdir -p "${target}"; \
      CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${target}/${executable}" "./cmd/${executable}"; \
      cp "${manifest}" "${target}/plugin.yaml"; \
    done

FROM alpine:3.23

ARG TARGETARCH
RUN apk add --no-cache ca-certificates curl git skopeo tzdata && case "${TARGETARCH}" in amd64) KUBECTL_SHA256=123d8c8844f46b1244c547fffb3c17180c0c26dac9890589fe7e67763298748e; HELM_SHA256=15e041a93a590dce8100f39385cd98c84a765c9e36aeeb9e2dc6ff9e4769e2e0 ;; arm64) KUBECTL_SHA256=9f9d9c44a7b5264515ac9da5991584e2395bd50662e651132337e7b4d0c56f8f; HELM_SHA256=67f58155079ff9ffab98ba5c88daff0ed9b542f3a4732f5dd426dde7dd0f5244 ;; *) exit 1 ;; esac && curl -fsSLo /tmp/kubectl "https://dl.k8s.io/release/v1.36.0/bin/linux/${TARGETARCH}/kubectl" && printf '%s  %s\n' "${KUBECTL_SHA256}" /tmp/kubectl | sha256sum -c - && install -m 0755 /tmp/kubectl /usr/local/bin/kubectl && curl -fsSLo /tmp/helm.tgz "https://get.helm.sh/helm-v3.21.3-linux-${TARGETARCH}.tar.gz" && printf '%s  %s\n' "${HELM_SHA256}" /tmp/helm.tgz | sha256sum -c - && tar -xzf /tmp/helm.tgz -C /tmp && install -m 0755 "/tmp/linux-${TARGETARCH}/helm" /usr/local/bin/helm && rm -rf /tmp/kubectl /tmp/helm.tgz "/tmp/linux-${TARGETARCH}" && addgroup -S -g 10001 kubephos && adduser -S -D -H -u 10001 -G kubephos kubephos && mkdir -p /var/lib/kubephos && chown 10001:10001 /var/lib/kubephos
COPY --from=build /out/kubephos /usr/local/bin/kubephos
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/plugins /opt/kubephos/plugins
COPY catalog/applications /opt/kubephos/catalog/applications
COPY migrations /opt/kubephos/migrations
COPY --from=web /src/frontend/dist /opt/kubephos/web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubephos"]
CMD ["serve"]
