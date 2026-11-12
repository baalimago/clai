# go test's -p default is the logical CPU count, so an SMT machine starts twice
# as many single-processor race binaries as it has cores: each then pays about
# twice its alone time and the 30s per-binary deadline measures the scheduler
# (internal/testenv's deadline report calls that binary "starved"). Half the
# logical count is the physical count on Linux and on macOS.
QA_PROCS := $(shell (nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4) | awk '{ n = int(($$1 + 1) / 2); print n < 2 ? 2 : n }')

.PHONY: lint
lint:
	@go run honnef.co/go/tools/cmd/staticcheck@latest ./...
	@go run mvdan.cc/gofumpt@latest -w -l .
	@go fix ./...

.PHONY: qa
qa: lint
	@go test ./... -race -count=3 -cover -timeout=30s -p $(QA_PROCS)
