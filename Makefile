BINARY  := lifemail
GOARCH  ?= amd64
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build web check test vet fmt image clean db-status

# Build the admin console into web/build, then embed it in the binary.
web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/lifemail

# Backend checks plus the frontend type check.
check:
	@test -z "$$(gofmt -l cmd internal web)" || (gofmt -l cmd internal web && exit 1)
	go vet ./...
	cd web && npm run check

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal web

image: build
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):latest .

db-status:
	./$(BINARY) db status

clean:
	rm -f $(BINARY)
	rm -rf web/build/* web/.svelte-kit
	@touch web/build/.gitkeep
