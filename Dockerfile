# ------------ builder ------------
FROM golang:1.25-alpine AS builder

# Install build deps (git is needed for go modules with VCS)
RUN apk add --no-cache ca-certificates git

WORKDIR /app

# Cache modules first (better layer reuse)
COPY go.mod go.sum ./
RUN go mod download -x

# Copy the rest of the source
COPY . .

# Set CGO off for fully static binary, smaller + portable
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# Build the server binary
# Adjust the path if your main is elsewhere (here it's cmd/server/main.go)
RUN go build -trimpath -ldflags="-s -w" -o /out/raw-cacher-go ./cmd/server

# ------------ runtime ------------
FROM alpine:3.21

# Install CA certs so HTTPS works
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary
COPY --from=builder /out/raw-cacher-go /app/raw-cacher-go

# Optional: copy default config
# COPY config.yaml /app/config.yaml

EXPOSE 8080

# Use a non-root user
RUN adduser -D -u 10001 appuser
USER appuser

ENTRYPOINT ["/app/raw-cacher-go"]
