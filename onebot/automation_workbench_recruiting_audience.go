package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func workbenchRecruitingAudiencePolicyResolver(
	evaluator *ServerAutomationRiskEvaluator,
	input AutomationPolicyInput,
	decoded WorkbenchDecodedToolRequest,
) (*AutomationRecipient, error) {
	if evaluator == nil {
		return nil, NewAutomationValidationError("recipient", "risk evaluator is unavailable")
	}
	arguments, ok := decoded.Arguments.(*WorkbenchAudienceSendArguments)
	if !ok {
		return nil, NewAutomationValidationError("recipient", "audience send arguments are invalid")
	}
	audience, err := workbenchRecruitingResolveAudience(
		evaluator.audiences,
		arguments.AudienceID,
		arguments.Intent,
		arguments.Text,
	)
	if err != nil {
		return nil, err
	}
	if input.RunActionCount > audience.PerRunRecipientCap {
		return nil, NewAutomationValidationError("recipient", "audience recipient cap exceeded")
	}
	return workbenchRecruitingAudienceRecipient(audience), nil
}

func workbenchRecruitingResolveAudience(
	resolver AutomationAudienceResolver,
	audienceID string,
	intent string,
	text string,
) (AutomationAudience, error) {
	if resolver == nil {
		return AutomationAudience{}, errors.New("configured audience resolver is unavailable")
	}
	audience, ok := resolver.ResolveAutomationAudience(strings.TrimSpace(audienceID))
	if !ok || !audience.Enabled {
		return AutomationAudience{}, NewAutomationValidationError("audience_id", "configured audience is unavailable")
	}
	if audience.ID != strings.TrimSpace(audienceID) {
		return AutomationAudience{}, NewAutomationValidationError("audience_id", "configured audience identity is invalid")
	}
	if !containsAutomationIntent(audience.AllowedIntents, intent) {
		return AutomationAudience{}, NewAutomationValidationError("intent", "is not allowed for the configured audience")
	}
	if isForbiddenReply(ReplyPrivateTextArgs{Intent: intent, Text: text}) {
		return AutomationAudience{}, NewAutomationValidationError("text", "contains a forbidden capability")
	}
	if audience.ApprovedContent == "" ||
		audience.ContentSHA256 == "" ||
		audience.ExpiresAt == nil ||
		audience.PerHourLimit < 1 ||
		audience.PerRunRecipientCap < 1 {
		return AutomationAudience{}, NewAutomationValidationError("audience_id", "approved audience scope is incomplete")
	}
	if audience.ContentSHA256 != automationPromptSHA256(normalizeAutomationAudienceContent(text)) {
		return AutomationAudience{}, NewAutomationValidationError("text", "does not match approved audience content")
	}
	if !audience.ExpiresAt.After(time.Now().UTC()) {
		return AutomationAudience{}, NewAutomationValidationError("audience_id", "approved audience scope has expired")
	}
	normalized, err := normalizeAutomationAudience(audience, true)
	if err != nil {
		return AutomationAudience{}, NewAutomationValidationError("audience_id", "configured audience is invalid")
	}
	if err := workbenchRecruitingValidateAudienceRecipient(normalized); err != nil {
		return AutomationAudience{}, err
	}
	return normalized, nil
}

func workbenchRecruitingApprovedAudienceRecipient(
	parent AutomationAction,
	audience AutomationAudience,
) (*AutomationRecipient, error) {
	approved := parent.Recipient
	if approved == nil ||
		approved.AudienceID == "" ||
		approved.AudienceVersion == 0 ||
		!IsValidChatType(approved.ChatType) ||
		strings.TrimSpace(approved.PeerID) == "" {
		return nil, errWorkbenchRecruitingAudienceBindingMissing
	}
	if approved.AudienceID != audience.ID {
		return nil, errWorkbenchRecruitingAudienceRecipientChanged
	}
	if approved.AudienceVersion != audience.Version {
		return nil, errWorkbenchRecruitingAudienceVersionChanged
	}
	if approved.ChatType != audience.ChatType || approved.PeerID != audience.PeerID {
		return nil, errWorkbenchRecruitingAudienceRecipientChanged
	}
	return cloneAutomationRecipient(approved), nil
}

