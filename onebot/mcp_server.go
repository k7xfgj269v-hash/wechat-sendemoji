package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MCP Streamable HTTP server for the local workbench.
//
// The endpoint is POST-only JSON-RPC 2.0 over a single request/response body.
// There is no SSE stream, no session state, and no third-party MCP SDK: the
// transported payload is plain encoding/json, and every tool call re-enters the
// existing workbench dispatcher so permissions, risk policy and audit paths are
// unchanged.

const (
	// mcpEndpointPath is the single Streamable HTTP endpoint.
	mcpEndpointPath = "/mcp"

	mcpServerName = "onebot"

	// mcpDefaultProtocolVersion is answered when the client omits a version.
	mcpDefaultProtocolVersion = "2026-07-28"

	// mcpMaxRequestBodyBytes bounds a JSON-RPC request body. Workbench arguments
	// are capped far lower by WorkbenchMaxArgumentsJSONBytes; this only stops a
	// hostile client from streaming an unbounded body.
	mcpMaxRequestBodyBytes = 1 << 20

	// mcpOperatorID is recorded in the execution context so audit entries and
	// domain actors attribute MCP-originated workbench calls. It is a fixed
	// literal: no credential, session value or filesystem detail is ever placed
	// in it.
	mcpOperatorID = "mcp"
)

// JSON-RPC 2.0 error codes used by this server.
const (
	mcpErrorCodeParse          = -32700
	mcpErrorCodeInvalidRequest = -32600
	mcpErrorCodeMethodNotFound = -32601
	mcpErrorCodeInvalidParams  = -32602
	mcpErrorCodeInternal       = -32603
)

// mcpServerVersion is reported as initialize.serverInfo.version. This binary
// carries no separate build stamp, so the value is the workbench tool catalog
// generation it implements.
const mcpServerVersion = WorkbenchToolCatalogVersion

// errMCPInvalidCatalog means the internal operation catalog and the MCP tool
// projection disagree. It is an internal fault, never a client error.
var errMCPInvalidCatalog = errors.New("workbench tool catalog is unavailable")

// mcpInvalidParamsError marks a client-side params fault, mapping to -32602.
type mcpInvalidParamsError struct {
	message string
}

func (e mcpInvalidParamsError) Error() string { return e.message }

func newMCPInvalidParamsError(message string) error {
	return mcpInvalidParamsError{message: message}
}

// mcpDeps are the collaborators handed to the HTTP handler.
type mcpDeps struct {
	dispatcher WorkbenchToolDispatcher
}

// mcpServer answers decoded JSON-RPC requests for the MCP tool surface.
type mcpServer struct {
	dispatcher WorkbenchToolDispatcher
}

// mcpRPCRequest is one JSON-RPC 2.0 request or notification.
type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// mcpRPCError is a JSON-RPC 2.0 error object.
type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpRPCResponse is one JSON-RPC 2.0 response. Exactly one of Result or Error is
// populated; ID is echoed from the request verbatim.
type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

// registerMCPRoutes mounts the MCP endpoint behind the existing operator
// authentication (loopback plus Bearer operator token).
func registerMCPRoutes(mux *http.ServeMux, ctx httpRouteRegistrationContext) {
	if mux == nil || ctx.automation == nil {
		return
	}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          ctx.store,
		Agent:        ctx.agent,
		Automation:   ctx.automation.store,
		Audit:        ctx.automation.audit,
		ForwardRules: ctx.rules,
	})
	mux.HandleFunc(
		mcpEndpointPath,
		ctx.automationOperatorAuth(mcpHandler(mcpDeps{dispatcher: dispatcher})),
	)
}

// automationOperatorAuth applies the shared operator authentication to one
// handler using this registration context's operator token. It is a thin alias
// so the MCP route is authenticated exactly like the other operator routes.
func (c httpRouteRegistrationContext) automationOperatorAuth(next http.HandlerFunc) http.HandlerFunc {
	return automationOperatorAuth(c.operatorToken(), next)
}

