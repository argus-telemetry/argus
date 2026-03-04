.PHONY: build test lint clean certify-smoke

BINARY_DIR := bin

build:
	go build -o $(BINARY_DIR)/argus ./cmd/argus
	go build -o $(BINARY_DIR)/argus-sim ./cmd/argus-sim
	go build -o $(BINARY_DIR)/argus-certify ./cmd/certify

test:
	go test ./... -v -race -count=1

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -rf $(BINARY_DIR)

check: vet lint test

certify-smoke:
	./bin/argus-certify run --scenario simulator/scenarios/alarm_storm.yaml
	./bin/argus-certify run --scenario simulator/scenarios/steady_state.yaml
