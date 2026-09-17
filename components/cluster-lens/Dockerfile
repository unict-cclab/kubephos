FROM golang:1.25 AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o /cluster-lens .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /cluster-lens /cluster-lens
COPY frontend /frontend
ENV CLUSTER_LENS_ADDR=:8088
ENV CLUSTER_LENS_STATIC_DIR=/frontend
USER nonroot:nonroot
EXPOSE 8088
ENTRYPOINT ["/cluster-lens"]
