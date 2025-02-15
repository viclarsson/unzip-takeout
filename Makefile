.PHONY: build test clean

build: ## Build the binary
	go build -o bin/unzip-takeout

test: ## Run tests
	go test -v ./...

clean: ## Clean build artifacts
	rm -rf bin/
	go clean
