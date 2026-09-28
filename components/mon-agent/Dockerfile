FROM golang:1.25.5-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /mon-agent .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /mon-agent /mon-agent
USER nonroot:nonroot
ENTRYPOINT ["/mon-agent"]
