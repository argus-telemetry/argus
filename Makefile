.PHONY: build test integration certify lint helm-lint matrix check vet clean add-vendor

BINARY_DIR := bin

build:
	go build -o $(BINARY_DIR)/argus ./cmd/argus
	go build -o $(BINARY_DIR)/argus-sim ./cmd/argus-sim
	go build -o $(BINARY_DIR)/argus-certify ./cmd/certify

test:
	go test ./... -v -race -count=1

integration:
	go test -tags integration -timeout 120s ./test/integration/...

certify: build
	./bin/argus-certify run --scenario simulator/scenarios/alarm_storm.yaml
	./bin/argus-certify run --scenario simulator/scenarios/steady_state.yaml

lint:
	golangci-lint run ./...

helm-lint:
	helm lint --strict deploy/helm/argus

matrix: build
	./bin/argus-certify matrix --matrix-dir test/scenarios/matrix/

vet:
	go vet ./...

clean:
	rm -rf $(BINARY_DIR)

check: vet lint test

add-vendor:
	@read -p "Vendor name (e.g. huawei_u2020): " v; \
	./bin/argus-certify add-vendor --vendor $$v \
		--nfs amf,smf,upf,gnb,slice \
		--output schema/v1/
