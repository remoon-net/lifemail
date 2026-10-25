BINARY  := lifemail
IMAGE   ?= shynome/lifemail
GOARCH  ?= amd64
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build gobuild web check test verify vet fmt image clean db-status

# Build the UI into web/build. It is served from disk at runtime
# (LIFEMAIL_WEB_DIR), not embedded in the binary.
web:
	cd web && pnpm install --frozen-lockfile && pnpm run build

build: web
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/lifemail

gobuild:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/lifemail

# Backend checks plus the frontend type check.
check:
	@test -z "$$(gofmt -l cmd internal web)" || (gofmt -l cmd internal web && exit 1)
	go vet ./...
	cd web && pnpm run check

test:
	go test ./...

# Everything that must be green before a commit. Run it once per change set,
# then commit; only rerun if a file changed after the check.
verify: check test

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal web

image: build
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

db-status:
	./$(BINARY) db status

clean:
	rm -f $(BINARY)
	rm -rf web/build/* web/.svelte-kit
	@touch web/build/.gitkeep
