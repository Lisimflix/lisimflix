# Run before pushing code (CONTRIBUTING.md "Checks"). CI runs this same target.
.PHONY: check
check:
	@test -z "$$(gofmt -l server)" || { echo "gofmt needed:"; gofmt -l server; exit 1; }
	go -C server vet ./...
	go -C server test -count=1 ./...
	npm --prefix web run generate:api
	git diff --exit-code -- web/src/api/schema.d.ts
	npm --prefix web run test:contracts
	npm --prefix web run lint
	npm --prefix web run typecheck
	npm --prefix web run build
