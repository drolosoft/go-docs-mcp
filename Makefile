.PHONY: build install test clean

build:
	go build -o go-docs-mcp .

install:
	go build -o /usr/local/bin/go-docs-mcp .

test:
	go test -v -race ./...

clean:
	rm -f go-docs-mcp
