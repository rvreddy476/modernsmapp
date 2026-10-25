FROM golang:1.26.10-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /

COPY go.work .
COPY go.work.sum .
COPY vendor /vendor
COPY shared /shared
COPY services /services
COPY tools /tools

RUN go build -mod=vendor -o /server ./services/food-service/cmd/server/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /
COPY --from=builder /server /server
CMD ["/server"]
