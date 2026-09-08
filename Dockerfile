FROM golang:1.25.5-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kubephos ./cmd/kubephos
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/reference-plugin ./cmd/reference-plugin

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 kubephos && adduser -S -D -H -u 10001 -G kubephos kubephos
COPY --from=build /out/kubephos /usr/local/bin/kubephos
COPY --from=build /out/reference-plugin /opt/kubephos/plugins/reference/reference-plugin
COPY plugins/reference/plugin.yaml /opt/kubephos/plugins/reference/plugin.yaml
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubephos"]
CMD ["serve"]
