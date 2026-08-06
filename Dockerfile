# Stage 1: Build the Go binary
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY main.go .

# Initialize a standard module and build a statically linked binary
RUN go mod init rss-proxy && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cached-rss-proxy main.go

# Stage 2: Create a minimal final image
FROM alpine:latest

# Install CA certificates to allow HTTPS calls to the upstream URL
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/cached-rss-proxy .

# Set defaults
ENV PORT=8080
EXPOSE 8080

CMD ["./cached-rss-proxy"]
