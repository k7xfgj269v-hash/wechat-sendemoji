package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func DecodeWorkbenchToolRequest(
	tool AutomationToolName,
	raw json.RawMessage,
) (WorkbenchDecodedToolRequest, error) {

	if !IsWorkbenchToolName(tool) {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"tool",
			"unsupported workbench tool",
		)
	}
	var request WorkbenchToolRequest
	if err := decodeAutomationJSONObject(
		"arguments",
		raw,
		&request,
		"operation",
		"arguments_json",
	); err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	spec, ok := lookupWorkbenchOperationSpec(tool, request.Operation)
	if !ok {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"arguments.operation",
			"unsupported operation for workbench tool",
		)
	}
	if len(request.ArgumentsJSON) > WorkbenchMaxArgumentsJSONBytes {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"arguments.arguments_json",
			fmt.Sprintf("must be at most %d bytes", WorkbenchMaxArgumentsJSONBytes),
		)
	}
	normalizedArgumentsJSON, err := normalizeWorkbenchOperationArgumentAliases(
		request.Operation,
		[]byte(request.ArgumentsJSON),
	)
	if err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	arguments := spec.newArguments()
	if err := decodeAutomationJSONObject(
		"arguments.arguments_json",
		normalizedArgumentsJSON,
		arguments,
		spec.requiredFields...,
	); err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	if err := normalizeWorkbenchOperationArguments(request.Operation, arguments); err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	canonicalArguments, err := json.Marshal(arguments)
	if err != nil {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"arguments.arguments_json",
			"could not be canonicalized",
		)
	}
	if len(canonicalArguments) > WorkbenchMaxArgumentsJSONBytes {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"arguments.arguments_json",
			fmt.Sprintf("canonical arguments must be at most %d bytes", WorkbenchMaxArgumentsJSONBytes),
		)
	}
	request.ArgumentsJSON = string(canonicalArguments)
	canonicalRequest, err := json.Marshal(request)
	if err != nil {
		return WorkbenchDecodedToolRequest{}, NewAutomationValidationError(
			"arguments",
			"could not be canonicalized",
		)
	}
	return WorkbenchDecodedToolRequest{
		Tool:               tool,
		Request:            request,
		Arguments:          arguments,
		CanonicalArguments: canonicalArguments,
		CanonicalRequest:   canonicalRequest,
	}, nil
}

func normalizeWorkbenchOperationArgumentAliases(
	operation WorkbenchOperation,
	data []byte,
) ([]byte, error) {
	if operation != WorkbenchOperationContactsSearch {
		return data, nil
	}
	trimmed := trimAutomationJSONWhitespace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data, nil
	}
	if err := rejectAutomationDuplicateJSONKeys("arguments.arguments_json", trimmed); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return data, nil
	}
	query, hasQuery := object["query"]
	if hasQuery {
		if _, hasKeyword := object["keyword"]; hasKeyword {
			return nil, NewAutomationValidationError(
				"arguments.arguments_json.query",
				"cannot be combined with keyword",
			)
		}
		delete(object, "query")
		object["keyword"] = query
	}
	if channel, ok := object["channel"]; ok {
		var channelValue string
		if err := json.Unmarshal(channel, &channelValue); err != nil ||
			strings.TrimSpace(channelValue) != "wechat" {
			return nil, NewAutomationValidationError(
				"arguments.arguments_json.channel",
				"must be \"wechat\"",
			)
		}
		delete(object, "channel")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, NewAutomationValidationError(
			"arguments.arguments_json",
			"could not be canonicalized",
		)
	}
	return normalized, nil
}

func DecodeWorkbenchToolResult(data []byte) (WorkbenchToolResult, error) {
	var result WorkbenchToolResult
	if err := decodeAutomationJSONObject("result", data, &result, "operation", "ok"); err != nil {
		return WorkbenchToolResult{}, err
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		return WorkbenchToolResult{}, err
	}
	return result, nil
}

func NewWorkbenchToolSuccess(
	operation WorkbenchOperation,
	data any,
	page *WorkbenchPage,
) WorkbenchToolResult {
	raw, err := json.Marshal(data)
	if err != nil {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInternal, "result could not be encoded", false)
	}
	result := WorkbenchToolResult{
		Operation: operation,
		OK:        true,
		Data:      raw,
		Page:      cloneWorkbenchPage(page),
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorResultTooLarge, "result exceeds the allowed size", false)
	}
	return result
}

func NewWorkbenchToolFailure(
	operation WorkbenchOperation,
	code WorkbenchErrorCode,
	message string,
	retryable bool,
) WorkbenchToolResult {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "workbench tool failed"
	}
	if len([]rune(message)) > 500 {
		message = string([]rune(message)[:500])
	}
	return WorkbenchToolResult{
		Operation: operation,
		OK:        false,
		Error: &WorkbenchError{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
	}
}

func ValidateWorkbenchToolResult(result WorkbenchToolResult) error {
	if !isKnownWorkbenchOperation(result.Operation) {
		return NewAutomationValidationError("result.operation", "unsupported workbench operation")
	}
	if result.OK {
		if result.Error != nil {
			return NewAutomationValidationError("result.error", "must be absent for a successful result")
		}
		if len(result.Data) != 0 && !json.Valid(result.Data) {
			return NewAutomationValidationError("result.data", "must contain valid JSON")
		}
	} else {
		if len(result.Data) != 0 || result.Page != nil {
			return NewAutomationValidationError("result", "failed results must not contain data or page metadata")
		}
		if result.Error == nil {
			return NewAutomationValidationError("result.error", "is required for a failed result")
		}
		if !IsValidWorkbenchErrorCode(result.Error.Code) {
			return NewAutomationValidationError("result.error.code", "unsupported workbench error code")
		}
		if strings.TrimSpace(result.Error.Message) == "" || len([]rune(result.Error.Message)) > 500 {
			return NewAutomationValidationError("result.error.message", "must contain 1 through 500 characters")
		}
	}
	if result.Page != nil {
		if result.Page.Limit < 1 || result.Page.Limit > WorkbenchMaxPageLimit ||
			result.Page.Offset < 0 ||
			result.Page.Returned < 0 ||
			result.Page.Returned > result.Page.Limit ||
			result.Page.Total < 0 ||
			result.Page.NextOffset < 0 {
			return NewAutomationValidationError("result.page", "contains invalid pagination metadata")
		}
		if result.Page.HasMore && result.Page.NextOffset <= result.Page.Offset {
			return NewAutomationValidationError("result.page.next_offset", "must advance when more results exist")
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return NewAutomationValidationError("result", "could not be encoded")
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		return NewAutomationValidationError(
			"result",
			fmt.Sprintf("must be at most %d bytes", WorkbenchMaxResultJSONBytes),
		)
	}
	return nil
}
