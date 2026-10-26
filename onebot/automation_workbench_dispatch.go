package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

const workbenchUnavailableMessage = "operation is unavailable in this workbench release"

type WorkbenchToolDependencies struct {
	CRM          *CRMStore
	Agent        AgentClient
	Automation   *AutomationStore
	Audit        *AutomationAuditLedger
	Audiences    AutomationAudienceResolver
	Sender       AutomationTextSender
	ForwardRules *ForwardRuleStore
}

type defaultWorkbenchToolDispatcher struct {
	crm          *CRMStore
	agent        AgentClient
	automation   *AutomationStore
	audit        *AutomationAuditLedger
	audiences    AutomationAudienceResolver
	sender       AutomationTextSender
	forwardRules *ForwardRuleStore
}

func NewDefaultWorkbenchToolDispatcher(
	crm *CRMStore,
	agents ...AgentClient,
) WorkbenchToolDispatcher {
	agent := currentHRWorkbenchAgentClient()
	if len(agents) > 0 {
		agent = agents[0]
	}
	return NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:   crm,
		Agent: agent,
	})
}

func NewDefaultWorkbenchToolDispatcherWithDependencies(
	dependencies WorkbenchToolDependencies,
) WorkbenchToolDispatcher {
	return &defaultWorkbenchToolDispatcher{
		crm:          dependencies.CRM,
		agent:        dependencies.Agent,
		automation:   dependencies.Automation,
		audit:        dependencies.Audit,
		audiences:    dependencies.Audiences,
		sender:       dependencies.Sender,
		forwardRules: dependencies.ForwardRules,
	}
}

func (d *defaultWorkbenchToolDispatcher) Dispatch(
	ctx context.Context,
	execution WorkbenchExecutionContext,
	decoded WorkbenchDecodedToolRequest,
) WorkbenchToolResult {
	operation := decoded.Request.Operation
	if ctx == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInvalidArguments,
			"context is required",
			false,
		)
	}
	if err := ctx.Err(); err != nil {
		return workbenchContextFailure(operation, err)
	}
	if d == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"workbench dispatcher is unavailable",
			true,
		)
	}
	if err := validateWorkbenchDecodedRequest(decoded); err != nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInvalidArguments,
			"decoded workbench request is invalid",
			false,
		)
	}
	definition, ok := WorkbenchOperationDefinitionFor(decoded.Tool, operation)
	if !ok {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnsupportedOperation,
			"unsupported workbench operation",
			false,
		)
	}
	if !workbenchOperationEffectiveAvailable(definition) {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			workbenchUnavailableMessage,
			false,
		)
	}
	if definition.Handler == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			workbenchUnavailableMessage,
			false,
		)
	}
	return definition.Handler(ctx, d, execution, decoded.Arguments)
}

func validateWorkbenchDecodedRequest(decoded WorkbenchDecodedToolRequest) error {
	if !IsWorkbenchToolName(decoded.Tool) || decoded.Request.Operation == "" {
		return errors.New("invalid workbench request identity")
	}
	if decoded.Request.ArgumentsJSON != string(decoded.CanonicalArguments) {
		return errors.New("canonical arguments do not match the request")
	}
	encodedArguments, err := json.Marshal(decoded.Arguments)
	if err != nil || !bytes.Equal(encodedArguments, decoded.CanonicalArguments) {
		return errors.New("decoded arguments are not canonical")
	}
	encodedRequest, err := json.Marshal(decoded.Request)
	if err != nil || !bytes.Equal(encodedRequest, decoded.CanonicalRequest) {
		return errors.New("decoded request is not canonical")
	}
	canonical, err := DecodeWorkbenchToolRequest(decoded.Tool, decoded.CanonicalRequest)
	if err != nil ||
		canonical.Request != decoded.Request ||
		!bytes.Equal(canonical.CanonicalArguments, decoded.CanonicalArguments) ||
		!bytes.Equal(canonical.CanonicalRequest, decoded.CanonicalRequest) ||
		reflect.TypeOf(canonical.Arguments) != reflect.TypeOf(decoded.Arguments) ||
		!reflect.DeepEqual(canonical.Arguments, decoded.Arguments) {
		return errors.New("decoded request was not produced by the workbench decoder")
	}
	return nil
}

