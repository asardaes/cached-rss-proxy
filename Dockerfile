FROM golang:1.27-alpine AS builder

WORKDIR /app
COPY main.go .

# Initialize a standard module and build a statically linked binary
RUN go mod init cached-rss-proxy && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cached-rss-proxy main.go

FROM alpine:latest

# Install CA certificates to allow HTTPS calls to the upstream URL
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/cached-rss-proxy .

ENV PORT=8080
EXPOSE 8080

CMD ["/app/cached-rss-proxy"]
