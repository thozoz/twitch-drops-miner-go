VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v1.0.0)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)

LDFLAGS := -s -w -X tdm/pkg/version.Version=$(VERSION) -X tdm/pkg/version.Commit=$(COMMIT) -X tdm/pkg/version.Date=$(DATE)

.PHONY: all build build-linux-amd64 build-linux-arm64 docker test lint clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o dist/tdm ./cmd/tdm

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/tdm-linux-amd64 ./cmd/tdm

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/tdm-linux-arm64 ./cmd/tdm

docker:
	docker build -t tdm:latest .

test:
	go test -v ./...

lint:
	golangci-lint run

clean:
	rm -rf dist
