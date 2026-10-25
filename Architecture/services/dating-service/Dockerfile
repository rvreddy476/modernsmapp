FROM golang:1.26.10-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /

# Copy workspace definition
COPY go.work .
COPY go.work.sum .
COPY vendor /vendor
COPY tools /tools

# Copy shared module
COPY shared /shared

# Copy all services to satisfy go.work
COPY services /services

# Build the application
RUN go build -mod=vendor -o /server ./services/dating-service/cmd/server/main.go
# Lane D9: the DPDP workers ship in the same image. Compose and the chart's
# worker start them with an explicit command; CMD stays the server.
RUN go build -mod=vendor -o /data-exporter ./services/dating-service/cmd/data-exporter
RUN go build -mod=vendor -o /data-purger ./services/dating-service/cmd/data-purger
# The media claim reconcile tool (cmd/media-claims): run in the pod with
# `kubectl exec deploy/dating-service -- /media-claims` (dry run) or `-apply`.
RUN go build -mod=vendor -o /media-claims ./services/dating-service/cmd/media-claims

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
COPY --from=builder /data-exporter /data-exporter
COPY --from=builder /data-purger /data-purger
COPY --from=builder /media-claims /media-claims
CMD ["/server"]
