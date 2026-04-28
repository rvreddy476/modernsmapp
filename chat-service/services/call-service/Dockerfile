FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
ENV GOWORK=off

# Copy module files first for cache
COPY shared/go.mod shared/go.sum* shared/
COPY services/call-service/go.mod services/call-service/go.sum* services/call-service/

# Download dependencies
RUN cd services/call-service && go mod download

# Copy source code
COPY shared/ shared/
COPY services/call-service/ services/call-service/

# Build
RUN cd services/call-service && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/call-service ./cmd/server

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/call-service /usr/local/bin/call-service

EXPOSE 8097

ENTRYPOINT ["call-service"]
