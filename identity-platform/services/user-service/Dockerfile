FROM golang:1.25-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /app
ENV GOWORK=off
COPY shared/ shared/
COPY services/user-service/ services/user-service/
WORKDIR /app/services/user-service
RUN go mod download
RUN go build -o /server ./cmd/server/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
# No migrations are copied in. This service does not migrate its own database:
# it applies its embedded database/setup.sql (additive, idempotent) and then
# verifies its schema preconditions, refusing to start when the deployment
# pipeline has not run. See cmd/server/main.go and identity-shared/store/schemaguard.
WORKDIR /
CMD ["/server"]
