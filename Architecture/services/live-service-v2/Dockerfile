FROM golang:1.25.6-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /

COPY go.work .
COPY go.work.sum .
COPY vendor /vendor
COPY tools /tools
COPY shared /shared
COPY services /services

RUN go build -mod=vendor -o /server ./services/live-service-v2/cmd/server/main.go

FROM alpine:3.19
WORKDIR /
COPY --from=builder /server /server
CMD ["/server"]
