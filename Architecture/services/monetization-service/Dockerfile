FROM golang:1.24-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /

# Copy workspace definition
COPY go.work .
COPY go.work.sum .
COPY vendor /vendor

# Copy shared module
COPY shared /shared

# Copy all services to satisfy go.work
COPY services /services

# Build the application
RUN go build -mod=vendor -o /server ./services/monetization-service/cmd/server/main.go

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
CMD ["/server"]
