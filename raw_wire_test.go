package main

// Raw-wire tests: the real binary, over stdio, with no MCP library on the
// client side.
//
// This file is the arbiter of any future mcp-go bump: it must pass UNCHANGED
// or the bump does not merge. contract_test.go drives the server with
// mcp-go's own client, so a library change that moved both ends at once would
// keep it green. Here every request is a JSON line written by hand and every
// answer is decoded from the bytes on stdout, so what is checked is the
// protocol itself. Only the standard library is imported, on purpose.
//
// Legacy era: the client opens with initialize. Modern era (2026-07-28):
// there is no handshake, and every request carries the protocol version and
// the client capabilities in params._meta.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// modernProtocolVersion is the revision that removed the initialize
// handshake.
const modernProtocolVersion = "2026-07-28"

// latestLegacyProtocolVersion is the newest revision that still opens with
// initialize. The server falls back to it for any offer it cannot echo.
const latestLegacyProtocolVersion = "2025-11-25"

// The modern era moves the handshake into every request: these are the _meta
// keys that replace initialize, and the one that carries the server identity
// back on every result.
const (
	// protocolVersionKey names the revision the request speaks.
	protocolVersionKey = "io.modelcontextprotocol/protocolVersion"

	// clientCapabilitiesKey carries what initialize used to declare once.
	clientCapabilitiesKey = "io.modelcontextprotocol/clientCapabilities"

	// serverInfoKey carries the serverInfo that initialize used to return.
	serverInfoKey = "io.modelcontextprotocol/serverInfo"
)

// Error codes the modern era answers a broken envelope with.
const (
	// invalidParamsCode is JSON-RPC's own code for malformed params, used
	// when the envelope lacks a required key.
	invalidParamsCode = -32602

	// unsupportedVersionCode is the 2026-07-28 code for a revision the
	// server does not speak.
	unsupportedVersionCode = -32022
)

// rawReadTimeout bounds every wait for a message, so a server that hangs
// fails the test instead of blocking it. It is generous because one page of
// OCR on a loaded machine has been measured above six seconds.
const rawReadTimeout = 60 * time.Second

// rawExitTimeout is how long cleanup waits for the server to exit after its
// stdin closes before killing it.
const rawExitTimeout = 10 * time.Second

// maxRawLineBytes is the longest line the reader accepts. tools/list sends
// every schema on one line and tool results may reach 9 MB, so the scanner's
// 64 KB default is far too small.
const maxRawLineBytes = 16 << 20

// expectedSupportedVersions is what server/discover and the unsupported
// version error must list. The order is part of the answer: newest first,
// so a client takes the first one it also speaks.
var expectedSupportedVersions = []string{
	"2026-07-28",
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// legacyProtocolVersions are the revisions that open with initialize. The
// server must echo each one back unchanged, or a client pinned to it hangs up.
var legacyProtocolVersions = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
}

// rawFrame is one JSON-RPC message read from the server's stdout. A response
// has an id and a result or an error; a notification has a method and no id.
type rawFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rawError       `json:"error"`

	// raw is the line as it came, for failure messages and traces.
	raw []byte
}

// rawError is the error member of a JSON-RPC response.
type rawError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// rawSession is one server process driven over its stdio pipes. Requests are
// numbered from 1, and the notifications read while waiting for a response
// are kept for the tests that check them.
type rawSession struct {
	t             *testing.T
	stdin         io.WriteCloser
	lines         <-chan []byte
	nextID        int
	notifications []rawFrame
}

// startRawServer spawns the binary serving dir and returns a session on its
// pipes. Cleanup closes stdin, which is how a stdio client hangs up, and
// waits for the process, killing it if it does not exit.
func startRawServer(t *testing.T, dir string) *rawSession {
	t.Helper()

	command := exec.Command(binPath)
	command.Env = append(os.Environ(), "DOCS_MCP_DIR="+dir)

	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", binPath, err)
	}

	// Lines are read on their own goroutine so that every wait can have a
	// deadline. Closing stopped lets that goroutine give up once the test is
	// over instead of blocking on a channel nobody reads any more.
	lines := make(chan []byte, 64)
	stopped := make(chan struct{})
	go readLines(stdout, lines, stopped)

	t.Cleanup(func() {
		close(stopped)
		_ = stdin.Close()
		waitOrKill(t, command)
	})

	return &rawSession{t: t, stdin: stdin, lines: lines, nextID: 1}
}

