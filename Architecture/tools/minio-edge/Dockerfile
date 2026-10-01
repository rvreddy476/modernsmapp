# DEV ONLY: see main.go. Standard library only, so it builds offline.
FROM golang:1.25.6-alpine AS builder
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags="-s -w" -o /minio-edge .

FROM alpine:3.19
COPY --from=builder /minio-edge /minio-edge
USER 65534
EXPOSE 9002
ENTRYPOINT ["/minio-edge"]
