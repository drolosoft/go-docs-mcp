.PHONY: build install test clean fmt vet vuln check snapshot docker docker-smoke docker-multiarch

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

# The version the image reports. A plain local build says "dev"; pass the
# release number to check what the published image will say.
VERSION ?=

docker:
	docker build --build-arg VERSION=$(VERSION) -t go-docs-mcp .

# Builds the image for this machine and talks MCP to it over stdin with a PDF,
# a DOCX and a PNG mounted read-only. Run it before tagging a release.
docker-smoke:
	scripts/docker-smoke.sh $(or $(VERSION),0.0.0-smoke)

# Proves both release architectures build (the other one under QEMU). Nothing
# is pushed or loaded.
docker-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) .

clean:
	rm -rf go-docs-mcp dist
