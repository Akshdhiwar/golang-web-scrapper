# ── Build Stage ─────────────────────────────────────────────
FROM golang:1.22-bookworm AS builder

WORKDIR /app

# Download Go dependencies first (layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the scraper and the playwright-go CLI (used to install its driver)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/bin/scraper ./cmd/main.go \
 && CGO_ENABLED=0 GOOS=linux go build -o /app/bin/playwright github.com/mxschmitt/playwright-go/cmd/playwright


# ── Runtime Stage ────────────────────────────────────────────
# Must match the Playwright version bundled with playwright-go in go.mod
# (v0.6401.0 → Playwright 1.64.1). The image ships Chromium + system deps.
FROM mcr.microsoft.com/playwright:v1.64.1-noble

WORKDIR /app

COPY --from=builder /app/bin/scraper /app/bin/playwright ./

# Feed definitions (read at runtime; edit and rebuild to change feeds)
COPY profiles.yaml companies.yaml ./

# Run as non-root, and install the playwright-go driver into that user's
# cache so startup doesn't download it (browsers come from the base image).
RUN adduser --disabled-password --gecos '' scraper \
 && mkdir -p /app/.data /app/logs \
 && chown -R scraper:scraper /app
USER scraper
RUN ./playwright install chromium

CMD ["./scraper"]
