FROM golang:1.26.10-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /

COPY go.work .
COPY go.work.sum .
COPY vendor /vendor
COPY tools /tools
COPY shared /shared
COPY services /services

RUN go build -mod=vendor -o /server ./services/live-service-v2/cmd/server/main.go
# The media claim reconcile tool (cmd/media-claims): run in the pod with
# `kubectl exec deploy/live-service-v2 -- /media-claims` (dry run) or `-apply`.
RUN go build -mod=vendor -o /media-claims ./services/live-service-v2/cmd/media-claims

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
COPY --from=builder /media-claims /media-claims
CMD ["/server"]
