package main

import (
	"crypto/sha256"
	"time"
)

func validateFundCallback(field string, callback *FundCallback) error {
	if err := validateStoredCRMString(field+".candidate_id", callback.CandidateID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".job_id", callback.JobID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".lead_id", callback.LeadID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".onboarding_case_id", callback.OnboardingCaseID, 80, false, false); err != nil {
		return err
	}
	if callback.AmountMinor <= 0 {
		return NewCRMValidationError(field+".amount_minor", "must be greater than zero")
	}
	if !isValidCRMCurrency(callback.Currency) {
		return NewCRMValidationError(field+".currency", "must be a three-letter uppercase currency code")
	}
	if err := validateStoredCRMString(field+".condition", callback.Condition, 4000, true, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".promised_by", callback.PromisedBy, 160, true, false); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".due_at", callback.DueAt); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".evidence", callback.Evidence, 4000, false, true); err != nil {
		return err
	}
	if !IsValidFundCallbackStatus(callback.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateStoredCRMString(field+".commitment_by", callback.CommitmentBy, 80, false, false); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".commitment_at", callback.CommitmentAt); err != nil {
		return err
	}
	if (callback.CommitmentBy == "") != (callback.CommitmentAt == nil) {
		return NewCRMValidationError(field+".commitment_at", "must be present together with commitment_by")
	}
	if err := validateStoredCRMString(field+".final_result", callback.FinalResult, 4000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".final_confirmed_by", callback.FinalConfirmedBy, 80, false, false); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".final_confirmed_at", callback.FinalConfirmedAt); err != nil {
		return err
	}
	if callback.FinalConfirmedAt != nil && (callback.FinalConfirmedBy == "" || callback.FinalResult == "") {
		return NewCRMValidationError(field+".final_confirmed_at", "requires final_result and final_confirmed_by")
	}
	if callback.FinalConfirmedAt == nil && callback.FinalConfirmedBy != "" {
		return NewCRMValidationError(field+".final_confirmed_by", "requires final_confirmed_at")
	}
	if err := validateVersionedTimes(field, callback.Version, callback.CreatedAt, callback.UpdatedAt); err != nil {
		return err
	}
	for _, value := range []struct {
		field string
		time  *time.Time
	}{
		{field + ".commitment_at", callback.CommitmentAt},
		{field + ".final_confirmed_at", callback.FinalConfirmedAt},
	} {
		if value.time != nil && (value.time.Before(callback.CreatedAt) || value.time.After(callback.UpdatedAt)) {
			return NewCRMValidationError(value.field, "must be between created_at and updated_at")
		}
	}
	return nil
}

func validateFundCallbackLedgerEntry(field string, entry *FundCallbackLedgerEntry) error {
	if err := validateStoredCRMString(field+".fund_callback_id", entry.FundCallbackID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".candidate_id", entry.CandidateID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".type", entry.Type, 120, true, false); err != nil {
		return err
	}
	switch entry.Type {
	case "create", "update", "commitment", "final", "dispute", "cancel", "status_change":
	default:
		return NewCRMValidationError(field+".type", "is invalid")
	}
	if entry.Type != "create" && entry.CandidateID != "" {
		return NewCRMValidationError(field+".candidate_id", "must be empty for non-create entries")
	}
	if entry.FromStatus != "" && !IsValidFundCallbackStatus(entry.FromStatus) {
		return NewCRMValidationError(field+".from_status", "is invalid")
	}
	if !IsValidFundCallbackStatus(entry.ToStatus) {
		return NewCRMValidationError(field+".to_status", "is invalid")
	}
	if entry.Type == "create" && entry.FromStatus != "" {
		return NewCRMValidationError(field+".from_status", "must be empty for create entries")
	}
	if entry.Type != "create" && entry.FromStatus == "" {
		return NewCRMValidationError(field+".from_status", "is required for non-create entries")
	}
	if entry.AmountMinor <= 0 {
		return NewCRMValidationError(field+".amount_minor", "must be greater than zero")
	}
	if !isValidCRMCurrency(entry.Currency) {
		return NewCRMValidationError(field+".currency", "must be a three-letter uppercase currency code")
	}
	if err := validateStoredCRMString(field+".operator_id", entry.OperatorID, 80, true, false); err != nil {
		return err
	}
	if len(entry.EvidenceSHA256) != sha256.Size*2 {
		return NewCRMValidationError(field+".evidence_sha256", "must be a SHA-256 hex digest")
	}
	for _, character := range entry.EvidenceSHA256 {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return NewCRMValidationError(field+".evidence_sha256", "must be a lowercase SHA-256 hex digest")
		}
	}
	return validateCRMTime(field+".created_at", entry.CreatedAt)
}

