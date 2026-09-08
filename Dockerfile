FROM node:24.20.0-alpine3.23 AS web

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend ./
COPY internal/webui/static/styles.css /src/internal/webui/static/styles.css
RUN npm run build

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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/k3s-plugin ./cmd/k3s-plugin

FROM alpine:3.23

RUN apk add --no-cache ca-certificates git tzdata && addgroup -S -g 10001 kubephos && adduser -S -D -H -u 10001 -G kubephos kubephos && mkdir -p /var/lib/kubephos && chown 10001:10001 /var/lib/kubephos
COPY --from=build /out/kubephos /usr/local/bin/kubephos
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
COPY --from=build /out/k3s-plugin /opt/kubephos/plugins/k3s/k3s-plugin
COPY plugins/k3s/plugin.yaml /opt/kubephos/plugins/k3s/plugin.yaml
COPY catalog/applications /opt/kubephos/catalog/applications
COPY --from=web /src/frontend/dist /opt/kubephos/web
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubephos"]
CMD ["serve"]
