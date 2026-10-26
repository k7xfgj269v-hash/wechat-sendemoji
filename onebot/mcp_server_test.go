package main

// Tests for the MCP Streamable HTTP endpoint (T1).
//
// Everything here goes through the real HTTP surface: the production
// registerMCPRoutes -> automationOperatorAuth -> mcpHandler stack is mounted on
// an http.ServeMux and exercised with httptest, so authentication, method
// gating, JSON-RPC framing and the workbench dispatch all run exactly as they
// would for a DSH mcp-client. Assertions are made against the wire shape
// (JSON field names, HTTP status codes), never against the Go structs, so a
// silently renamed JSON field fails the test.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mcpTestOperatorToken is a distinctive literal: if the endpoint ever echoed the
// credential into a response body the leak assertion would catch it verbatim.
const mcpTestOperatorToken = "mcp-test-operator-token-7f4c1e2a"

// mcpTestLoopback is the only client address the shared operator auth admits.
const mcpTestLoopback = "127.0.0.1:51234"

// mcpTestAcceptDefault mirrors what a Streamable HTTP MCP client advertises.
const mcpTestAcceptDefault = "application/json, text/event-stream"

// ---------------------------------------------------------------------------
// Wire shapes, as a client sees them.
// ---------------------------------------------------------------------------

type mcpTestRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTestRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Result  json.RawMessage  `json:"result"`
	Error   *mcpTestRPCError `json:"error"`
}

