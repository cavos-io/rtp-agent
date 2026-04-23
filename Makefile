.PHONY: test cover lint clean help

test: ## Run unit tests for stable packages (adapters, library, core)
	go test ./adapter/... ./library/... ./core/... -v

cover: ## Run tests and show coverage for stable packages
	go test ./adapter/... ./library/... ./core/... -coverprofile=coverage.out
	go tool cover -html=coverage.out

lint: ## Run golangci-lint
	golangci-lint run

clean: ## Remove test artifacts
	rm -f coverage.out coverage.txt

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