func validateFundCallbackApprovedActionBinding(
	field string,
	binding *FundCallbackApprovedActionBinding,
) error {
	if err := validateStoredCRMString(field+".action_id", binding.ActionID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(
		field+".permission_request_id",
		binding.PermissionRequestID,
		80,
		true,
		false,
	); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".fund_callback_id", binding.FundCallbackID, 80, true, false); err != nil {
		return err
	}
	if binding.OriginalResult == nil {
		return NewCRMValidationError(field+".original_result", "is required")
	}
	if binding.OriginalResultSHA256 == "" {
		return NewCRMValidationError(field+".original_result_sha256", "is required")
	}
	result := binding.OriginalResult
	if err := validateStoredCRMString(field+".original_result.id", result.ID, 80, true, false); err != nil {
		return err
	}
	if result.ID != binding.FundCallbackID {
		return NewCRMValidationError(field+".original_result.id", "must match fund_callback_id")
	}
	if err := validateStoredCRMString(
		field+".original_result.candidate_id",
		result.CandidateID,
		80,
		true,
		false,
	); err != nil {
		return err
	}
	if result.AmountMinor <= 0 {
		return NewCRMValidationError(field+".original_result.amount_minor", "must be greater than zero")
	}
	if !isValidCRMCurrency(result.Currency) {
		return NewCRMValidationError(
			field+".original_result.currency",
			"must be a three-letter uppercase currency code",
		)
	}
	if result.Status != FundCallbackStatusDraft {
		return NewCRMValidationError(field+".original_result.status", "must be draft")
	}
	if result.Version != 1 {
		return NewCRMValidationError(field+".original_result.version", "must be 1")
	}
	if err := validateCRMTime(field+".original_result.updated_at", result.UpdatedAt); err != nil {
		return err
	}
	if len(binding.OriginalResultSHA256) != sha256.Size*2 {
		return NewCRMValidationError(
			field+".original_result_sha256",
			"must be a SHA-256 hex digest",
		)
	}
	for _, character := range binding.OriginalResultSHA256 {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return NewCRMValidationError(
				field+".original_result_sha256",
				"must be a lowercase SHA-256 hex digest",
			)
		}
	}
	expectedSHA256, err := crmFundCallbackApprovedActionResultSHA256(*result)
	if err != nil {
		return NewCRMValidationError(field+".original_result", "could not be encoded")
	}
	if binding.OriginalResultSHA256 != expectedSHA256 {
		return NewCRMValidationError(
			field+".original_result_sha256",
			"does not match original_result",
		)
	}
	return nil
}

func validateCRMAuditEvent(field string, event *CRMAuditEvent) error {
	if err := validateStoredCRMString(field+".actor", event.Actor, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".operation", event.Operation, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".entity_type", event.EntityType, 80, false, false); err != nil {
		return err
	}
	if event.EntityType != "" && !isValidCRMAuditEntityType(event.EntityType) {
		return NewCRMValidationError(field+".entity_type", "is invalid")
	}
	if err := validateStoredCRMString(field+".entity_id", event.EntityID, 80, false, false); err != nil {
		return err
	}
	if event.EntityID != "" && event.EntityType == "" {
		return NewCRMValidationError(field+".entity_type", "is required when entity_id is set")
	}
	if err := validateStoredCRMString(field+".source_event_id", event.SourceEventID, 80, false, false); err != nil {
		return err
	}
	return validateCRMTime(field+".created_at", event.CreatedAt)
}

