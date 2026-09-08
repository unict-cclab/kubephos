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

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 kubephos && adduser -S -D -H -u 10001 -G kubephos kubephos && mkdir -p /var/lib/kubephos && chown 10001:10001 /var/lib/kubephos
COPY --from=build /out/kubephos /usr/local/bin/kubephos
COPY --from=build /out/reference-plugin /opt/kubephos/plugins/reference/reference-plugin
COPY plugins/reference/plugin.yaml /opt/kubephos/plugins/reference/plugin.yaml
COPY --from=build /out/proxmox-plugin /opt/kubephos/plugins/proxmox/proxmox-plugin
COPY plugins/proxmox/plugin.yaml /opt/kubephos/plugins/proxmox/plugin.yaml
COPY --from=build /out/proxmox-vm-plugin /opt/kubephos/plugins/proxmox-vm/proxmox-vm-plugin
COPY plugins/proxmox-vm/plugin.yaml /opt/kubephos/plugins/proxmox-vm/plugin.yaml
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubephos"]
CMD ["serve"]
