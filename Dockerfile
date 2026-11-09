# The build stage runs on the builder's own platform and cross-compiles, which
# is safe because the binary is pure Go (CGO_ENABLED=0). Only the final stage
# runs under QEMU in a multi-arch build, and it just installs packages.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release passes VERSION without the leading "v" (1.5.0). The build stage
# has no git, so without it the build info says "(devel)" and the binary
# reports "dev". -buildvcs=false keeps a git worktree (where .git is a file
# that points outside the context) from breaking the build.
ARG VERSION=
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.linkedVersion=${VERSION}" -o /go-docs-mcp .

FROM alpine:3.21
ARG VERSION=
# The official MCP Registry proves that the image belongs to the server by
# reading this label from the image config. Its value must equal "name" in
# server.json.
LABEL io.modelcontextprotocol.server.name="io.github.drolosoft/go-docs-mcp"
# The release workflow sets these too, from the tag and the repository. They
# are here so a local build carries the same metadata.
LABEL org.opencontainers.image.title="go-docs-mcp" \
      org.opencontainers.image.description="Lets your AI read PDF, Office files, EPUB, HTML, Jupyter and images, with OCR, search and paging." \
      org.opencontainers.image.source="https://github.com/drolosoft/go-docs-mcp" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION:-dev}"
# On Alpine 3.21 the pandoc binary ships in pandoc-cli; there is no package
# named pandoc. The image carries the English and Spanish OCR models; English
# stays the default, DOCS_MCP_OCR_LANG=spa (or eng+spa) changes it.
RUN apk add --no-cache poppler-utils tesseract-ocr \
    tesseract-ocr-data-eng tesseract-ocr-data-spa pandoc-cli
RUN adduser -D docs && mkdir /documents && chown docs:docs /documents
COPY --from=build /go-docs-mcp /usr/local/bin/
ENV DOCS_MCP_DIR=/documents
VOLUME /documents
USER docs
ENTRYPOINT ["go-docs-mcp"]
