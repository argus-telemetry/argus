.PHONY: build test lint clean

BINARY_DIR := bin

build:
	go build -o $(BINARY_DIR)/argus ./cmd/argus
	go build -o $(BINARY_DIR)/argus-sim ./cmd/argus-sim

test:
	go test ./... -v -race -count=1

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -rf $(BINARY_DIR)

check: vet lint test
