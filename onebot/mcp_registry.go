package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// This file is the single bridge between the internal workbench operation
// catalog and the MCP tool catalog.
//
// Exposure is derived from workbenchOperationSpecs, which is the exact same
// registry the dispatcher enforces at call time (including runtime permission
// overrides). Nothing here restates an operation name, a tool name, or an
// argument schema by hand: the tool names are operation ids, the descriptions
// come from the catalog label/description, and the input schema is the schema
// the workbench decoder already validates against.

// mcpExposedOperation is one workbench operation surfaced as an MCP tool.
type mcpExposedOperation struct {
	tool        AutomationToolName
	name        string
	label       string
	description string
	category    string
	schema      json.RawMessage
}

// mcpExposedOperations lists every operation an MCP client may attempt.
//
// A definition is exposed when the catalog marks it as allowed exposure and the
// registry currently reports it available. Permanently forbidden operations
// (never bound to a handler) and operations denied by the runtime override set
// are therefore absent. Operations that still require a fresh allow_once
// approval stay listed, because the catalog exposes them; calling one fails
// closed through the normal permission check.
func mcpExposedOperations() []mcpExposedOperation {
	operations := make([]mcpExposedOperation, 0, workbenchOperationDefinitionCount)
	for _, tool := range WorkbenchProviderTools() {
		for _, spec := range workbenchOperationSpecs(tool) {
			if spec.catalog.Exposure != WorkbenchExposureAllowed {
				continue
			}
			if spec.catalog.Availability != WorkbenchAvailabilityAvailable {
				continue
			}
			operations = append(operations, mcpExposedOperation{
				tool:        tool,
				name:        string(spec.catalog.Operation),
				label:       spec.catalog.Label,
				description: spec.catalog.Description,
				category:    spec.catalog.Category,
				schema:      append(json.RawMessage(nil), spec.catalog.ArgumentsSchema...),
			})
		}
	}
	sort.SliceStable(operations, func(i, j int) bool {
		return operations[i].name < operations[j].name
	})
	return operations
}

// mcpExposedOperationFor resolves one exposed MCP tool name.
func mcpExposedOperationFor(name string) (mcpExposedOperation, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return mcpExposedOperation{}, false
	}
	for _, operation := range mcpExposedOperations() {
		if operation.name == name {
			return operation, true
		}
	}
	return mcpExposedOperation{}, false
}

// mcpDescription is the client-facing tool description: the catalog label plus
// the catalog description. No new prose, no hidden operation list.
func (o mcpExposedOperation) mcpDescription() string {
	label := strings.TrimSpace(o.label)
	description := strings.TrimSpace(o.description)
	switch {
	case label == "":
		return description
	case description == "":
		return label
	default:
		return label + ": " + description
	}
}

// mcpReadOnly reports whether the operation belongs to the read-only query
// tool, so the tool advertisement can carry a readOnlyHint.
func (o mcpExposedOperation) mcpReadOnly() bool {
	return o.tool == AutomationToolWorkbenchQuery
}
