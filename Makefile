MODULE  := github.com/Sahil-796/scribe
BIN_DIR := bin
BINARY  := $(BIN_DIR)/scribe

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/scribe

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -l -w .
