# Local shortcuts. No Docker or database needed: with DATABASE_URL unset the
# scraper keeps its state in .data/seen.json (see STORE_FILE).

.PHONY: run dry watch test build

run:   ## one run, sends to Telegram
	RUN_ONCE=true go run ./cmd

dry:   ## one run, prints Telegram messages instead of sending
	RUN_ONCE=true DRY_RUN=true go run ./cmd

watch: ## keep running on CRON_SCHEDULE (default hourly)
	go run ./cmd

test:
	go test ./...

build:
	go build -ldflags="-w -s" -o scraper ./cmd
