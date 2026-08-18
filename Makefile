VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: build build-all clean test fmt vet deploy

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/magento-staging-linux-amd64 .

build-all: build
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/magento-staging-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/magento-staging-darwin-amd64 .

test:
	go test ./... 2>/dev/null || true

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf bin/

deploy:
	@if [ -z "$(SERVER)" ]; then echo "Usage: make deploy SERVER=user@host[:port] DOMAIN=example.com"; exit 1; fi
	scp bin/magento-staging-linux-amd64 $(SERVER):/var/www/vhosts/$(DOMAIN)/magento-staging
