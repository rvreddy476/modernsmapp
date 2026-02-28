FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
ENV GOWORK=off

# Copy module files first for cache
COPY shared/go.mod shared/go.sum* shared/
COPY services/ws-gateway/go.mod services/ws-gateway/go.sum* services/ws-gateway/

# Download dependencies
RUN cd services/ws-gateway && go mod download

# Copy source code
COPY shared/ shared/
COPY services/ws-gateway/ services/ws-gateway/

# Build
RUN cd services/ws-gateway && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/ws-gateway ./cmd/server

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/ws-gateway /usr/local/bin/ws-gateway

EXPOSE 8093

ENTRYPOINT ["ws-gateway"]