// mcpHandler serves POST /mcp.
func mcpHandler(deps mcpDeps) http.HandlerFunc {
	server := &mcpServer{dispatcher: deps.dispatcher}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeMCPHTTPError(
				w,
				http.StatusMethodNotAllowed,
				"method_not_allowed",
				"mcp endpoint accepts POST only; "+r.Method+" is unsupported",
			)
			return
		}
		if !mcpAcceptsJSONResponse(r.Header.Get("Accept")) {
			writeMCPHTTPError(
				w,
				http.StatusNotAcceptable,
				"not_acceptable",
				"mcp endpoint replies with application/json only",
			)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxRequestBodyBytes))
		if err != nil {
			writeMCPRPCResponse(
				w,
				mcpRPCErrorResponse(nil, mcpErrorCodeParse, "request body could not be read"),
			)
			return
		}
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			writeMCPRPCResponse(
				w,
				mcpRPCErrorResponse(nil, mcpErrorCodeParse, "request body must be one JSON-RPC object"),
			)
			return
		}
		var request mcpRPCRequest
		if err := json.Unmarshal(trimmed, &request); err != nil {
			writeMCPRPCResponse(
				w,
				mcpRPCErrorResponse(nil, mcpErrorCodeParse, "request body is not valid JSON"),
			)
			return
		}
		response, respond := server.handleRPC(r.Context(), request)
		if !respond {
			// A JSON-RPC notification is acknowledged without a body.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeMCPRPCResponse(w, response)
	}
}

// handleRPC dispatches one request. The second return value is false when the
// input was a notification and therefore must not be answered.
func (s *mcpServer) handleRPC(
	ctx context.Context,
	request mcpRPCRequest,
) (mcpRPCResponse, bool) {
	if !mcpRequestHasID(request.ID) {
		s.handleNotification(request.Method)
		return mcpRPCResponse{}, false
	}
	id := append(json.RawMessage(nil), request.ID...)
	if version := strings.TrimSpace(request.JSONRPC); version != "" && version != "2.0" {
		return mcpRPCErrorResponse(id, mcpErrorCodeInvalidRequest, "jsonrpc must be \"2.0\""), true
	}
	method := strings.TrimSpace(request.Method)
	if method == "" {
		return mcpRPCErrorResponse(id, mcpErrorCodeInvalidRequest, "method is required"), true
	}
	if strings.HasPrefix(method, "notifications/") {
		s.handleNotification(method)
		return mcpRPCResultResponse(id, struct{}{})
	}
	switch method {
	case "initialize":
		result, err := s.initialize(request.Params)
		if err != nil {
			return mcpRPCErrorFrom(id, err), true
		}
		return mcpRPCResultResponse(id, result)
	case "ping":
		return mcpRPCResultResponse(id, struct{}{})
	case "tools/list":
		tools, err := s.listTools()
		if err != nil {
			return mcpRPCErrorFrom(id, err), true
		}
		return mcpRPCResultResponse(id, mcpListToolsResult{Tools: tools})
	case "tools/call":
		params, err := decodeMCPParams[mcpCallToolParams](request.Params)
		if err != nil {
			return mcpRPCErrorFrom(id, err), true
		}
		result, err := s.callTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return mcpRPCErrorFrom(id, err), true
		}
		return mcpRPCResultResponse(id, result)
	default:
		return mcpRPCErrorResponse(
			id,
			mcpErrorCodeMethodNotFound,
			"method not found: "+mcpPublicErrorText(method),
		), true
	}
}

// initialize answers the MCP handshake, echoing the client protocol version and
// advertising tools support only.
func (s *mcpServer) initialize(params json.RawMessage) (mcpInitializeResult, error) {
	request, err := decodeMCPParams[mcpInitializeParams](params)
	if err != nil {
		return mcpInitializeResult{}, err
	}
	protocolVersion := strings.TrimSpace(request.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = mcpDefaultProtocolVersion
	}
	return mcpInitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    mcpServerCapabilities{Tools: &mcpToolsCapability{}},
		ServerInfo:      mcpImplementation{Name: mcpServerName, Version: mcpServerVersion},
	}, nil
}

// listTools projects the internal operation catalog onto MCP tool entries.
func (s *mcpServer) listTools() ([]mcpTool, error) {
	operations := mcpExposedOperations()
	if len(operations) == 0 {
		return nil, errMCPInvalidCatalog
	}
	tools := make([]mcpTool, 0, len(operations))
	for _, operation := range operations {
		tool, err := mcpToolFromOperation(operation)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// callTool re-enters the existing workbench dispatcher. Tool-level failures are
// returned inside the MCP result envelope with isError set; only transport-level
// faults become JSON-RPC errors.
func (s *mcpServer) callTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (mcpCallResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return mcpCallResult{}, newMCPInvalidParamsError("tool name is required")
	}
	operation, ok := mcpExposedOperationFor(name)
	if !ok {
		return mcpToolFailureResult(
			"",
			WorkbenchErrorUnsupportedOperation,
			"unknown tool: "+name,
		), nil
	}
	arguments, err := mcpToolArguments(args)
	if err != nil {
		return mcpCallResult{}, err
	}
	if s == nil || s.dispatcher == nil {
		return mcpCallResult{}, errors.New("workbench dispatcher is unavailable")
	}
	request := WorkbenchToolRequest{
		Operation:     WorkbenchOperation(operation.name),
		ArgumentsJSON: string(arguments),
	}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return mcpCallResult{}, errors.New("workbench request could not be encoded")
	}
	decoded, err := DecodeWorkbenchToolRequest(operation.tool, rawRequest)
	if err != nil {
		return mcpToolFailureResult(
			WorkbenchOperation(operation.name),
			WorkbenchErrorInvalidArguments,
			err.Error(),
		), nil
	}
	result := s.dispatcher.Dispatch(ctx, mcpWorkbenchExecutionContext(), decoded)
	if err := ValidateWorkbenchToolResult(result); err != nil {
		return mcpToolFailureResult(
			WorkbenchOperation(operation.name),
			WorkbenchErrorInternal,
			"workbench result was rejected",
		), nil
	}
	return mcpToolResultFromWorkbench(result), nil
}

