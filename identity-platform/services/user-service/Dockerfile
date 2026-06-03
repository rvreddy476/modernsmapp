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
WORKDIR /
CMD ["/server"]
