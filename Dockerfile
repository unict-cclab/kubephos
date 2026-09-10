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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/reference-plugin ./cmd/reference-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxmox-plugin ./cmd/proxmox-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxmox-vm-plugin ./cmd/proxmox-vm-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxmox-topology-plugin ./cmd/proxmox-topology-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/application-inspector-plugin ./cmd/application-inspector-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/application-deployer-plugin ./cmd/application-deployer-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/load-session-plugin ./cmd/load-session-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/metrics-collector-plugin ./cmd/metrics-collector-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/target-binding-plugin ./cmd/target-binding-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/secondary-scheduler-plugin ./cmd/secondary-scheduler-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/k3s-plugin ./cmd/k3s-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nfs-plugin ./cmd/nfs-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/harbor-plugin ./cmd/harbor-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nfs-csi-plugin ./cmd/nfs-csi-plugin
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/managed-observability-plugin ./cmd/managed-observability-plugin

FROM alpine:3.23

ARG TARGETARCH
RUN apk add --no-cache ca-certificates curl git tzdata && case "${TARGETARCH}" in amd64) KUBECTL_SHA256=123d8c8844f46b1244c547fffb3c17180c0c26dac9890589fe7e67763298748e; HELM_SHA256=15e041a93a590dce8100f39385cd98c84a765c9e36aeeb9e2dc6ff9e4769e2e0 ;; arm64) KUBECTL_SHA256=9f9d9c44a7b5264515ac9da5991584e2395bd50662e651132337e7b4d0c56f8f; HELM_SHA256=67f58155079ff9ffab98ba5c88daff0ed9b542f3a4732f5dd426dde7dd0f5244 ;; *) exit 1 ;; esac && curl -fsSLo /tmp/kubectl "https://dl.k8s.io/release/v1.36.0/bin/linux/${TARGETARCH}/kubectl" && printf '%s  %s\n' "${KUBECTL_SHA256}" /tmp/kubectl | sha256sum -c - && install -m 0755 /tmp/kubectl /usr/local/bin/kubectl && curl -fsSLo /tmp/helm.tgz "https://get.helm.sh/helm-v3.21.3-linux-${TARGETARCH}.tar.gz" && printf '%s  %s\n' "${HELM_SHA256}" /tmp/helm.tgz | sha256sum -c - && tar -xzf /tmp/helm.tgz -C /tmp && install -m 0755 "/tmp/linux-${TARGETARCH}/helm" /usr/local/bin/helm && rm -rf /tmp/kubectl /tmp/helm.tgz "/tmp/linux-${TARGETARCH}" && addgroup -S -g 10001 kubephos && adduser -S -D -H -u 10001 -G kubephos kubephos && mkdir -p /var/lib/kubephos && chown 10001:10001 /var/lib/kubephos
COPY --from=build /out/kubephos /usr/local/bin/kubephos
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/reference-plugin /opt/kubephos/plugins/reference/reference-plugin
COPY plugins/reference/plugin.yaml /opt/kubephos/plugins/reference/plugin.yaml
COPY --from=build /out/proxmox-plugin /opt/kubephos/plugins/proxmox/proxmox-plugin
COPY plugins/proxmox/plugin.yaml /opt/kubephos/plugins/proxmox/plugin.yaml
COPY --from=build /out/proxmox-vm-plugin /opt/kubephos/plugins/proxmox-vm/proxmox-vm-plugin
COPY plugins/proxmox-vm/plugin.yaml /opt/kubephos/plugins/proxmox-vm/plugin.yaml
COPY --from=build /out/proxmox-topology-plugin /opt/kubephos/plugins/proxmox-topology/proxmox-topology-plugin
COPY plugins/proxmox-topology/plugin.yaml /opt/kubephos/plugins/proxmox-topology/plugin.yaml
COPY --from=build /out/application-inspector-plugin /opt/kubephos/plugins/application-inspector/application-inspector-plugin
COPY plugins/application-inspector/plugin.yaml /opt/kubephos/plugins/application-inspector/plugin.yaml
COPY --from=build /out/application-deployer-plugin /opt/kubephos/plugins/application-deployer/application-deployer-plugin
COPY plugins/application-deployer/plugin.yaml /opt/kubephos/plugins/application-deployer/plugin.yaml
COPY --from=build /out/load-session-plugin /opt/kubephos/plugins/load-session/load-session-plugin
COPY plugins/load-session/plugin.yaml /opt/kubephos/plugins/load-session/plugin.yaml
COPY --from=build /out/metrics-collector-plugin /opt/kubephos/plugins/metrics-collector/metrics-collector-plugin
COPY plugins/metrics-collector/plugin.yaml /opt/kubephos/plugins/metrics-collector/plugin.yaml
COPY --from=build /out/target-binding-plugin /opt/kubephos/plugins/target-binding/target-binding-plugin
COPY plugins/target-binding/plugin.yaml /opt/kubephos/plugins/target-binding/plugin.yaml
COPY --from=build /out/secondary-scheduler-plugin /opt/kubephos/plugins/secondary-scheduler/secondary-scheduler-plugin
COPY plugins/secondary-scheduler/plugin.yaml /opt/kubephos/plugins/secondary-scheduler/plugin.yaml
COPY --from=build /out/k3s-plugin /opt/kubephos/plugins/k3s/k3s-plugin
COPY plugins/k3s/plugin.yaml /opt/kubephos/plugins/k3s/plugin.yaml
COPY --from=build /out/nfs-plugin /opt/kubephos/plugins/nfs/nfs-plugin
COPY plugins/nfs/plugin.yaml /opt/kubephos/plugins/nfs/plugin.yaml
COPY --from=build /out/harbor-plugin /opt/kubephos/plugins/harbor/harbor-plugin
COPY plugins/harbor/plugin.yaml /opt/kubephos/plugins/harbor/plugin.yaml
COPY --from=build /out/nfs-csi-plugin /opt/kubephos/plugins/nfs-csi/nfs-csi-plugin
COPY plugins/nfs-csi/plugin.yaml /opt/kubephos/plugins/nfs-csi/plugin.yaml
COPY --from=build /out/managed-observability-plugin /opt/kubephos/plugins/managed-observability/managed-observability-plugin
COPY plugins/managed-observability/plugin.yaml /opt/kubephos/plugins/managed-observability/plugin.yaml
COPY catalog/applications /opt/kubephos/catalog/applications
COPY --from=web /src/frontend/dist /opt/kubephos/web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubephos"]
CMD ["serve"]
