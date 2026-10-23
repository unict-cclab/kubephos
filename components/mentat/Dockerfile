FROM golang:1.26.10-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mentat .

FROM alpine:3.23
COPY --from=build /mentat /usr/local/bin/mentat
COPY LICENSE /usr/share/doc/mentat/LICENSE
EXPOSE 2112 2113
ENTRYPOINT ["/usr/local/bin/mentat"]
