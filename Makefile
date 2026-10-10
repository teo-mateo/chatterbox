VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/teo-mateo/chatterbox/internal/cli.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test test-integration lint ergo-up ergo-down release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/chatterbox ./cmd/chatterbox

# Unit tests only: nothing external.
test:
	go test -race ./internal/...

# Integration and acceptance tests against a private Ergo (fetched on first use).
test-integration: build
	go test -count=1 -timeout 20m ./test/...

lint:
	go vet ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipped"

# A local Ergo for manual work. Prints the address and how to connect.
ergo-up:
	go run ./test/ergo/cmd/ergo-dev up

ergo-down:
	go run ./test/ergo/cmd/ergo-dev down

release:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/chatterbox-$(VERSION)-$$os-$$arch ./cmd/chatterbox || exit 1; \
	done

clean:
	rm -rf bin dist