// readLines sends every line of stdout to lines until stdout ends or stopped
// closes, and then closes lines so a waiting reader learns the server is gone.
func readLines(stdout io.Reader, lines chan<- []byte, stopped <-chan struct{}) {
	defer close(lines)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRawLineBytes)

	for scanner.Scan() {
		// The scanner reuses its buffer, so each line is copied before it
		// leaves this goroutine.
		line := slices.Clone(scanner.Bytes())

		select {
		case lines <- line:
		case <-stopped:
			return
		}
	}
}

// waitOrKill waits for command to exit and kills it after rawExitTimeout.
// A server that outlives its client is logged rather than failed: exiting on
// end of input is good manners, but not what this file pins.
func waitOrKill(t *testing.T, command *exec.Cmd) {
	t.Helper()

	exited := make(chan error, 1)
	go func() {
		exited <- command.Wait()
	}()

	select {
	case <-exited:
	case <-time.After(rawExitTimeout):
		t.Logf("server still running %s after stdin closed, killing it", rawExitTimeout)
		_ = command.Process.Kill()
		<-exited
	}
}

// send writes message as one JSON line, the framing of the stdio transport.
func (session *rawSession) send(message map[string]any) {
	session.t.Helper()

	line, err := json.Marshal(message)
	if err != nil {
		session.t.Fatalf("marshal %v: %v", message, err)
	}

	line = append(line, '\n')
	if _, err := session.stdin.Write(line); err != nil {
		session.t.Fatalf("write to the server: %v", err)
	}
}

// readFrame returns the next message from the server. It fails the test when
// none arrives within rawReadTimeout, when the server closes stdout, or when
// a line is not JSON.
func (session *rawSession) readFrame() rawFrame {
	session.t.Helper()

	timer := time.NewTimer(rawReadTimeout)
	defer timer.Stop()

	select {
	case line, open := <-session.lines:
		if !open {
			session.t.Fatal("the server closed stdout")
		}

		frame := rawFrame{raw: line}
		if err := json.Unmarshal(line, &frame); err != nil {
			session.t.Fatalf("stdout line is not JSON-RPC: %v\n%s", err, line)
		}

		return frame
	case <-timer.C:
		session.t.Fatalf("no message from the server within %s", rawReadTimeout)
	}

	return rawFrame{}
}