func validateTask(field string, task *FollowUpTask) error {
	if err := validateStoredCRMString(field+".title", task.Title, 200, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".description", task.Description, 4000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".owner", task.Owner, 80, false, false); err != nil {
		return err
	}
	if !IsValidFollowUpStatus(task.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateCRMTime(field+".due_at", task.DueAt); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".candidate_id", task.CandidateID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".lead_id", task.LeadID, 80, false, false); err != nil {
		return err
	}
	if task.CandidateID == "" && task.LeadID == "" {
		return NewCRMValidationError(field, "requires candidate_id or lead_id")
	}
	if err := validateVersionedTimes(field, task.Version, task.CreatedAt, task.UpdatedAt); err != nil {
		return err
	}
	switch task.Status {
	case FollowUpStatusOpen:
		if task.CompletedAt != nil {
			return NewCRMValidationError(field+".completed_at", "must be absent for an open task")
		}
	case FollowUpStatusCompleted:
		if task.CompletedAt == nil {
			return NewCRMValidationError(field+".completed_at", "is required for a completed task")
		}
		if err := validateCRMTime(field+".completed_at", *task.CompletedAt); err != nil {
			return err
		}
		if task.CompletedAt.Before(task.CreatedAt) || task.CompletedAt.After(task.UpdatedAt) {
			return NewCRMValidationError(field+".completed_at", "must be between created_at and updated_at")
		}
	}
	return nil
}

func validateNote(field string, note *Note) error {
	if !IsValidNoteTargetType(note.TargetType) {
		return NewCRMValidationError(field+".target_type", "is invalid")
	}
	if err := validateStoredCRMString(field+".target_id", note.TargetID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".author", note.Author, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".body", note.Body, 4000, true, true); err != nil {
		return err
	}
	return validateCRMTime(field+".created_at", note.CreatedAt)
}

