# Super basic Makefile for Rancher-Upgrader.
BINARY=rancher-upgrader
BINARY_DIR=bin

build: export GOOS=linux
build: export GOARCH=amd64
build: export CGO_ENABLED=0
build: 
	go build -o ./$(BINARY_DIR)/$(BINARY) cmd/main.go

local: 
	go build -o ./$(BINARY_DIR)/$(BINARY) cmd/main.go

clean:
	rm ./$(BINARY_DIR)/$(BINARY) && rmdir ./$(BINARY_DIR)