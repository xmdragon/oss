# oss-admin build helpers
BIN_DIR := bin
BIN     := $(BIN_DIR)/oss-admin-linux-amd64
PKG     := ./cmd/oss-admin

GIT_REV := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(GIT_REV)

.PHONY: build vet test tidy clean run

build: $(BIN)

$(BIN): $(shell find . -name '*.go') $(shell find internal/web -type f)
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $@ $(PKG)
	@echo "built $@ ($$(du -h $@ | cut -f1))"

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

run:
	go run $(PKG)

clean:
	rm -rf $(BIN_DIR)