// notify sends a notification, which carries no id and gets no answer.
func (session *rawSession) notify(method string) {
	session.t.Helper()

	session.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// request sends method with params and returns the response that carries
// its id. Notifications that arrive in between are kept, since the server may
// interleave them with any answer.
func (session *rawSession) request(method string, params map[string]any) rawFrame {
	session.t.Helper()

	id := session.nextID
	session.nextID++

	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	session.send(message)

	wantID := strconv.Itoa(id)
	for {
		frame := session.readFrame()

		// Requests here go one at a time, so a message with an id can only
		// be the answer to this one; anything else is a protocol fault.
		if frame.ID == nil && frame.Method != "" {
			session.notifications = append(session.notifications, frame)
			continue
		}
		if string(frame.ID) != wantID {
			session.t.Fatalf("%s: got a message with id %s, want %s:\n%s", method, frame.ID, wantID, frame.raw)
		}

		return frame
	}
}

// result is request for calls that must succeed: it fails the test on a
// JSON-RPC error and returns the result member.
func (session *rawSession) result(method string, params map[string]any) json.RawMessage {
	session.t.Helper()

	frame := session.request(method, params)
	if frame.Error != nil || frame.Result == nil {
		session.t.Fatalf("%s did not succeed:\n%s", method, frame.raw)
	}

	return frame.Result
}

// failure is request for calls that must be refused: it fails the test on a
// result and returns the error member.
func (session *rawSession) failure(method string, params map[string]any) rawError {
	session.t.Helper()

	frame := session.request(method, params)
	if frame.Error == nil {
		session.t.Fatalf("%s should have failed:\n%s", method, frame.raw)
	}

	return *frame.Error
}

// awaitNotifications returns the kept notifications named method, reading on
// until there are at least count of them. Progress for a call can trail its
// response, because the stdio transport writes notifications from their own
// goroutine; measured on v1.5.0 in both eras.
func (session *rawSession) awaitNotifications(method string, count int) []rawFrame {
	session.t.Helper()

	for {
		var matching []rawFrame
		for _, frame := range session.notifications {
			if frame.Method == method {
				matching = append(matching, frame)
			}
		}
		if len(matching) >= count {
			return matching
		}

		frame := session.readFrame()
		if frame.ID != nil {
			session.t.Fatalf("got a response while waiting for %s:\n%s", method, frame.raw)
		}
		session.notifications = append(session.notifications, frame)
	}
}

// initializeLegacy opens a legacy session offering version and returns the
// version the server chose. It then sends notifications/initialized, as a
// client must before its first real request.
func initializeLegacy(session *rawSession, offered string) string {
	session.t.Helper()

	result := session.result("initialize", map[string]any{
		"protocolVersion": offered,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "raw-wire-test", "version": "1.0"},
	})

	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	decodeRaw(session.t, result, &initialized)

	session.notify("notifications/initialized")

	return initialized.ProtocolVersion
}

// modernEnvelope returns the two _meta keys a 2026-07-28 request carries in
// place of initialize. Each call builds a new map, so a caller can add keys
// such as a progress token without touching another request.
func modernEnvelope() map[string]any {
	return map[string]any{
		protocolVersionKey:    modernProtocolVersion,
		clientCapabilitiesKey: map[string]any{},
	}
}

// withMeta returns params with meta as its _meta member. A nil params stands
// for a request that has nothing but the envelope.
func withMeta(params map[string]any, meta map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = meta

	return params
}

// decodeRaw unmarshals data into target and fails the test with the raw
// bytes when they do not have the expected shape.
func decodeRaw(t *testing.T, data json.RawMessage, target any) {
	t.Helper()

	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %T: %v\n%s", target, err, data)
	}
}

// wireTools decodes a tools/list result into its tools, each kept as its raw
// members so the definitions can be compared field by field.
func wireTools(t *testing.T, result json.RawMessage) []map[string]json.RawMessage {
	t.Helper()

	var listing struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	decodeRaw(t, result, &listing)

	return listing.Tools
}