func newWorkbenchPage(limit, offset, total, returned int) *WorkbenchPage {
	hasMore := offset+returned < total
	nextOffset := 0
	if hasMore {
		nextOffset = offset + returned
	}
	return &WorkbenchPage{
		Limit:      limit,
		Offset:     offset,
		Returned:   returned,
		Total:      total,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}
}

func workbenchResult(
	operation WorkbenchOperation,
	data any,
	err error,
) WorkbenchToolResult {
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, data, nil)
}

func workbenchDomainFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return workbenchContextFailure(operation, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return workbenchContextFailure(operation, context.Canceled)
	case workbenchCRMNotFound(err):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, err.Error(), false)
	case errors.Is(err, ErrCRMConflict):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, err.Error(), false)
	case errors.Is(err, ErrCRMValidation), errors.Is(err, ErrAutomationValidation):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInvalidArguments, err.Error(), false)
	case errors.Is(err, ErrCRMStoreClosed), errors.Is(err, ErrCRMPersistence):
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"CRM store is unavailable",
			true,
		)
	default:
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInternal,
			"CRM operation failed",
			false,
		)
	}
}

func workbenchAgentFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return workbenchContextFailure(operation, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return workbenchContextFailure(operation, context.Canceled)
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"WeChat agent operation failed",
			true,
		)
	}
	message := publicAgentError(agentErr, "WeChat agent operation failed")
	switch agentErr.Code {
	case AgentErrorCodeValidation, AgentErrorCodeInvalidFrame,
		AgentErrorCodeFrameTooLarge, AgentErrorCodeUnsupportedVersion,
		AgentErrorCodeUnsupportedOperation:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInvalidArguments, message, false)
	case AgentErrorCodeNotFound:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, message, false)
	case AgentErrorCodeConflict, AgentErrorCodeCursorExpired:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, message, false)
	case AgentErrorCodeUnauthorizedPeer:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorForbidden, message, false)
	case AgentErrorCodeDeadlineExceeded:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorDeadlineExceeded, message, true)
	case AgentErrorCodeUnavailable:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, message, true)
	default:
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInternal,
			"WeChat agent operation failed",
			false,
		)
	}
}

func workbenchCRMNotFound(err error) bool {
	var candidate *crmCandidateNotFoundError
	var pipeline *crmPipelineNotFoundError
	var activity *crmActivityNotFoundError
	return errors.Is(err, crmMessageErrNotFound) ||
		errors.As(err, &candidate) ||
		errors.As(err, &pipeline) ||
		errors.As(err, &activity)
}

func workbenchContextFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorDeadlineExceeded,
			"workbench operation deadline exceeded",
			true,
		)
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorCancelled,
		"workbench operation was cancelled",
		false,
	)
}

func requireWorkbenchAllowOnce(
	operation WorkbenchOperation,
	execution WorkbenchExecutionContext,
) *WorkbenchToolResult {
	switch execution.PermissionDecision {
	case AutomationPermissionAllowOnce:
		if strings.TrimSpace(execution.OperatorID) == "" {
			result := NewWorkbenchToolFailure(
				operation,
				WorkbenchErrorForbidden,
				"approved operator is required",
				false,
			)
			return &result
		}
		return nil
	case AutomationPermissionAllowPersistent:
		if !persistentHighRiskSendEnabled() ||
			!isPersistentHighRiskSendOperation(operation) ||
			strings.TrimSpace(execution.PermissionRequestID) == "" ||
			strings.TrimSpace(execution.OperatorID) == "" {
			result := NewWorkbenchToolFailure(
				operation,
				WorkbenchErrorAllowOnceRequired,
				"an enabled persistent outbound approval is required",
				false,
			)
			return &result
		}
		return nil
	case AutomationPermissionDenyOnce:
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorForbidden,
			"operation was denied",
			false,
		)
		return &result
	default:
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorAllowOnceRequired,
			"a fresh allow_once approval is required",
			false,
		)
		return &result
	}
}
