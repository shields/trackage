# trackage build and lint targets.
#
# Conventions per github.com/shields/right-answers:
#   * Makefile is the entry point for build / test / lint / fmt.
#   * gofumpt formats; golangci-lint v2 lints.
#   * Both tools are pulled in via `go tool` directives in go.mod and
#     invoked through `go tool ...` so a fresh checkout needs nothing
#     beyond a working Go toolchain.

.PHONY: all build test coverage coverage-check lint lint-go lint-mod lint-md fmt fmt-go fmt-md hooks run clean

GO ?= go
COVERAGE_FILE ?= coverage.out

all: build

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

coverage:
	$(GO) test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@LC_ALL=C awk 'NR>1{t+=$$2;if($$3>0)c+=$$2} \
	  END{printf "Coverage: %.1f%% (%d/%d statements)\n",(t>0?100*c/t:0),c,t}' $(COVERAGE_FILE)

# Enforces 100% statement coverage per right-answers go.md.
coverage-check: coverage
	@LC_ALL=C awk 'NR>1{t+=$$2;if($$3>0)c+=$$2} \
	  END{if(c!=t){print "FAIL: coverage is not 100.0%"; exit 1}}' $(COVERAGE_FILE)

lint: lint-go lint-mod lint-md

lint-go:
	@diff=$$($(GO) tool gofumpt -d .); \
	  if [ -n "$$diff" ]; then \
	    printf '%s\n\n' "$$diff"; \
	    echo "gofumpt found formatting drift. Run 'make fmt'."; \
	    exit 1; \
	  fi
	$(GO) tool golangci-lint run ./...

lint-mod:
	@$(GO) mod tidy -diff > /dev/null

lint-md:
	bunx prettier --check '**/*.md'

fmt: fmt-go fmt-md

fmt-go:
	$(GO) tool gofumpt -w .
	$(GO) mod tidy

fmt-md:
	bunx prettier --write '**/*.md'

hooks:
	$(GO) tool lefthook install

run:
	$(GO) run ./cmd/trackage $(ARGS)

clean:
	rm -f $(COVERAGE_FILE) trackage
