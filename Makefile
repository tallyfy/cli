BINARY := tallyfy
LDFLAGS := -s -w \
  -X github.com/tallyfy/cli/internal/version.Version=dev \
  -X github.com/tallyfy/cli/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
  -X github.com/tallyfy/cli/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build test lint e2e live-e2e snapshot verify-release fmt

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/tallyfy

test:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

e2e:
	go test -race ./test/e2e/...

# Live staging suite. Requires (deliberately):
#   TALLYFY_E2E=1
#   TALLYFY_E2E_CREDENTIALS=/path/to/staging credentials JSON
#   TALLYFY_E2E_CONFIRM_ORG=<the org id inside that file>
live-e2e:
	@test -n "$$TALLYFY_E2E" || (echo "set TALLYFY_E2E=1" && exit 1)
	@test -n "$$TALLYFY_E2E_CREDENTIALS" || (echo "set TALLYFY_E2E_CREDENTIALS" && exit 1)
	@test -n "$$TALLYFY_E2E_CONFIRM_ORG" || (echo "set TALLYFY_E2E_CONFIRM_ORG" && exit 1)
	go test -tags live -count=1 -v ./test/live/...

snapshot:
	goreleaser release --snapshot --clean

verify-release:
	./scripts/verify-release.sh
