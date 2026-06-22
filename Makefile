BINARY_NAME=locke-connector
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build clean test

build: build-windows build-linux build-darwin

build-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 .

build-darwin:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 .

local:
	rm -f test-connector.db
	go build $(LDFLAGS) -o dist/$(BINARY_NAME) .

test:
	go test ./...

test-verbose:
	go test -v ./...

clean:
	rm -rf dist/

fmt:
	go fmt ./...

vet:
	go vet ./...
