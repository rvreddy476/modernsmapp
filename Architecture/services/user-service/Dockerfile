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
RUN go build -mod=vendor -o /server ./services/user-service/cmd/server/main.go
# The media claim reconcile tool (cmd/media-claims): run in the pod with
# `kubectl exec deploy/user-service -- /media-claims` (dry run) or `-apply`.
RUN go build -mod=vendor -o /media-claims ./services/user-service/cmd/media-claims

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
COPY --from=builder /media-claims /media-claims
CMD ["/server"]
