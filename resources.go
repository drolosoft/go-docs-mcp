package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// documentURIPrefix starts the URI of every document resource. What follows
// is the file name, percent-encoded, so docs://my%20report.pdf is the file
// "my report.pdf" in the documents directory.
const documentURIPrefix = "docs://"

// documentURITemplate is the resource template a client fills in to read any
// document by name, including one added after the last resources/list.
const documentURITemplate = documentURIPrefix + "{filename}"

// registerDocumentResources offers every document as a resource, so a client
// such as Claude Code can let the user @-mention it. resources/list is
// rebuilt from the directory before each answer through hooks, because
// mcp-go only lists what was registered and the directory changes while the
// server runs; resources/read of a docs:// URI returns the same text as
// read_document without pages.
func registerDocumentResources(s *server.MCPServer, hooks *server.Hooks, reader *pdf.Reader, docsDir string) {
	readHandler := documentResourceHandler(reader, docsDir)

	template := mcp.NewResourceTemplate(documentURITemplate, "Document",
		mcp.WithTemplateTitle("Document"),
		mcp.WithTemplateDescription("The text of a document in the documents directory, by file name: verbatim for TXT, MD and CSV, extracted for PDF and DOCX."),
	)
	s.AddResourceTemplate(template, server.ResourceTemplateHandlerFunc(readHandler))

	hooks.AddBeforeListResources(func(ctx context.Context, _ any, _ *mcp.ListResourcesRequest) {
		s.SetResources(documentResources(ctx, reader, readHandler)...)
	})
}

// documentResources returns one resource per document reader lists. When the
// directory cannot be read the list is empty, which is what a client can
// still open; the error itself reaches the model through list_documents.
func documentResources(ctx context.Context, reader *pdf.Reader, readHandler server.ResourceHandlerFunc) []server.ServerResource {
	documents, err := reader.ListDocuments(ctx)
	if err != nil {
		return nil
	}

	resources := make([]server.ServerResource, 0, len(documents))
	for _, document := range documents {
		resource := mcp.NewResource(documentURI(document.Filename), document.Filename,
			mcp.WithResourceTitle(document.Title),
			mcp.WithMIMEType(documentMIMEType(document.Filename)),
		)

		resources = append(resources, server.ServerResource{Resource: resource, Handler: readHandler})
	}

	return resources
}

// documentResourceHandler reads the document a docs:// URI names. Its errors
// go back to the client as JSON-RPC errors, which the tool middleware does
// not see, so disk paths are taken out of them here.
func documentResourceHandler(reader *pdf.Reader, docsDir string) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := request.Params.URI

		filename, err := documentFilename(uri)
		if err != nil {
			return nil, err
		}

		text, err := reader.ReadDocument(ctx, filename, 0)
		if err != nil {
			return nil, errors.New(redactPaths(err.Error(), docsDir, os.TempDir()))
		}

		contents := mcp.TextResourceContents{
			URI:      uri,
			MIMEType: documentMIMEType(filename),
			Text:     text,
		}

		return []mcp.ResourceContents{contents}, nil
	}
}

// documentFilename turns a docs:// URI back into the file name it encodes. A
// name with a directory in it is refused here, before the reader, because
// no listed resource has one and the reader would quietly keep only its last
// element.
func documentFilename(uri string) (string, error) {
	encoded, found := strings.CutPrefix(uri, documentURIPrefix)
	if !found || encoded == "" {
		return "", fmt.Errorf("not a document URI: %q", uri)
	}

	filename, err := url.PathUnescape(encoded)
	if err != nil {
		return "", fmt.Errorf("malformed document URI %q: %w", uri, err)
	}

	if filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\`) {
		return "", fmt.Errorf("a document URI names one file in the documents directory, not a path: %q", uri)
	}

	return filename, nil
}

// documentURI returns the docs:// URI of filename. Everything but the RFC
// 3986 unreserved characters is percent-encoded, which is what the
// {filename} template matches; url.PathEscape would leave characters such as
// ":" and "@" that the template rejects.
func documentURI(filename string) string {
	var encoded strings.Builder
	encoded.WriteString(documentURIPrefix)

	for index := 0; index < len(filename); index++ {
		character := filename[index]

		if isUnreserved(character) {
			encoded.WriteByte(character)
			continue
		}

		fmt.Fprintf(&encoded, "%%%02X", character)
	}

	return encoded.String()
}

// isUnreserved reports whether character may appear in a URI as it is.
func isUnreserved(character byte) bool {
	switch {
	case 'a' <= character && character <= 'z':
		return true
	case 'A' <= character && character <= 'Z':
		return true
	case '0' <= character && character <= '9':
		return true
	}

	return strings.IndexByte("-._~", character) >= 0
}

// documentMIMEType is the type of the text a document resource returns: the
// file itself for Markdown and CSV, and plain text for everything else,
// since PDF and DOCX come back as their extracted text, not as the file.
func documentMIMEType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	default:
		return "text/plain"
	}
}