// wireToolNames returns the sorted names in a tools/list result.
func wireToolNames(t *testing.T, result json.RawMessage) []string {
	t.Helper()

	names := []string{}
	for _, tool := range wireTools(t, result) {
		var name string
		decodeRaw(t, tool["name"], &name)
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// wireText returns the first text block of a tools/call result and fails the
// test when the call is a tool error or has no text.
func wireText(t *testing.T, result json.RawMessage) string {
	t.Helper()

	var call struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	decodeRaw(t, result, &call)

	if call.IsError {
		t.Fatalf("tools/call returned a tool error:\n%s", result)
	}
	for _, content := range call.Content {
		if content.Type == "text" {
			return content.Text
		}
	}

	t.Fatalf("no text content in tools/call result:\n%s", result)
	return ""
}

// checkServerInfo asserts that a modern-era result names the server in its
// _meta. Without initialize, that is the only place a client learns who
// answered.
func checkServerInfo(t *testing.T, method string, result json.RawMessage) {
	t.Helper()

	var envelope struct {
		Meta map[string]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"_meta"`
	}
	decodeRaw(t, result, &envelope)

	info, found := envelope.Meta[serverInfoKey]
	if !found {
		t.Fatalf("%s result has no _meta[%q]:\n%s", method, serverInfoKey, result)
	}
	if info.Name != "go-docs-mcp" || info.Version != expectedServerVersion {
		t.Errorf("%s serverInfo = %+v, want go-docs-mcp %s", method, info, expectedServerVersion)
	}
}

// listDocumentsNamesFixtures calls list_documents and fails the test unless
// its text names both fixtures, the one real call every era must complete.
func listDocumentsNamesFixtures(t *testing.T, session *rawSession, meta map[string]any) json.RawMessage {
	t.Helper()

	params := map[string]any{"name": "list_documents", "arguments": map[string]any{}}
	if meta != nil {
		params = withMeta(params, meta)
	}

	result := session.result("tools/call", params)
	listing := wireText(t, result)
	if !strings.Contains(listing, "prueba.md") || !strings.Contains(listing, "notas.txt") {
		t.Fatalf("list_documents did not name both fixtures: %s", listing)
	}

	return result
}

// canonicalJSON re-encodes raw with sorted keys and exact numbers, so two
// definitions that differ only in key order or spacing compare equal. An
// absent member comes back empty, so absent on one side only is a difference.
func canonicalJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	if raw == nil {
		return ""
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %v: %v", value, err)
	}

	return string(canonical)
}

// TestRawWireLegacyHandshake opens with initialize on every revision that
// has it: the server must echo the offer, list the exact tool surface and
// complete a real call.
func TestRawWireLegacyHandshake(t *testing.T) {
	for _, version := range legacyProtocolVersions {
		t.Run(version, func(t *testing.T) {
			session := startRawServer(t, docsDir)

			if chosen := initializeLegacy(session, version); chosen != version {
				t.Fatalf("initialize offering %s chose %q, want the offer echoed", version, chosen)
			}

			names := wireToolNames(t, session.result("tools/list", nil))
			if !slices.Equal(names, expectedToolNames) {
				t.Fatalf("tools/list names =\n  %v\nwant\n  %v", names, expectedToolNames)
			}

			listDocumentsNamesFixtures(t, session, nil)
		})
	}
}

// TestRawWireInitializeFallsBackToLatestLegacy offers revisions the
// handshake cannot echo. The server must answer with the newest legacy
// revision, which the client can accept or hang up on.
func TestRawWireInitializeFallsBackToLatestLegacy(t *testing.T) {
	tests := []struct {
		name    string
		offered string
	}{
		// A revision from the future that nobody speaks yet.
		{name: "unknown revision", offered: "2099-01-01"},
		// The modern revision has no initialize, so offering it through
		// initialize can only be answered in the legacy era.
		{name: "modern revision through initialize", offered: modernProtocolVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := startRawServer(t, docsDir)

			if chosen := initializeLegacy(session, test.offered); chosen != latestLegacyProtocolVersion {
				t.Fatalf("initialize offering %s chose %q, want %q", test.offered, chosen, latestLegacyProtocolVersion)
			}
		})
	}
}

// TestRawWireDiscover asks the modern era what it speaks. supportedVersions
// is compared in order, because a client takes the first entry it knows.
func TestRawWireDiscover(t *testing.T) {
	session := startRawServer(t, docsDir)

	result := session.result("server/discover", withMeta(nil, modernEnvelope()))

	var discovery struct {
		ResultType        string   `json:"resultType"`
		SupportedVersions []string `json:"supportedVersions"`
	}
	decodeRaw(t, result, &discovery)

	if discovery.ResultType != "complete" {
		t.Errorf("server/discover resultType = %q, want %q", discovery.ResultType, "complete")
	}
	if !slices.Equal(discovery.SupportedVersions, expectedSupportedVersions) {
		t.Errorf("server/discover supportedVersions =\n  %v\nwant\n  %v", discovery.SupportedVersions, expectedSupportedVersions)
	}

	checkServerInfo(t, "server/discover", result)
}

// TestRawWireModernWithoutHandshake sends real work with the envelope and no
// initialize at all: tools, a call and the document resources must all be
// served, each result naming the server.
func TestRawWireModernWithoutHandshake(t *testing.T) {
	session := startRawServer(t, docsDir)

	tools := session.result("tools/list", withMeta(nil, modernEnvelope()))
	if names := wireToolNames(t, tools); !slices.Equal(names, expectedToolNames) {
		t.Fatalf("tools/list names =\n  %v\nwant\n  %v", names, expectedToolNames)
	}
	checkServerInfo(t, "tools/list", tools)

	call := listDocumentsNamesFixtures(t, session, modernEnvelope())
	checkServerInfo(t, "tools/call", call)

	resources := session.result("resources/list", withMeta(nil, modernEnvelope()))

	var listing struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	decodeRaw(t, resources, &listing)

	uris := []string{}
	for _, resource := range listing.Resources {
		uris = append(uris, resource.URI)
	}
	slices.Sort(uris)
	if !slices.Equal(uris, expectedResourceURIs) {
		t.Fatalf("resources/list uris =\n  %v\nwant\n  %v", uris, expectedResourceURIs)
	}
	checkServerInfo(t, "resources/list", resources)

	read := session.result("resources/read", withMeta(map[string]any{"uri": "docs://prueba.md"}, modernEnvelope()))

	var document struct {
		Contents []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	decodeRaw(t, read, &document)

	if len(document.Contents) != 1 {
		t.Fatalf("resources/read returned %d contents, want 1:\n%s", len(document.Contents), read)
	}
	content := document.Contents[0]
	if content.MIMEType != "text/markdown" || !strings.Contains(content.Text, "Dual-era contract fixture") {
		t.Fatalf("resources/read docs://prueba.md = %+v", content)
	}
	checkServerInfo(t, "resources/read", read)
}

// TestRawWireModernMissingClientCapabilities sends the version without the
// capabilities. The envelope is incomplete, so the request is refused as
// invalid params, naming the missing key so the client author can fix it.
func TestRawWireModernMissingClientCapabilities(t *testing.T) {
	session := startRawServer(t, docsDir)

	refusal := session.failure("tools/list", withMeta(nil, map[string]any{
		protocolVersionKey: modernProtocolVersion,
	}))

	if refusal.Code != invalidParamsCode {
		t.Errorf("error code = %d, want %d", refusal.Code, invalidParamsCode)
	}
	if !strings.Contains(refusal.Message, clientCapabilitiesKey) {
		t.Errorf("error message %q does not name %s", refusal.Message, clientCapabilitiesKey)
	}
}

// TestRawWireModernUnknownVersion sends a revision the server does not
// speak. The refusal must list what it does speak, so the client can retry
// with one of them instead of guessing.
func TestRawWireModernUnknownVersion(t *testing.T) {
	session := startRawServer(t, docsDir)

	envelope := modernEnvelope()
	envelope[protocolVersionKey] = "2027-01-01"
	refusal := session.failure("tools/list", withMeta(nil, envelope))

	if refusal.Code != unsupportedVersionCode {
		t.Errorf("error code = %d, want %d", refusal.Code, unsupportedVersionCode)
	}

	var data struct {
		Supported []string `json:"supported"`
	}
	decodeRaw(t, refusal.Data, &data)

	if !slices.Equal(data.Supported, expectedSupportedVersions) {
		t.Errorf("error data.supported =\n  %v\nwant\n  %v", data.Supported, expectedSupportedVersions)
	}
}

// TestRawWireToolDefinitionsMatchAcrossEras lists the tools once per era, in
// two processes so neither era can borrow state from the other. A model must
// see the same tool whichever era its client speaks, so every member that
// shapes a call has to match byte for byte once canonicalised.
func TestRawWireToolDefinitionsMatchAcrossEras(t *testing.T) {
	legacySession := startRawServer(t, docsDir)
	initializeLegacy(legacySession, latestLegacyProtocolVersion)
	legacyTools := wireTools(t, legacySession.result("tools/list", nil))

	modernSession := startRawServer(t, docsDir)
	modernTools := wireTools(t, modernSession.result("tools/list", withMeta(nil, modernEnvelope())))

	modernByName := map[string]map[string]json.RawMessage{}
	for _, tool := range modernTools {
		var name string
		decodeRaw(t, tool["name"], &name)
		modernByName[name] = tool
	}

	if len(legacyTools) != len(expectedToolNames) || len(modernTools) != len(expectedToolNames) {
		t.Fatalf("legacy listed %d tools and modern %d, want %d each", len(legacyTools), len(modernTools), len(expectedToolNames))
	}

	for _, legacyTool := range legacyTools {
		var name string
		decodeRaw(t, legacyTool["name"], &name)

		modernTool, found := modernByName[name]
		if !found {
			t.Errorf("%s is listed in the legacy era only", name)
			continue
		}

		// Every tool has a description and an input schema; were they
		// missing on both sides, the comparison below would pass on nothing.
		if legacyTool["description"] == nil || legacyTool["inputSchema"] == nil {
			t.Errorf("%s has no description or inputSchema in the legacy era", name)
		}

		for _, member := range []string{"description", "inputSchema", "outputSchema", "annotations"} {
			legacyValue := canonicalJSON(t, legacyTool[member])
			modernValue := canonicalJSON(t, modernTool[member])
			if legacyValue != modernValue {
				t.Errorf("%s %s differs between eras:\nlegacy %s\nmodern %s", name, member, legacyValue, modernValue)
			}
		}
	}
}

// rawProgress is the part of a notifications/progress this file checks.
type rawProgress struct {
	ProgressToken any     `json:"progressToken"`
	Progress      float64 `json:"progress"`
	Total         float64 `json:"total"`
	Message       string  `json:"message"`
}

// TestRawWireOCRProgressInBothEras runs ocr_document with a progress token
// in each era and expects the two notifications the tool sends: the page
// count before the work and "OCR done" after it. Only their order between
// themselves is checked; the last one may land after the response.
func TestRawWireOCRProgressInBothEras(t *testing.T) {
	// The server also looks in Homebrew's directories, but PATH is what a
	// standard-library test can check without importing the reader: this can
	// skip on a machine that has the tools, never run on one that lacks them.
	for _, tool := range []string{"tesseract", "pdftoppm", "pdfinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("OCR needs tesseract and poppler: %s is not in PATH", tool)
		}
	}

	tests := []struct {
		name   string
		legacy bool
		meta   map[string]any
	}{
		{name: "legacy", legacy: true, meta: map[string]any{"progressToken": "ocr-legacy"}},
		{name: "modern", legacy: false, meta: withProgressToken(modernEnvelope(), "ocr-modern")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "scan.pdf"), onePagePDF("Scan", "HELLO WORLD"), 0o644); err != nil {
				t.Fatalf("write scan.pdf: %v", err)
			}

			session := startRawServer(t, dir)
			if test.legacy {
				initializeLegacy(session, latestLegacyProtocolVersion)
			}

			params := withMeta(map[string]any{
				"name":      "ocr_document",
				"arguments": map[string]any{"filename": "scan.pdf"},
			}, test.meta)
			result := session.result("tools/call", params)
			if text := wireText(t, result); !strings.Contains(text, "HELLO") {
				t.Fatalf("ocr_document did not read the page: %s", text)
			}

			token := test.meta["progressToken"]
			want := []rawProgress{
				{ProgressToken: token, Progress: 0, Total: 1, Message: "starting OCR of 1 page"},
				{ProgressToken: token, Progress: 1, Total: 1, Message: "OCR done"},
			}

			got := []rawProgress{}
			for _, frame := range session.awaitNotifications("notifications/progress", len(want)) {
				var update rawProgress
				decodeRaw(t, frame.Params, &update)
				got = append(got, update)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("progress =\n  %+v\nwant\n  %+v", got, want)
			}
		})
	}
}

// withProgressToken adds a progress token to meta and returns it, so a table
// row can build its envelope in one expression.
func withProgressToken(meta map[string]any, token string) map[string]any {
	meta["progressToken"] = token

	return meta
}
