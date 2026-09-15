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
RUN go build -mod=vendor -o /server ./services/media-service/cmd/server/main.go

FROM alpine:3.19
# Lane D5: blink liveness samples the uploaded selfie video with ffmpeg/ffprobe
# (same package as Dockerfile.worker). Also what the server's extract-frame
# route (processing.ExtractFrames) already shells out to.
RUN apk add --no-cache ffmpeg
WORKDIR /
COPY --from=builder /server /server
CMD ["/server"]
