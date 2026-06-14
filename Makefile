.PHONY: test cover lint fmt tidy integration clean help

test: ## Run unit tests with race detector
	go test -race ./...

cover: ## Run tests and open an HTML coverage report
	go test -coverprofile=coverage.out -coverpkg=./... ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

lint: ## Run golangci-lint (must be installed)
	golangci-lint run

fmt: ## Format code
	gofmt -s -w .
	go run golang.org/x/tools/cmd/goimports@latest -w .

integration: ## Run integration tests against real databases (needs Docker)
	go test -race -tags=integration -timeout=15m ./integration/...

tidy: ## Tidy modules
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
