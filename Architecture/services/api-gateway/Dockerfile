FROM golang:1.25.6-alpine AS builder

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

# Build the application — point at the package, not main.go alone, so
# sibling files (ratelimit_redis.go, probes.go, jwt_keyset.go) are
# compiled into the binary too. Building only main.go misses every
# symbol declared in those siblings (e.g. redisRateLimitMiddleware).
RUN go build -mod=vendor -o /server ./services/api-gateway/cmd/server

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
CMD ["/server"]