// handleNotification swallows JSON-RPC notifications. The Streamable HTTP
// transport expects no response body for them, and this server owns no state
// that a notification could change, so notifications/initialized and any other
// notification are simply acknowledged.
func (s *mcpServer) handleNotification(method string) {
	_ = method
}

// mcpWorkbenchExecutionContext builds the execution context for one MCP call.
//
// No permission decision is supplied: operations that require a fresh
// allow_once approval therefore fail closed inside the normal permission check,
// exactly as they do for any other caller without an approval.
func mcpWorkbenchExecutionContext() WorkbenchExecutionContext {
	return WorkbenchExecutionContext{OperatorID: mcpOperatorID}
}

// mcpToolArguments normalises tools/call arguments to a JSON object.
func mcpToolArguments(args json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage("{}"), nil
	}
	if trimmed[0] != '{' {
		return nil, newMCPInvalidParamsError("tool arguments must be a JSON object")
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

// decodeMCPParams decodes an optional JSON object params payload.
func decodeMCPParams[T any](params json.RawMessage) (T, error) {
	var value T
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return value, nil
	}
	if trimmed[0] != '{' {
		return value, newMCPInvalidParamsError("params must be a JSON object")
	}
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return value, newMCPInvalidParamsError("params are invalid")
	}
	return value, nil
}

// mcpRequestHasID reports whether the payload expects a response. A payload
// without an id, or with a null id, is a notification.
func mcpRequestHasID(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
}

// mcpAcceptsJSONResponse reports whether the client can consume a JSON reply.
// An absent or empty Accept header is accepted; text/event-stream is tolerated
// because Streamable HTTP clients advertise it, even though this server never
// opens a stream.
func mcpAcceptsJSONResponse(accept string) bool {
	accept = strings.ToLower(strings.TrimSpace(accept))
	if accept == "" {
		return true
	}
	for _, candidate := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(candidate, ";", 2)[0])
		switch mediaType {
		case "application/json", "text/event-stream", "application/*", "*/*":
			return true
		}
	}
	return false
}

// mcpRPCResultResponse encodes one successful response.
func mcpRPCResultResponse(id json.RawMessage, result any) (mcpRPCResponse, bool) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return mcpRPCErrorResponse(id, mcpErrorCodeInternal, "response could not be encoded"), true
	}
	return mcpRPCResponse{
		JSONRPC: "2.0",
		ID:      mcpResponseID(id),
		Result:  encoded,
	}, true
}

// mcpRPCErrorResponse encodes one error response.
func mcpRPCErrorResponse(id json.RawMessage, code int, message string) mcpRPCResponse {
	return mcpRPCResponse{
		JSONRPC: "2.0",
		ID:      mcpResponseID(id),
		Error:   &mcpRPCError{Code: code, Message: mcpPublicErrorText(message)},
	}
}

// mcpRPCErrorFrom maps a handler error onto a JSON-RPC error response.
func mcpRPCErrorFrom(id json.RawMessage, err error) mcpRPCResponse {
	var invalid mcpInvalidParamsError
	switch {
	case errors.As(err, &invalid):
		return mcpRPCErrorResponse(id, mcpErrorCodeInvalidParams, invalid.Error())
	case errors.Is(err, errMCPInvalidCatalog):
		return mcpRPCErrorResponse(id, mcpErrorCodeInternal, "workbench tool catalog is unavailable")
	default:
		return mcpRPCErrorResponse(id, mcpErrorCodeInternal, "internal error")
	}
}

// mcpResponseID always emits an id member, using null when the request had none.
func mcpResponseID(id json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), trimmed...)
}

// writeMCPRPCResponse writes one JSON-RPC response body.
func writeMCPRPCResponse(w http.ResponseWriter, response mcpRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// writeMCPHTTPError writes a transport-level error. Messages are fixed literals
// so no token, filesystem path or internal detail can leak through them.
func writeMCPHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