func workbenchRecruitingAudienceRateLimit(
	state AutomationState,
	runID string,
	audience AutomationAudience,
	now time.Time,
) error {
	hourStart := now.Add(-time.Hour)
	hourCount := 0
	runCount := 0
	outboxByAction := make(map[string]AutomationOutboxEntry, len(state.Outbox))
	for _, entry := range state.Outbox {
		outboxByAction[entry.ActionID] = entry
	}
	for _, action := range state.Actions {
		if action.Tool != AutomationToolReplyAudienceText ||
			action.Recipient == nil ||
			action.Recipient.AudienceID != audience.ID ||
			!workbenchRecruitingAudienceReservationActive(outboxByAction, action) {
			continue
		}
		if !action.CreatedAt.Before(hourStart) {
			hourCount++
		}
		if action.RunID == runID {
			runCount++
		}
	}
	if hourCount >= audience.PerHourLimit {
		return NewAutomationValidationError("audience_id", "configured audience hourly limit reached")
	}
	if runCount >= audience.PerRunRecipientCap {
		return NewAutomationValidationError("audience_id", "configured audience run limit reached")
	}
	return nil
}

func workbenchRecruitingAudienceReservationActive(
	outboxByAction map[string]AutomationOutboxEntry,
	action AutomationAction,
) bool {
	switch action.Status {
	case AutomationActionApproved,
		AutomationActionExecuting,
		AutomationActionSucceeded,
		AutomationActionUnknown:
		return true
	case AutomationActionFailed:
		if entry, ok := outboxByAction[action.ID]; ok {
			return entry.DispatchAttempted ||
				entry.Status == AutomationOutboxSent ||
				entry.Status == AutomationOutboxUnknown
		}
	}
	return false
}

func workbenchRecruitingAudienceReplay(
	store *AutomationStore,
	parentID string,
	payload json.RawMessage,
) *WorkbenchToolResult {
	bridge, ok := workbenchRecruitingFindBridge(store.Snapshot(), parentID, workbenchRecruitingBridgeAudience)
	if !ok {
		return nil
	}
	if !jsonEqual(bridge.Payload, payload) {
		result := NewWorkbenchToolFailure(
			WorkbenchOperationMessagesSendAudience,
			WorkbenchErrorConflict,
			"approved audience send conflicts with an existing execution",
			false,
		)
		return &result
	}
	result := workbenchRecruitingAudienceBridgeResult(store, bridge)
	return &result
}

func workbenchRecruitingAudienceBridgeResult(
	store *AutomationStore,
	bridge AutomationAction,
) WorkbenchToolResult {
	operation := WorkbenchOperationMessagesSendAudience
	if bridge.Status == AutomationActionApproved || bridge.Status == AutomationActionExecuting {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, "approved audience send is already executing", false)
	}
	if bridge.Status != AutomationActionSucceeded {
		code := workbenchRecruitingExecutionCode(bridge)
		return NewWorkbenchToolFailure(
			operation,
			code,
			"controlled audience send failed",
			bridge.Retryable || code == WorkbenchErrorUnavailable,
		)
	}
	if bridge.Recipient == nil ||
		bridge.Recipient.AudienceID == "" ||
		bridge.Recipient.AudienceVersion == 0 {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInternal,
			"controlled audience send result is unavailable",
			false,
		)
	}
	entry, err := store.GetOutboxByActionID(context.Background(), bridge.ID)
	if err != nil {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInternal, "controlled audience send result is unavailable", false)
	}
	return NewWorkbenchToolSuccess(operation, workbenchRecruitingAudienceSendResult{
		AudienceID:      bridge.Recipient.AudienceID,
		AudienceVersion: bridge.Recipient.AudienceVersion,
		OutboxID:        entry.ID,
		Status:          entry.Status,
	}, nil)
}
