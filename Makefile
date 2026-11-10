.PHONY: build test clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -s -w -X main.toolVersion=$(VERSION)

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/xuanyu .

test:
	go test ./...
	go vet ./...

clean:
	rm -rf dist
