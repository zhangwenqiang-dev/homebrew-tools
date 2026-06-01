APP := cm
VERSION ?= dev
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: test build clean

test:
	GOCACHE=$(GOCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) go build -ldflags "-X main.version=$(VERSION)" -o bin/$(APP) ./cmd/$(APP)

clean:
	rm -rf bin .cache
