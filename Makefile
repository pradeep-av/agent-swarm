BIN_DIR := bin

.PHONY: all build swarmd swarm-agent clean vet test

all: build

build: swarmd swarm-agent

swarmd:
	go build -o $(BIN_DIR)/swarmd ./cmd/swarmd

swarm-agent:
	go build -o $(BIN_DIR)/swarm-agent ./cmd/swarm-agent

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
