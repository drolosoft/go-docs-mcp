.PHONY: build install test clean

build:
	go build -o go-pdf-mcp .

install:
	go build -o /usr/local/bin/go-pdf-mcp .

test:
	go test -v -race ./...

clean:
	rm -f go-pdf-mcp
