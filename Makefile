.PHONY: build test clean release help

build:
	go build -o bin/unzip-takeout

test:
	go test -v -race ./...

clean:
	rm -rf bin/
	go clean

# Example usage: make release version=1.0.0
release:
	@if [ "$(version)" = "" ]; then \
		echo "Error: version parameter is required. Use: make release version=X.Y.Z"; \
		exit 1; \
	fi
	@if ! echo "$(version)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: version must be in format X.Y.Z"; \
		exit 1; \
	fi
	git tag -a v$(version) -m "Release v$(version)"
	git push origin v$(version)
	@echo "Release v$(version) has been created and pushed. GitHub Actions will now build and publish the release."
