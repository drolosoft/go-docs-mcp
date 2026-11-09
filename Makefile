.PHONY: build install test clean fmt vet vuln check snapshot manifest mcpb docker

build:
	go build -o go-docs-mcp .

install:
	go build -o /usr/local/bin/go-docs-mcp .

test:
	go test -v -race ./...

# Fails when any file needs gofmt, like the CI job does.
fmt:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# The same checks CI runs. Run it before opening a pull request.
check: fmt vet vuln
	go test -race -cover ./...

# A local build of the release binary for this machine, into dist/.
snapshot:
	goreleaser build --snapshot --clean --single-target

# Rewrites the tools of mcpb/manifest.json from the server's own tools/list.
manifest:
	go generate ./...

# A local snapshot of the whole release into dist/, the .mcpb bundle
# included, then the staged bundle checked with the MCPB CLI (needs Node).
mcpb:
	goreleaser release --snapshot --clean --skip=publish
	npx -y @anthropic-ai/mcpb@2.1.2 validate dist/mcpb

docker:
	docker build -t go-docs-mcp .

clean:
	rm -rf go-docs-mcp dist
