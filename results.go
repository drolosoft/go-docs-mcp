package main

import (
	"fmt"

	"github.com/drolosoft/go-docs-mcp/internal/pdf"
	"github.com/mark3labs/mcp-go/mcp"
)

// textPagingOptions adds the offset and max_chars arguments, and the output
// schema of a text page, to a tool that returns the text of a document.
func textPagingOptions() mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithInteger("offset",
			mcp.Description("Character offset to start reading from (default 0). A truncated result ends with the offset of the next page."),
			mcp.Min(0),
			mcp.DefaultNumber(0),
		)(tool)
		mcp.WithInteger("max_chars",
			mcp.Description(fmt.Sprintf("Maximum characters to return (default %d, at most %d).", pdf.DefaultMaxChars, pdf.MaxCharsLimit)),
			mcp.Min(1),
			mcp.Max(pdf.MaxCharsLimit),
			mcp.DefaultNumber(pdf.DefaultMaxChars),
		)(tool)
		mcp.WithOutputSchema[pdf.TextPage]()(tool)
	}
}

// pagedTextResult returns the page of text that the request's offset and
// max_chars select. The text content carries the page and, when the document
// goes on, the trailer that names the next offset; the structured content
// carries the same page with next_offset and total_chars as numbers.
func pagedTextResult(request mcp.CallToolRequest, text string) *mcp.CallToolResult {
	offset := request.GetInt("offset", 0)
	maxChars := request.GetInt("max_chars", pdf.DefaultMaxChars)

	page, err := pdf.PageText(text, offset, maxChars)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error paging text: %v", err))
	}

	return mcp.NewToolResultStructured(page, page.TextWithTrailer())
}
