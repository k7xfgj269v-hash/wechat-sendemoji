package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// MCP / JSON-RPC wire types for the Streamable HTTP endpoint.
//
// Only the subset this server actually answers is modelled: tools/list and
// tools/call plus the handshake. Everything is encoded with encoding/json; no
// third-party MCP SDK is involved.

// mcpTool is one entry of a tools/list result.
type mcpTool struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	InputSchema json.RawMessage     `json:"inputSchema"`
	Annotations *mcpToolAnnotations `json:"annotations,omitempty"`
}

// mcpToolAnnotations mirrors the optional MCP tool annotation object.
type mcpToolAnnotations struct {
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
}

// mcpListToolsResult is the tools/list result payload.
type mcpListToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

// mcpContent is one content block of a tools/call result. This server only ever
// emits text blocks: the workbench result envelope, JSON-encoded.
type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// mcpCallResult is the tools/call result payload.
type mcpCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

// mcpImplementation is the serverInfo object of an initialize result.
type mcpImplementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// mcpToolsCapability advertises tools support; it marshals as {}.
type mcpToolsCapability struct{}

// mcpServerCapabilities is the capabilities object of an initialize result.
type mcpServerCapabilities struct {
	Tools *mcpToolsCapability `json:"tools"`
}

// mcpInitializeResult is the initialize result payload.
type mcpInitializeResult struct {
	ProtocolVersion string                `json:"protocolVersion"`
	Capabilities    mcpServerCapabilities `json:"capabilities"`
	ServerInfo      mcpImplementation     `json:"serverInfo"`
}

// mcpInitializeParams is the subset of initialize params this server reads.
type mcpInitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      json.RawMessage `json:"clientInfo"`
}

// mcpCallToolParams is the tools/call params payload.
type mcpCallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// mcpToolFromOperation projects one exposed workbench operation onto an MCP
// tool advertisement, reusing the catalog schema verbatim.
func mcpToolFromOperation(operation mcpExposedOperation) (mcpTool, error) {
	if strings.TrimSpace(operation.name) == "" {
		return mcpTool{}, errMCPInvalidCatalog
	}
	if len(operation.schema) == 0 {
		return mcpTool{}, errMCPInvalidCatalog
	}
	// The advertised schema must stay a JSON object schema; anything else means
	// the catalog drifted from what the decoder accepts.
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(operation.schema, &envelope); err != nil {
		return mcpTool{}, errMCPInvalidCatalog
	}
	if envelope.Type != "object" {
		return mcpTool{}, errMCPInvalidCatalog
	}
	tool := mcpTool{
		Name:        operation.name,
		Description: operation.mcpDescription(),
		InputSchema: append(json.RawMessage(nil), operation.schema...),
	}
	if operation.mcpReadOnly() {
		readOnly := true
		tool.Annotations = &mcpToolAnnotations{ReadOnlyHint: &readOnly}
	}
	return tool, nil
}

// mcpToolResultFromWorkbench projects a workbench tool result onto the MCP
// result envelope. Failures stay at the tool level (isError) so the client sees
// the workbench error code instead of losing it in a transport error.
func mcpToolResultFromWorkbench(result WorkbenchToolResult) mcpCallResult {
	payload, err := json.Marshal(result)
	if err != nil {
		return mcpToolFailureResult(result.Operation, WorkbenchErrorInternal, "workbench result could not be encoded")
	}
	return mcpCallResult{
		Content: []mcpContent{{Type: "text", Text: string(payload)}},
		IsError: !result.OK,
	}
}

// mcpToolFailureResult builds an isError result in the workbench envelope.
func mcpToolFailureResult(
	operation WorkbenchOperation,
	code WorkbenchErrorCode,
	message string,
) mcpCallResult {
	payload, err := json.Marshal(NewWorkbenchToolFailure(
		operation,
		code,
		mcpPublicErrorText(message),
		false,
	))
	if err != nil {
		return mcpCallResult{
			Content: []mcpContent{{Type: "text", Text: `{"ok":false}`}},
			IsError: true,
		}
	}
	return mcpCallResult{
		Content: []mcpContent{{Type: "text", Text: string(payload)}},
		IsError: true,
	}
}

// mcpFilesystemPathPattern matches absolute paths that must never reach a
// client. Workbench decoders already emit field-scoped messages, so this only
// fires on text that a handler appended by accident.
var mcpFilesystemPathPattern = regexp.MustCompile(
	`(?:[A-Za-z]:\\|/(?:Users|private|var|tmp|etc|home|opt|usr|root)/)[^\s"'<>]*`,
)

// mcpPublicErrorText keeps response text free of control characters, redacts
// filesystem paths, and bounds its length. Workbench decoders already emit
// field-scoped, path-free messages; this is a defensive backstop before the text
// reaches a client.
func mcpPublicErrorText(message string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(message))
	cleaned = mcpFilesystemPathPattern.ReplaceAllString(cleaned, "<redacted-path>")
	if cleaned == "" {
		return "workbench operation failed"
	}
	const maxRunes = 400
	runes := []rune(cleaned)
	if len(runes) > maxRunes {
		cleaned = string(runes[:maxRunes])
	}
	return cleaned
}
