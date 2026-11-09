.PHONY: build test clean

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o dist/xuanyu .

test:
	go test ./...
	go vet ./...

clean:
	rm -rf dist
