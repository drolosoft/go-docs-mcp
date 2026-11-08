# Security

## Reporting a problem

If you find a way to read a file outside `DOCS_MCP_DIR`, to make the server
reach a network address it should not, or to hang it, please report it
privately through GitHub's "Report a vulnerability" button on this
repository (Security tab), or by email to the address on the
[drolosoft](https://github.com/drolosoft) organisation page. Please do not
open a public issue for it.

You will get an answer within a week. Fixes ship as a patch release with a
note in the CHANGELOG; credit goes to the reporter unless they prefer not.

## What the server promises

- It reads only files whose resolved path is inside `DOCS_MCP_DIR`. Symlinks
  that point outside are not listed and cannot be read.
- It never writes inside that directory. Downloads from `read_url` go to a
  temporary file that is removed right after extraction.
- Every external process runs under a deadline and is killed when it passes.
- `read_url` refuses private, loopback, link-local and cloud metadata
  addresses, follows at most the client's redirect limit with the same check
  on every hop, and can be removed with `DOCS_MCP_DISABLE_URL=1`.

## What it does not cover

- The contents of your documents. A document can contain text meant to steer
  the model that reads it; the server returns the text as is.
- The external tools it runs (poppler, tesseract, pandoc). Keep them updated
  from your package manager.
- Access control between clients. Anyone who can start the binary with a
  given `DOCS_MCP_DIR` can read everything in it.

## Supported versions

Only the latest release receives security fixes.