type mcpTestInitializeResult struct {
	ProtocolVersion string                     `json:"protocolVersion"`
	Capabilities    map[string]json.RawMessage `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpTestTool struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	InputSchema json.RawMessage            `json:"inputSchema"`
	Annotations map[string]json.RawMessage `json:"annotations"`
}

type mcpTestListToolsResult struct {
	Tools []mcpTestTool `json:"tools"`
}

type mcpTestContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpTestToolCallResult struct {
	Content []mcpTestContent `json:"content"`
	IsError bool             `json:"isError"`
}

type mcpTestWorkbenchPage struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type mcpTestWorkbenchError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type mcpTestWorkbenchResult struct {
	Operation string                 `json:"operation"`
	OK        bool                   `json:"ok"`
	Data      json.RawMessage        `json:"data"`
	Page      *mcpTestWorkbenchPage  `json:"page"`
	Error     *mcpTestWorkbenchError `json:"error"`
}

// ---------------------------------------------------------------------------
// Harness.
// ---------------------------------------------------------------------------

// mcpTestEnv owns one mounted MCP endpoint plus the operator token it expects.
type mcpTestEnv struct {
	token string
	mux   *http.ServeMux
}

// mcpTestHTTPRequest is one raw request. Empty fields fall back to the
// "well-behaved client" defaults; an empty token means "send no Authorization
// header at all", which is how the unauthenticated cases are expressed.
type mcpTestHTTPRequest struct {
	method      string
	body        string
	token       string
	remoteAddr  string
	accept      string
	contentType string
}

func newMCPTestEnv(t *testing.T) *mcpTestEnv {
	t.Helper()
	store, err := OpenCRMStore(t.TempDir() + "/crm.json")
	if err != nil {
		t.Fatalf("open CRM store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mux := http.NewServeMux()
	registerMCPRoutes(mux, httpRouteRegistrationContext{
		mux:        mux,
		store:      store,
		automation: &AutomationRuntime{operatorToken: mcpTestOperatorToken},
	})
	return &mcpTestEnv{token: mcpTestOperatorToken, mux: mux}
}

// request performs one HTTP round trip and asserts the boundary every response
// shares: no credential and no absolute filesystem path in the body.
func (e *mcpTestEnv) request(t *testing.T, request mcpTestHTTPRequest) *httptest.ResponseRecorder {
	t.Helper()
	method := request.method
	if method == "" {
		method = http.MethodPost
	}
	remoteAddr := request.remoteAddr
	if remoteAddr == "" {
		remoteAddr = mcpTestLoopback
	}
	accept := request.accept
	if accept == "" {
		accept = mcpTestAcceptDefault
	}
	contentType := request.contentType
	if contentType == "" {
		contentType = "application/json"
	}

	httpRequest := httptest.NewRequest(method, mcpEndpointPath, strings.NewReader(request.body))
	httpRequest.RemoteAddr = remoteAddr
	if request.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+request.token)
	}
	httpRequest.Header.Set("Accept", accept)
	httpRequest.Header.Set("Content-Type", contentType)

	recorder := httptest.NewRecorder()
	e.mux.ServeHTTP(recorder, httpRequest)
	e.assertNoLeak(t, recorder.Body.String())
	return recorder
}

// assertNoLeak enforces the "no internal detail in responses" rule for every
// single response this file produces.
func (e *mcpTestEnv) assertNoLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"/Users/",
		"/private/",
		"/var/folders/",
		"C:\\Users\\",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response body leaks an absolute path %q: %s", forbidden, body)
		}
	}
	if e.token != "" && strings.Contains(body, e.token) {
		t.Errorf("response body leaks the operator token: %s", body)
	}
}

// call sends one authenticated JSON-RPC request and decodes the response.
func (e *mcpTestEnv) call(t *testing.T, id, method, params string) *mcpTestRPCResponse {
	t.Helper()
	recorder := e.request(t, mcpTestHTTPRequest{
		token: e.token,
		body:  mcpTestRPCBody(t, id, method, params),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body = %s", method, recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("%s Content-Type = %q, want application/json", method, got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("%s Cache-Control = %q, want no-store", method, got)
	}
	response := mcpTestDecodeRPCResponse(t, recorder.Body.String())
	if response.JSONRPC != "2.0" {
		t.Fatalf("%s jsonrpc = %q, want 2.0", method, response.JSONRPC)
	}
	if got := strings.TrimSpace(string(response.ID)); got != strings.TrimSpace(id) {
		t.Fatalf("%s id = %s, want %s", method, got, id)
	}
	return response
}

// callTool drives tools/call and returns the decoded MCP tool envelope. The
// JSON-RPC layer must stay successful for every one of these: tool failures are
// reported inside the result, not as transport errors.
func (e *mcpTestEnv) callTool(t *testing.T, id, toolName, arguments string) mcpTestToolCallResult {
	t.Helper()
	args := strings.TrimSpace(arguments)
	if args == "" {
		args = "{}"
	}
	params := fmt.Sprintf(`{"name":%q,"arguments":%s}`, toolName, args)
	response := e.call(t, id, "tools/call", params)
	if response.Error != nil {
		t.Fatalf("tools/call %s returned a JSON-RPC error: code=%d message=%q",
			toolName, response.Error.Code, response.Error.Message)
	}
	var result mcpTestToolCallResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode tools/call %s result: %v (raw=%s)", toolName, err, response.Result)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tools/call %s returned no content: %s", toolName, response.Result)
	}
	return result
}

func mcpTestRPCBody(t *testing.T, id, method, params string) string {
	t.Helper()
	if strings.TrimSpace(id) == "" {
		return fmt.Sprintf(`{"jsonrpc":"2.0","method":%q}`, method)
	}
	if strings.TrimSpace(params) == "" {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q}`, id, method)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q,"params":%s}`, id, method, params)
}

func mcpTestDecodeRPCResponse(t *testing.T, body string) *mcpTestRPCResponse {
	t.Helper()
	var response mcpTestRPCResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode JSON-RPC response: %v (body=%s)", err, body)
	}
	return &response
}

// mcpTestRequireTextContent asserts the MCP content contract and returns the
// text payload.
func mcpTestRequireTextContent(t *testing.T, result mcpTestToolCallResult) string {
	t.Helper()
	for index, content := range result.Content {
		if content.Type != "text" {
			t.Fatalf("content[%d].type = %q, want text", index, content.Type)
		}
	}
	return result.Content[0].Text
}

func mcpTestDecodeWorkbenchResult(t *testing.T, result mcpTestToolCallResult) mcpTestWorkbenchResult {
	t.Helper()
	text := mcpTestRequireTextContent(t, result)
	var envelope mcpTestWorkbenchResult
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decode workbench envelope: %v (text=%s)", err, text)
	}
	return envelope
}

// mcpTestExpectedTools derives the expected tool surface from the same
// workbench registry the server projects. Nothing is hard-coded, so adding or
// removing an operation cannot silently drift away from this test.
func mcpTestExpectedTools(t *testing.T) map[string]mcpTestExpectedTool {
	t.Helper()
	expected := make(map[string]mcpTestExpectedTool)
	for _, tool := range WorkbenchToolCatalog().Tools {
		for _, operation := range tool.Operations {
			if operation.Exposure != WorkbenchExposureAllowed ||
				operation.Availability != WorkbenchAvailabilityAvailable {
				continue
			}
			name := string(operation.Operation)
			if _, duplicate := expected[name]; duplicate {
				t.Fatalf("workbench catalog lists operation %q twice", name)
			}
			expected[name] = mcpTestExpectedTool{
				operation: operation.Operation,
				readOnly:  tool.Name == AutomationToolWorkbenchQuery,
			}
		}
	}
	if len(expected) == 0 {
		t.Fatal("workbench catalog exposes no operations")
	}
	return expected
}

type mcpTestExpectedTool struct {
	operation WorkbenchOperation
	readOnly  bool
}

// ---------------------------------------------------------------------------
// Handshake.
// ---------------------------------------------------------------------------

func TestMCPInitializeHandshake(t *testing.T) {
	env := newMCPTestEnv(t)

	// The default is the literal from CONTRACT.md, deliberately not the
	// mcpDefaultProtocolVersion constant, so changing the constant cannot make
	// this test silently pass with a different protocol.
	const contractDefaultProtocolVersion = "2026-07-28"

	for _, test := range []struct {
		name     string
		params   string
		expected string
	}{
		{
			name:     "echoes the client protocol version",
			params:   `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"dsh","version":"0.2.1"}}`,
			expected: "2025-06-18",
		},
		{
			name:     "falls back when the client omits protocolVersion",
			params:   `{"capabilities":{"roots":{}},"clientInfo":{"name":"dsh"}}`,
			expected: contractDefaultProtocolVersion,
		},
		{
			name:     "falls back when params are absent entirely",
			params:   "",
			expected: contractDefaultProtocolVersion,
		},
		{
			name:     "falls back when protocolVersion is blank",
			params:   `{"protocolVersion":"   "}`,
			expected: contractDefaultProtocolVersion,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := env.call(t, "1", "initialize", test.params)
			if response.Error != nil {
				t.Fatalf("initialize error: code=%d message=%q", response.Error.Code, response.Error.Message)
			}
			var result mcpTestInitializeResult
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatalf("decode initialize result: %v (raw=%s)", err, response.Result)
			}
			if result.ServerInfo.Name != "onebot" {
				t.Fatalf("serverInfo.name = %q, want onebot", result.ServerInfo.Name)
			}
			if strings.TrimSpace(result.ServerInfo.Version) == "" {
				t.Fatal("serverInfo.version is empty")
			}
			if result.ProtocolVersion != test.expected {
				t.Fatalf("protocolVersion = %q, want %q", result.ProtocolVersion, test.expected)
			}
			toolsCapability, ok := result.Capabilities["tools"]
			if !ok {
				t.Fatalf("capabilities.tools is missing: %s", response.Result)
			}
			// The tools capability is an object (currently empty): a client must
			// be able to read it as one, not as a boolean or a string.
			var toolsObject map[string]json.RawMessage
			if err := json.Unmarshal(toolsCapability, &toolsObject); err != nil {
				t.Fatalf("capabilities.tools = %s, want a JSON object", toolsCapability)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// tools/list
// ---------------------------------------------------------------------------

func TestMCPToolsListMatchesWorkbenchRegistry(t *testing.T) {
	env := newMCPTestEnv(t)

	response := env.call(t, "2", "tools/list", "")
	if response.Error != nil {
		t.Fatalf("tools/list error: code=%d message=%q", response.Error.Code, response.Error.Message)
	}
	var result mcpTestListToolsResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode tools/list result: %v (raw=%s)", err, response.Result)
	}

	expected := mcpTestExpectedTools(t)
	if len(result.Tools) != len(expected) {
		t.Fatalf("tools = %d, want the %d operations the workbench catalog exposes",
			len(result.Tools), len(expected))
	}

	seen := make(map[string]struct{}, len(result.Tools))
	for index, tool := range result.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			t.Fatalf("tools[%d] has an empty name", index)
		}
		if _, duplicate := seen[tool.Name]; duplicate {
			t.Fatalf("tools[%d] duplicate name %q", index, tool.Name)
		}
		seen[tool.Name] = struct{}{}

		want, ok := expected[tool.Name]
		if !ok {
			t.Fatalf("tools[%d] name %q is not an exposed workbench operation", index, tool.Name)
		}
		if index > 0 && result.Tools[index-1].Name > tool.Name {
			t.Fatalf("tools are not sorted: %q before %q", result.Tools[index-1].Name, tool.Name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Fatalf("tool %q has an empty description", tool.Name)
		}

		var schema struct {
			Type       string          `json:"type"`
			Properties json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("tool %q inputSchema is not JSON: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Fatalf("tool %q inputSchema.type = %q, want object", tool.Name, schema.Type)
		}

		readOnlyRaw, hasReadOnly := tool.Annotations["readOnlyHint"]
		if hasReadOnly != want.readOnly {
			t.Fatalf("tool %q readOnlyHint presence = %v, want %v (operation %s)",
				tool.Name, hasReadOnly, want.readOnly, want.operation)
		}
		if hasReadOnly {
			var readOnly bool
			if err := json.Unmarshal(readOnlyRaw, &readOnly); err != nil || !readOnly {
				t.Fatalf("tool %q readOnlyHint = %s, want true", tool.Name, readOnlyRaw)
			}
		}
	}
	for name := range expected {
		if _, ok := seen[name]; !ok {
			t.Fatalf("operation %q is exposed by the workbench catalog but missing from tools/list", name)
		}
	}

	// The read operation this file calls must be advertised, otherwise the
	// tools/call tests below would be testing an unlisted tool.
	if _, ok := seen[string(WorkbenchOperationCatalogList)]; !ok {
		t.Fatalf("tools/list is missing the read operation %q", WorkbenchOperationCatalogList)
	}
}

// ---------------------------------------------------------------------------
// tools/call
// ---------------------------------------------------------------------------

func TestMCPToolsCall(t *testing.T) {
	env := newMCPTestEnv(t)

	t.Run("unknown tool is a tool-level error on HTTP 200", func(t *testing.T) {
		// A JSON-RPC error here would make DSH surface a transport failure; the
		// contract requires the 200 + isError envelope instead.
		result := env.callTool(t, "3", "does.not.exist", `{}`)
		if !result.IsError {
			t.Fatalf("unknown tool isError = false: %+v", result)
		}
		text := mcpTestRequireTextContent(t, result)
		for _, fragment := range []string{
			string(WorkbenchErrorUnsupportedOperation),
			"unknown tool",
			"does.not.exist",
		} {
			if !strings.Contains(text, fragment) {
				t.Fatalf("unknown tool text %q does not mention %q", text, fragment)
			}
		}
		var envelope mcpTestWorkbenchResult
		if err := json.Unmarshal([]byte(text), &envelope); err != nil {
			t.Fatalf("decode unknown-tool envelope: %v (text=%s)", err, text)
		}
		if envelope.OK {
			t.Fatalf("unknown tool envelope reports ok=true: %s", text)
		}
		if envelope.Error == nil || envelope.Error.Code != string(WorkbenchErrorUnsupportedOperation) {
			t.Fatalf("unknown tool envelope error = %+v", envelope.Error)
		}
	})

	t.Run("read operation catalog.list succeeds as text content", func(t *testing.T) {
		result := env.callTool(t, "4", string(WorkbenchOperationCatalogList), `{"limit":3,"offset":0}`)
		if result.IsError {
			t.Fatalf("catalog.list isError = true: %s", mcpTestRequireTextContent(t, result))
		}
		if len(result.Content) != 1 {
			t.Fatalf("catalog.list content = %d blocks, want 1", len(result.Content))
		}
		envelope := mcpTestDecodeWorkbenchResult(t, result)
		if !envelope.OK {
			t.Fatalf("catalog.list ok = false: %+v", envelope.Error)
		}
		if envelope.Operation != string(WorkbenchOperationCatalogList) {
			t.Fatalf("catalog.list operation = %q", envelope.Operation)
		}
		if len(envelope.Data) == 0 {
			t.Fatal("catalog.list returned no data")
		}
		if envelope.Page == nil {
			t.Fatalf("catalog.list returned no page: %+v", envelope)
		}
		if envelope.Page.Limit != 3 || envelope.Page.Offset != 0 {
			t.Fatalf("catalog.list page = %+v, want limit 3 offset 0", envelope.Page)
		}
	})

	t.Run("allow_once tool fails closed without a permission decision", func(t *testing.T) {
		// messages.send_private lives on workbench.allow_once. The MCP
		// execution context carries no permission decision, so the call must be
		// refused inside the normal permission check -- never silently executed.
		arguments := `{"event_id":"event_00000001","intent":"general","text":"hello"}`
		result := env.callTool(t, "5", string(WorkbenchOperationMessagesSendPrivate), arguments)
		if !result.IsError {
			t.Fatalf("allow_once tool isError = false: %s", mcpTestRequireTextContent(t, result))
		}
		text := mcpTestRequireTextContent(t, result)
		if !strings.Contains(text, string(WorkbenchErrorAllowOnceRequired)) {
			t.Fatalf("allow_once text %q does not mention %q", text, WorkbenchErrorAllowOnceRequired)
		}
		var envelope mcpTestWorkbenchResult
		if err := json.Unmarshal([]byte(text), &envelope); err != nil {
			t.Fatalf("decode allow_once envelope: %v (text=%s)", err, text)
		}
		if envelope.Operation != string(WorkbenchOperationMessagesSendPrivate) {
			t.Fatalf("allow_once envelope operation = %q, want %q",
				envelope.Operation, WorkbenchOperationMessagesSendPrivate)
		}
		if envelope.OK || envelope.Error == nil {
			t.Fatalf("allow_once envelope = %+v", envelope)
		}
		if envelope.Error.Code != string(WorkbenchErrorAllowOnceRequired) {
			t.Fatalf("allow_once error code = %q, want %q",
				envelope.Error.Code, WorkbenchErrorAllowOnceRequired)
		}
	})

	t.Run("arguments that fail the operation schema stay inside the envelope", func(t *testing.T) {
		result := env.callTool(t, "6", string(WorkbenchOperationCatalogList), `{"limit":"three"}`)
		if !result.IsError {
			t.Fatalf("bad arguments isError = false: %s", mcpTestRequireTextContent(t, result))
		}
		envelope := mcpTestDecodeWorkbenchResult(t, result)
		if envelope.Error == nil || envelope.Error.Code != string(WorkbenchErrorInvalidArguments) {
			t.Fatalf("bad arguments error = %+v", envelope.Error)
		}
	})

	t.Run("ping is answered", func(t *testing.T) {
		response := env.call(t, `"ping-id"`, "ping", "")
		if response.Error != nil {
			t.Fatalf("ping error: code=%d message=%q", response.Error.Code, response.Error.Message)
		}
		if len(response.Result) == 0 {
			t.Fatal("ping returned an empty result")
		}
	})

	t.Run("notifications are acknowledged without a body", func(t *testing.T) {
		recorder := env.request(t, mcpTestHTTPRequest{
			token: env.token,
			body:  `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		})
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("notification status = %d, want 204 (body=%s)", recorder.Code, recorder.Body.String())
		}
		if strings.TrimSpace(recorder.Body.String()) != "" {
			t.Fatalf("notification body = %q, want empty", recorder.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Protocol and authentication boundaries.
// ---------------------------------------------------------------------------

func TestMCPProtocolAndAuthBoundaries(t *testing.T) {
	env := newMCPTestEnv(t)

	type boundaryCase struct {
		request      mcpTestHTTPRequest
		wantStatus   int
		wantRPCError int // 0 means the body must carry a result, not an error
		wantContains []string
	}

	authenticated := func(body string) mcpTestHTTPRequest {
		return mcpTestHTTPRequest{token: env.token, body: body}
	}

	for _, test := range []struct {
		name  string
		cased boundaryCase
	}{
		{
			name: "missing bearer token is unauthorized",
			cased: boundaryCase{
				request:    mcpTestHTTPRequest{body: mcpTestRPCBody(t, "1", "ping", "")},
				wantStatus: http.StatusUnauthorized,
			},
		},
		{
			name: "wrong bearer token is unauthorized",
			cased: boundaryCase{
				request:    mcpTestHTTPRequest{token: "not-the-operator-token", body: mcpTestRPCBody(t, "1", "ping", "")},
				wantStatus: http.StatusUnauthorized,
			},
		},
		{
			name: "non loopback caller is forbidden",
			cased: boundaryCase{
				request: mcpTestHTTPRequest{
					token: env.token, body: mcpTestRPCBody(t, "1", "ping", ""), remoteAddr: "10.1.2.3:40000",
				},
				wantStatus: http.StatusForbidden,
			},
		},
		{
			name: "GET is rejected with an explicit unsupported message",
			cased: boundaryCase{
				request:      mcpTestHTTPRequest{token: env.token, method: http.MethodGet},
				wantStatus:   http.StatusMethodNotAllowed,
				wantContains: []string{"unsupported", http.MethodPost},
			},
		},
		{
			name: "PUT is rejected",
			cased: boundaryCase{
				request:      mcpTestHTTPRequest{token: env.token, method: http.MethodPut, body: `{}`},
				wantStatus:   http.StatusMethodNotAllowed,
				wantContains: []string{"unsupported"},
			},
		},
		{
			name: "an unacceptable Accept header is refused",
			cased: boundaryCase{
				request:      mcpTestHTTPRequest{token: env.token, body: mcpTestRPCBody(t, "1", "ping", ""), accept: "text/plain"},
				wantStatus:   http.StatusNotAcceptable,
				wantContains: []string{"application/json"},
			},
		},
		{
			name: "empty body is a parse error",
			cased: boundaryCase{
				request:      authenticated(""),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeParse,
			},
		},
		{
			name: "non object body is a parse error",
			cased: boundaryCase{
				request:      authenticated(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeParse,
			},
		},
		{
			name: "truncated JSON is a parse error",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":1,"method":`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeParse,
			},
		},
		{
			name: "oversized body is a parse error",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"padding":"` + strings.Repeat("x", mcpMaxRequestBodyBytes) + `"}}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeParse,
			},
		},
		{
			name: "unknown method is method not found",
			cased: boundaryCase{
				request:      authenticated(mcpTestRPCBody(t, "7", "resources/list", "")),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeMethodNotFound,
				wantContains: []string{"resources/list"},
			},
		},
		{
			name: "unsupported jsonrpc version is an invalid request",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"1.0","id":8,"method":"ping"}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidRequest,
			},
		},
		{
			name: "missing method is an invalid request",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":9}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidRequest,
			},
		},
		{
			name: "array params are invalid params",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":[]}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidParams,
			},
		},
		{
			name: "initialize with array params is invalid params",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":11,"method":"initialize","params":[]}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidParams,
			},
		},
		{
			name: "non object tool arguments are invalid params",
			cased: boundaryCase{
				request: authenticated(`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":` +
					fmt.Sprintf("%q", WorkbenchOperationCatalogList) + `,"arguments":[1,2]}}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidParams,
			},
		},
		{
			name: "missing tool name is invalid params",
			cased: boundaryCase{
				request:      authenticated(`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"arguments":{}}}`),
				wantStatus:   http.StatusOK,
				wantRPCError: mcpErrorCodeInvalidParams,
			},
		},
		{
			name: "a notification carrying an id is answered",
			cased: boundaryCase{
				request:    authenticated(`{"jsonrpc":"2.0","id":14,"method":"notifications/initialized"}`),
				wantStatus: http.StatusOK,
			},
		},
		{
			name: "a string id is echoed verbatim",
			cased: boundaryCase{
				request:    authenticated(mcpTestRPCBody(t, `"client-abc"`, "ping", "")),
				wantStatus: http.StatusOK,
			},
		},
		{
			name: "a numeric zero id still gets a response",
			cased: boundaryCase{
				// A zero id is a request, not a notification: mcpRequestHasID
				// must not treat it as absent.
				request:    authenticated(mcpTestRPCBody(t, "0", "ping", "")),
				wantStatus: http.StatusOK,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := env.request(t, test.cased.request)
			if recorder.Code != test.cased.wantStatus {
				t.Fatalf("status = %d, want %d (body = %s)",
					recorder.Code, test.cased.wantStatus, recorder.Body.String())
			}
			body := recorder.Body.String()
			for _, fragment := range test.cased.wantContains {
				if !strings.Contains(body, fragment) {
					t.Fatalf("body %q does not contain %q", body, fragment)
				}
			}
			if test.cased.wantStatus != http.StatusOK {
				// Transport-level failures must not masquerade as JSON-RPC.
				if strings.Contains(body, `"jsonrpc"`) {
					t.Fatalf("HTTP %d response carries a JSON-RPC envelope: %s", recorder.Code, body)
				}
				return
			}
			if strings.TrimSpace(body) == "" {
				t.Fatalf("empty body for a request that must be answered")
			}
			response := mcpTestDecodeRPCResponse(t, body)
			if response.JSONRPC != "2.0" {
				t.Fatalf("jsonrpc = %q, want 2.0", response.JSONRPC)
			}
			if test.cased.wantRPCError == 0 {
				if response.Error != nil {
					t.Fatalf("unexpected JSON-RPC error: code=%d message=%q",
						response.Error.Code, response.Error.Message)
				}
				return
			}
			if response.Error == nil {
				t.Fatalf("want JSON-RPC error %d, got result %s", test.cased.wantRPCError, response.Result)
			}
			if response.Error.Code != test.cased.wantRPCError {
				t.Fatalf("error code = %d, want %d (message=%q)",
					response.Error.Code, test.cased.wantRPCError, response.Error.Message)
			}
			if strings.TrimSpace(response.Error.Message) == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

// TestMCPHandlerWithoutDispatcher covers the transport-level fault paths that
// the mounted route cannot reach: the endpoint must still answer with a
// JSON-RPC internal error instead of panicking or leaking details.
func TestMCPHandlerWithoutDispatcher(t *testing.T) {
	t.Run("tools/call without a dispatcher is an internal error", func(t *testing.T) {
		handler := mcpHandler(mcpDeps{})
		request := httptest.NewRequest(
			http.MethodPost,
			mcpEndpointPath,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":`+
				fmt.Sprintf("%q", WorkbenchOperationCatalogList)+`,"arguments":{}}}`),
		)
		request.RemoteAddr = mcpTestLoopback
		request.Header.Set("Accept", mcpTestAcceptDefault)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", recorder.Code, recorder.Body.String())
		}
		response := mcpTestDecodeRPCResponse(t, recorder.Body.String())
		if response.Error == nil {
			t.Fatalf("want a JSON-RPC internal error, got result %s", response.Result)
		}
		if response.Error.Code != mcpErrorCodeInternal {
			t.Fatalf("error code = %d, want %d", response.Error.Code, mcpErrorCodeInternal)
		}
		if strings.Contains(recorder.Body.String(), "/") {
			t.Fatalf("internal error leaks a path: %s", recorder.Body.String())
		}
	})

	t.Run("registering without an automation runtime mounts nothing", func(t *testing.T) {
		mux := http.NewServeMux()
		registerMCPRoutes(mux, httpRouteRegistrationContext{mux: mux})

		request := httptest.NewRequest(http.MethodPost, mcpEndpointPath, strings.NewReader(`{}`))
		request.RemoteAddr = mcpTestLoopback
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 when no runtime is registered", recorder.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// Concurrency.
// ---------------------------------------------------------------------------

func TestMCPConcurrentToolCallsStayIsolated(t *testing.T) {
	env := newMCPTestEnv(t)

	const callers = 8
	type callResult struct {
		limit  int
		status int
		body   string
	}
	results := make([]callResult, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func(index int) {
			defer waitGroup.Done()
			limit := index + 1
			request := httptest.NewRequest(
				http.MethodPost,
				mcpEndpointPath,
				strings.NewReader(fmt.Sprintf(
					`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{"limit":%d,"offset":0}}}`,
					limit, string(WorkbenchOperationCatalogList), limit,
				)),
			)
			request.RemoteAddr = mcpTestLoopback
			request.Header.Set("Authorization", "Bearer "+env.token)
			request.Header.Set("Accept", mcpTestAcceptDefault)
			recorder := httptest.NewRecorder()
			env.mux.ServeHTTP(recorder, request)
			results[index] = callResult{
				limit:  limit,
				status: recorder.Code,
				body:   recorder.Body.String(),
			}
		}(index)
	}
	waitGroup.Wait()

	for index, result := range results {
		if result.status != http.StatusOK {
			t.Fatalf("caller %d status = %d, body = %s", index, result.status, result.body)
		}
		env.assertNoLeak(t, result.body)
		var response mcpTestRPCResponse
		if err := json.Unmarshal([]byte(result.body), &response); err != nil {
			t.Fatalf("caller %d decode: %v (body=%s)", index, err, result.body)
		}
		if response.Error != nil {
			t.Fatalf("caller %d JSON-RPC error: %+v", index, response.Error)
		}
		var call mcpTestToolCallResult
		if err := json.Unmarshal(response.Result, &call); err != nil {
			t.Fatalf("caller %d decode tool result: %v", index, err)
		}
		if call.IsError {
			t.Fatalf("caller %d isError = true: %+v", index, call)
		}
		if len(call.Content) != 1 || call.Content[0].Type != "text" {
			t.Fatalf("caller %d content = %+v", index, call.Content)
		}
		var envelope mcpTestWorkbenchResult
		if err := json.Unmarshal([]byte(call.Content[0].Text), &envelope); err != nil {
			t.Fatalf("caller %d decode envelope: %v", index, err)
		}
		if envelope.Page == nil {
			t.Fatalf("caller %d has no page: %s", index, call.Content[0].Text)
		}
		// Interleaved calls must not cross-contaminate: every caller sees the
		// page size it asked for.
		if envelope.Page.Limit != result.limit {
			t.Fatalf("caller %d page.limit = %d, want %d (cross-talk between concurrent calls)",
				index, envelope.Page.Limit, result.limit)
		}
	}
}

// ---------------------------------------------------------------------------
// Response-text hygiene helpers (unit level).
// ---------------------------------------------------------------------------

func TestMCPPublicErrorTextIsPathAndControlFree(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		contains string
		absent   []string
	}{
		{
			name:     "redacts a user home path",
			input:    "open <home>/secret/crm.json failed",
			contains: "<redacted-path>",
			absent:   []string{"/Users/"},
		},
		{
			name:     "redacts a private tmp path",
			input:    "wrote /private/tmp/onebot.json",
			contains: "<redacted-path>",
			absent:   []string{"/private/tmp/"},
		},
		{
			name:     "redacts a var path",
			input:    "unreadable /var/folders/xy/store.json",
			contains: "<redacted-path>",
			absent:   []string{"/var/folders/"},
		},
		{
			name:     "flattens newlines and tabs",
			input:    "bad\nline\tvalue",
			contains: "bad line value",
			absent:   []string{"\n", "\t"},
		},
		{
			name:     "falls back when the message is blank",
			input:    "   ",
			contains: "workbench operation failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := mcpPublicErrorText(test.input)
			if test.contains != "" && !strings.Contains(got, test.contains) {
				t.Fatalf("mcpPublicErrorText(%q) = %q, want it to contain %q",
					test.input, got, test.contains)
			}
			for _, absent := range test.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("mcpPublicErrorText(%q) = %q, still contains %q",
						test.input, got, absent)
				}
			}
		})
	}

	t.Run("bounds the message length", func(t *testing.T) {
		got := mcpPublicErrorText(strings.Repeat("a", 5000))
		if runes := []rune(got); len(runes) > 400 {
			t.Fatalf("mcpPublicErrorText length = %d runes, want at most 400", len(runes))
		}
	})
}

func TestMCPAcceptsJSONResponse(t *testing.T) {
	for _, test := range []struct {
		accept string
		want   bool
	}{
		{accept: "", want: true},
		{accept: "application/json", want: true},
		{accept: "application/json; charset=utf-8", want: true},
		{accept: "APPLICATION/JSON", want: true},
		{accept: "text/event-stream", want: true},
		{accept: "application/json, text/event-stream", want: true},
		{accept: "*/*", want: true},
		{accept: "application/*", want: true},
		{accept: "text/plain", want: false},
		{accept: "text/html, application/xhtml+xml", want: false},
	} {
		if got := mcpAcceptsJSONResponse(test.accept); got != test.want {
			t.Errorf("mcpAcceptsJSONResponse(%q) = %v, want %v", test.accept, got, test.want)
		}
	}
}
