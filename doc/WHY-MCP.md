# 🤔 Why an MCP Server?

Why build a standalone MCP server instead of embedding document reading directly into your application?

---

## 🌍 Use It Everywhere

The MCP server is a standalone binary. Any MCP-compatible client can connect to it — Claude Code, Claude Desktop, Cursor, Windsurf, LM Studio, or any future tool that speaks the protocol.

**Build once, use everywhere.**

A direct tool is locked to one application. An MCP server is portable infrastructure.

---

## 🤝 Reusable Across Agents

Multiple AI agents can share the same document server simultaneously. No duplication of code, no duplication of configuration, no per-project reimplementation.

One server serves your entire AI workflow.

---

## 🎁 Open Source Value

A direct tool embedded in your app helps only you. An MCP server is a generic Go binary that anyone can install:

```bash
go install github.com/drolosoft/go-docs-mcp@latest
```

The community benefits. Contributors appear. The tool improves faster than anything you'd maintain alone.

---

## 🧰 12 Tools for Free

The MCP server encapsulates complexity behind clean tool interfaces:

- Read full text or page ranges
- Full-text search with context
- Document summaries
- Metadata extraction
- OCR for scanned documents
- Image extraction (base64)
- URL fetching
- Table extraction

All of this is available to any client with **zero integration work**. A direct tool would need to reimplement every capability per project.

---

## ⚡ Caching and Performance

Extracted text is cached by file modification time. Multiple calls to the same document are instant — no re-extraction, no redundant subprocess calls.

Each client gets this for free. No per-client caching logic required.

---

## 🔒 Security Boundary

The server only serves files from a single configured directory. Path traversal is blocked at the binary level. Symlink escapes are rejected.

Every client gets the same security guarantee without implementing their own filesystem sandboxing.

---

## ⚖️ The Tradeoff

| Approach | Latency | Scope |
|----------|---------|-------|
| Direct function call | ~1ms | One application |
| MCP subprocess call | ~100ms | Every MCP client |

For AI conversations, that 100ms is negligible — the LLM response takes 2-3 seconds regardless. You won't notice it.

---

## 📌 Summary

| | Direct Tool | MCP Server |
|---|---|---|
| Speed | Faster (~1ms) | Fast enough (~100ms) |
| Portability | One app only | Any MCP client |
| Reusability | Copy-paste per project | Shared binary |
| Open source value | None | Full ecosystem play |
| Security | DIY per integration | Built-in, universal |
| Caching | DIY per integration | Built-in, universal |
| Maintenance | Per project | Single source of truth |

**Direct tool** = faster for one app.
**MCP server** = ecosystem play that scales across everything you build.

---

[Back to README](../README.md)
