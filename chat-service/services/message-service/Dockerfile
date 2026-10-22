FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
ENV GOWORK=off

# Copy module files first for cache
COPY shared/go.mod shared/go.sum* shared/
COPY services/message-service/go.mod services/message-service/go.sum* services/message-service/

# Download dependencies
RUN cd services/message-service && go mod download

# Copy source code
COPY shared/ shared/
COPY services/message-service/ services/message-service/

# Build
RUN cd services/message-service && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/message-service ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/fieldcrypt-backfill ./cmd/fieldcrypt-backfill

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/message-service /usr/local/bin/message-service
# One-off re-seal of rows written before field encryption (run with
# kubectl exec; see cmd/fieldcrypt-backfill).
COPY --from=builder /bin/fieldcrypt-backfill /usr/local/bin/fieldcrypt-backfill

EXPOSE 8092

ENTRYPOINT ["message-service"]