func validateMessageEvent(field string, event *MessageEvent) error {
	if err := validateStoredCRMString(field+".message_key", event.MessageKey, 1024, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".source", event.Source, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".mid", event.Mid, 256, false, false); err != nil {
		return err
	}
	if !IsValidChatType(event.ChatType) {
		return NewCRMValidationError(field+".chat_type", "is invalid")
	}
	if err := validateStoredCRMString(field+".peer_id", event.PeerID, 256, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".author_id", event.AuthorID, 256, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".group_id", event.GroupID, 256, event.ChatType == ChatTypeGroup, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".text", event.Text, 4000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".kinds", event.Kinds, 32, 32); err != nil {
		return err
	}
	if !IsValidReviewStatus(event.ReviewStatus) {
		return NewCRMValidationError(field+".review_status", "is invalid")
	}
	if event.Disposition != "" {
		if !IsValidMessageDisposition(event.Disposition) {
			return NewCRMValidationError(field+".disposition", "is invalid")
		}
		if event.ChatType == ChatTypePrivate && event.Disposition != MessageDispositionPrivateCandidate {
			return NewCRMValidationError(field+".disposition", "must be private_candidate for private messages")
		}
		if event.ChatType == ChatTypeGroup && event.Disposition == MessageDispositionPrivateCandidate {
			return NewCRMValidationError(field+".disposition", "is invalid for group messages")
		}
	}
	if event.RecruitingProjectVersion == 0 {
		if event.RecruitingRouteSourceVersion != 0 ||
			event.RecruitingConversationKind != "" ||
			event.RecruitingAllowedTools != nil ||
			event.RecruitingOrganizationName != "" ||
			event.RecruitingRequireAuthoritative ||
			event.RecruitingSourceGroupID != "" ||
			event.RecruitingSourceGroupName != "" ||
			event.RecruitingSourceGroupVersion != 0 ||
			event.RecruitingSourceGateEnabled ||
			event.RecruitingSourceGateMode != "" {
			return NewCRMValidationError(field+".recruiting_project_version", "is required for a recruiting route snapshot")
		}
	} else {
		if event.Source != crmMessageSource {
			return NewCRMValidationError(field+".source", "must be wechat for a recruiting route snapshot")
		}
		if event.RecruitingRouteSourceVersion == 0 ||
			event.RecruitingRouteSourceVersion >= event.Version {
			return NewCRMValidationError(
				field+".recruiting_route_source_version",
				"must reference an earlier positive message version",
			)
		}
		if err := validateStoredCRMString(
			field+".recruiting_organization_name",
			event.RecruitingOrganizationName,
			200,
			true,
			true,
		); err != nil {
			return err
		}
		if err := validateRecruitingRouteTools(field, event); err != nil {
			return err
		}
		switch event.RecruitingConversationKind {
		case automationConversationWeChatPrivate:
			if event.ChatType != ChatTypePrivate ||
				event.Disposition != MessageDispositionPrivateCandidate {
				return NewCRMValidationError(
					field+".recruiting_conversation_kind",
					"does not match a private recruiting message",
				)
			}
		case automationConversationWeChatSourceGroup:
			if event.ChatType != ChatTypeGroup ||
				event.Disposition != MessageDispositionSourceGroupJob {
				return NewCRMValidationError(
					field+".recruiting_conversation_kind",
					"does not match a source-group recruiting message",
				)
			}
			if err := validateStoredCRMString(
				field+".recruiting_source_group_id",
				event.RecruitingSourceGroupID,
				80,
				true,
				false,
			); err != nil {
				return err
			}
			if err := validateStoredCRMString(
				field+".recruiting_source_group_name",
				event.RecruitingSourceGroupName,
				200,
				false,
				true,
			); err != nil {
				return err
			}
			if event.RecruitingSourceGroupVersion == 0 {
				return NewCRMValidationError(field+".recruiting_source_group_version", "must be positive")
			}
			if err := validateStoredCRMString(
				field+".recruiting_source_gate_mode",
				event.RecruitingSourceGateMode,
				80,
				false,
				false,
			); err != nil {
				return err
			}
			if event.RecruitingSourceGateMode != "" &&
				event.RecruitingSourceGateMode != RecruitingSourceGateModeJobRelevance {
				return NewCRMValidationError(
					field+".recruiting_source_gate_mode",
					"must be job_relevance",
				)
			}
			if event.RecruitingSourceGateEnabled && event.RecruitingSourceGateMode == "" {
				return NewCRMValidationError(
					field+".recruiting_source_gate_mode",
					"is required when gate is enabled",
				)
			}
		case automationConversationWeChatGroupNoise:
			if event.ChatType != ChatTypeGroup ||
				event.Disposition != MessageDispositionGroupNoise {
				return NewCRMValidationError(
					field+".recruiting_conversation_kind",
					"does not match a group-noise message",
				)
			}
		default:
			return NewCRMValidationError(field+".recruiting_conversation_kind", "is invalid")
		}
		if event.RecruitingConversationKind != automationConversationWeChatSourceGroup &&
			(event.RecruitingSourceGroupID != "" ||
				event.RecruitingSourceGroupName != "" ||
				event.RecruitingSourceGroupVersion != 0 ||
				event.RecruitingSourceGateEnabled ||
				event.RecruitingSourceGateMode != "") {
			return NewCRMValidationError(
				field+".recruiting_source_group_id",
				"is only valid for source-group events",
			)
		}
	}
	if err := validateStoredCRMString(field+".candidate_id", event.CandidateID, 80, false, false); err != nil {
		return err
	}
	if event.CandidateID != "" && event.ReviewStatus != ReviewStatusReviewed {
		return NewCRMValidationError(field+".review_status", "must be reviewed when a candidate is linked")
	}
	if event.MessageKey != MessageKeyForEvent(*event) {
		return NewCRMValidationError(field+".message_key", "does not match the canonical message key")
	}
	return validateVersionedTimes(field, event.Version, event.CreatedAt, event.UpdatedAt)
}
