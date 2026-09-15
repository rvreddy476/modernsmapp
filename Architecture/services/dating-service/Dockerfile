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
RUN go build -mod=vendor -o /server ./services/dating-service/cmd/server/main.go
# Lane D9: the DPDP workers ship in the same image. Compose and the chart's
# worker start them with an explicit command; CMD stays the server.
RUN go build -mod=vendor -o /data-exporter ./services/dating-service/cmd/data-exporter
RUN go build -mod=vendor -o /data-purger ./services/dating-service/cmd/data-purger

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
COPY --from=builder /data-exporter /data-exporter
COPY --from=builder /data-purger /data-purger
CMD ["/server"]
