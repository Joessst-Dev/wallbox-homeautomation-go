.PHONY: help css build test vet lint tidy run docker-build compose-up compose-local install clean

TAILWIND := internal/web/tailwind/tailwindcss
APP_CSS  := internal/web/static/app.css

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'

css: ## Compile Tailwind into the embedded app.css (needs the standalone CLI)
	@test -x $(TAILWIND) || { echo "Tailwind CLI not found at $(TAILWIND) — download the standalone binary there"; exit 1; }
	cd internal/web/tailwind && ./tailwindcss -c tailwind.config.js -i input.css -o ../static/app.css --minify

build: ## Build the wha binary for the host
	go build -trimpath -ldflags="-s -w" -o bin/wha ./cmd/wha

build-arm64: ## Cross-compile a static arm64 binary for the Raspberry Pi
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/wha-arm64 ./cmd/wha

test: ## Run all tests
	go test ./...

vet: ## go vet
	go vet ./...

lint: ## golangci-lint (if installed)
	golangci-lint run

tidy: ## Tidy go.mod
	go mod tidy

run: ## Run locally against a broker on localhost (config via WHA_* env)
	WHA_MQTT_BROKER=tcp://localhost:1883 WHA_DB_PATH=./wha.db go run ./cmd/wha

docker-build: ## Build the arm64 image with buildx
	docker buildx build --platform linux/arm64 -t wha:latest .

compose-up: ## Run the stack with the published GHCR image (Pi/prod)
	docker compose up -d

compose-local: ## Run the stack building wha from source (local dev)
	docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build

install: ## Guided Raspberry Pi setup (prompts for creds, secures broker, starts stack)
	bash scripts/install.sh

clean: ## Remove build artifacts
	rm -rf bin/ *.db *.db-wal *.db-shm
