package main

// mcpb/manifest.json describes the Claude Desktop bundle (.mcpb) that each
// release ships. Its "tools" array is written by mcpb/gen from this server's
// own tools/list: run go generate after adding a tool or changing a
// description. TestMCPBManifestMatchesToolsList fails until they agree.
//
//go:generate go run ./mcpb/gen
