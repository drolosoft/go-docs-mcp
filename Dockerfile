FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The build stage has no git, so the binary reports version "dev". The flag
# makes that explicit and keeps a git worktree (where .git is a file that
# points outside the context) from breaking the build.
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /go-docs-mcp .

FROM alpine:3.21
# On Alpine 3.21 the pandoc binary ships in pandoc-cli; there is no package
# named pandoc. tesseract-ocr-data-eng is the English model, the default
# OCR language.
RUN apk add --no-cache poppler-utils tesseract-ocr tesseract-ocr-data-eng pandoc-cli
RUN adduser -D docs && mkdir /documents && chown docs:docs /documents
COPY --from=build /go-docs-mcp /usr/local/bin/
ENV DOCS_MCP_DIR=/documents
VOLUME /documents
USER docs
ENTRYPOINT ["go-docs-mcp"]
