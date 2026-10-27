WEBASSETS := server/internal/webassets/dist
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)

# Run before pushing code (CONTRIBUTING.md "Checks"). CI runs this same target.
# The web build comes first so the command is tested against the real assets.
.PHONY: check web-assets copy-web-assets build image
check:
	@test -z "$$(gofmt -l server)" || { echo "gofmt needed:"; gofmt -l server; exit 1; }
	npm --prefix web run generate:api
	git diff --exit-code -- web/src/api/schema.d.ts
	npm --prefix web run test:contracts
	npm --prefix web run lint
	npm --prefix web run typecheck
	npm --prefix web run build
	$(MAKE) copy-web-assets
	go -C server vet ./...
	go -C server test -race -count=1 ./...

# Build the web app and copy it into the Go embed folder.
web-assets:
	npm --prefix web run build
	$(MAKE) copy-web-assets

copy-web-assets:
	find $(WEBASSETS) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	cp -R web/dist/. $(WEBASSETS)/

# Static binary at server/bin/lisimflix (needs the web assets).
build: web-assets
	CGO_ENABLED=0 go -C server build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/lisimflix ./cmd/lisimflix

# Container image (builds web and server inside Docker).
image:
	docker build --build-arg VERSION=$(VERSION) -t lisimflix:$(VERSION) -t lisimflix:m1 .
