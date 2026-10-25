FROM golang:1.26.10-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /app
ENV GOWORK=off
COPY shared/ shared/
COPY services/profile-service/ services/profile-service/
WORKDIR /app/services/profile-service
RUN go mod download
RUN go build -o /server ./cmd/server/main.go
# The media claim reconcile tool (cmd/media-claims): run in the pod with
# `kubectl exec deploy/profile-service -- /media-claims` (dry run) or `-apply`.
RUN go build -o /media-claims ./cmd/media-claims

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
COPY --from=builder /media-claims /media-claims
# No migrations are copied in. This service does not migrate its own database:
# it applies its embedded database/setup.sql (additive, idempotent) and then
# verifies its schema preconditions, refusing to start when the deployment
# pipeline has not run. See cmd/server/main.go and identity-shared/store/schemaguard.
WORKDIR /
CMD ["/server"]
