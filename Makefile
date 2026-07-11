.PHONY: lint
lint:
	@go run honnef.co/go/tools/cmd/staticcheck@latest ./...
	@go run mvdan.cc/gofumpt@latest -w -l .

.PHONY: qa
qa: lint
	@go test ./... -race -count=3 -timeout=30s
