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

# Build the application
RUN go build -mod=vendor -o /server ./services/feed-service/cmd/server/main.go
# One-off repair pass over the Scylla timelines (deploy/jobs/timeline-maintenance).
RUN go build -mod=vendor -o /timeline-maintenance ./services/feed-service/cmd/timeline-maintenance

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
COPY --from=builder /timeline-maintenance /timeline-maintenance
CMD ["/server"]
