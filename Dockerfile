# ── Build Stage ─────────────────────────────────────────────
FROM golang:1.21-bookworm AS builder

WORKDIR /app

# Install system dependencies for Playwright
RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Download Go dependencies first (layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/bin/scraper ./cmd/main.go


# ── Runtime Stage ────────────────────────────────────────────
FROM mcr.microsoft.com/playwright:v1.42.0-jammy

WORKDIR /app

# Copy the compiled binary
COPY --from=builder /app/bin/scraper .

# Install Playwright browsers (Chromium only)
RUN npx playwright install chromium

# Run as non-root
RUN adduser --disabled-password --gecos '' scraper && chown -R scraper:scraper /app
USER scraper

EXPOSE 8080

CMD ["./scraper"]
