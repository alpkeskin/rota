# Build stage
FROM golang:1.25.3-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application.
# TARGETARCH is provided automatically by Docker Buildx so multi-platform builds
# (linux/amd64, linux/arm64) produce a binary matching the target image arch.
ARG TARGETARCH
# VERSION is passed by the release workflow (the git tag); local builds report "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s -extldflags '-static' -X github.com/alpkeskin/rota/core/internal/version.Version=${VERSION}" \
    -a -installsuffix cgo \
    -o /app/bin/server \
    ./cmd/server/main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bin/server /app/server

# Change ownership
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose ports
EXPOSE 8000 8001

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8001/health || exit 1

# Run the application
ENTRYPOINT ["/app/server"]
