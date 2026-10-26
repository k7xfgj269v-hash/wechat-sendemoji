package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

func DecodeAutomationToolArguments(tool AutomationToolName, raw json.RawMessage) (any, error) {
	arguments, _, err := decodeAutomationToolArguments("arguments", tool, raw)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return nil, NewAutomationValidationError("arguments", "could not be canonicalized")
	}
	if len(canonical) > AutomationMaxActionPayloadBytes {
		return nil, NewAutomationValidationError(
			"arguments",
			fmt.Sprintf("canonical payload must be at most %d bytes", AutomationMaxActionPayloadBytes),
		)
	}
	return arguments, nil
}

func AutomationToolCallReferences(call AutomationToolCall) ([]AutomationToolReference, error) {
	_, references, err := decodeAutomationToolArguments("arguments", call.Tool, call.Arguments)
	return references, err
}

func ValidateAutomationEnvelopeEvidence(envelope AutomationToolEnvelope, resolver AutomationEvidenceTextResolver) error {
	for i := range envelope.Actions {
		for j := range envelope.Actions[i].Evidence {
			evidence := envelope.Actions[i].Evidence[j]
			field := indexedField(indexedField("actions", i)+".evidence", j) + ".excerpt"
			if resolver == nil {
				return NewAutomationValidationError(field, "cannot be verified without evidence context")
			}
			contextText, ok := resolver(evidence)
			if !ok {
				return NewAutomationValidationError(field, "references unavailable evidence context")
			}
			if !strings.Contains(contextText, evidence.Excerpt) {
				return NewAutomationValidationError(field, "must occur in the referenced context field")
			}
		}
	}
	return nil
}

func IsValidAutomationClassificationLabel(label string) bool {
	switch label {
	case AutomationLabelCandidateInterest,
		AutomationLabelJobInquiry,
		AutomationLabelAvailability,
		AutomationLabelCompensation,
		AutomationLabelInterview,
		AutomationLabelOffer,
		AutomationLabelRejection,
		AutomationLabelOnboarding,
		AutomationLabelGeneral,
		AutomationLabelSpam,
		AutomationLabelSuspicious:
		return true
	default:
		return false
	}
}

func IsValidAutomationReplyIntent(intent string) bool {
	switch intent {
	case AutomationReplyIntentGeneral,
		AutomationReplyIntentScheduling,
		AutomationReplyIntentInformation,
		AutomationReplyIntentOffer,
		AutomationReplyIntentRejection,
		AutomationReplyIntentHiring,
		AutomationReplyIntentOnboarding,
		AutomationReplyIntentCompensationCommitment,
		AutomationReplyIntentLegalCommitment,
		AutomationReplyIntentPaymentRequest,
		AutomationReplyIntentCredentialRequest:
		return true
	default:
		return false
	}
}

func decodeAutomationToolCall(field string, raw json.RawMessage) (AutomationToolCall, []AutomationToolReference, error) {
	var call AutomationToolCall
	if err := decodeAutomationJSONObject(
		field,
		raw,
		&call,
		"call_id",
		"tool",
		"confidence",
		"evidence",
		"arguments",
	); err != nil {
		return AutomationToolCall{}, nil, err
	}

	var err error
	if call.CallID, err = normalizeAutomationString(field+".call_id", call.CallID, 80, true, false); err != nil {
		return AutomationToolCall{}, nil, err
	}
	if !IsValidAutomationToolName(call.Tool) {
		return AutomationToolCall{}, nil, NewAutomationValidationError(field+".tool", "unsupported automation tool")
	}
	if math.IsNaN(call.Confidence) || math.IsInf(call.Confidence, 0) || call.Confidence < 0 || call.Confidence > 1 {
		return AutomationToolCall{}, nil, NewAutomationValidationError(field+".confidence", "must be finite and between 0 and 1")
	}
	if call.Evidence == nil {
		return AutomationToolCall{}, nil, NewAutomationValidationError(field+".evidence", "must not be null")
	}
	if len(call.Evidence) > AutomationMaxEvidencePerAction {
		return AutomationToolCall{}, nil, NewAutomationValidationError(
			field+".evidence",
			fmt.Sprintf("must contain at most %d entries", AutomationMaxEvidencePerAction),
		)
	}
	for i := range call.Evidence {
		evidenceField := indexedField(field+".evidence", i)
		call.Evidence[i], err = normalizeAutomationEvidence(evidenceField, call.Evidence[i])
		if err != nil {
			return AutomationToolCall{}, nil, err
		}
	}

	arguments, references, err := decodeAutomationToolArguments(field+".arguments", call.Tool, call.Arguments)
	if err != nil {
		return AutomationToolCall{}, nil, err
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return AutomationToolCall{}, nil, NewAutomationValidationError(field+".arguments", "could not be canonicalized")
	}
	if len(canonical) > AutomationMaxActionPayloadBytes {
		return AutomationToolCall{}, nil, NewAutomationValidationError(
			field+".arguments",
			fmt.Sprintf("canonical payload must be at most %d bytes", AutomationMaxActionPayloadBytes),
		)
	}
	call.Arguments = canonical
	return call, references, nil
}

func decodeAutomationToolArguments(field string, tool AutomationToolName, raw json.RawMessage) (any, []AutomationToolReference, error) {
	if !IsValidAutomationToolName(tool) {
		return nil, nil, NewAutomationValidationError(field, "unsupported automation tool")
	}

	switch tool {
	case AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
		AutomationToolWorkbenchAllowOnce:
		decoded, err := DecodeWorkbenchToolRequest(tool, raw)
		if err != nil {
			return nil, nil, err
		}
		return decoded.Request, nil, nil

	case AutomationToolMessageClassify:
		var arguments MessageClassifyArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "labels", "summary"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeMessageClassifyArgs(field, arguments)
		return normalized, nil, err

	case AutomationToolCandidateCreate:
		var arguments CandidateCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "full_name", "skills"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeCandidateCreateArgs(field, arguments)
		return normalized, nil, err

	case AutomationToolCandidateUpdate:
		if err := rejectAutomationNullField(field, raw, "patch", "skills"); err != nil {
			return nil, nil, err
		}
		var arguments CandidateUpdateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "candidate", "patch"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeCandidateUpdateArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{{
			Field:      "candidate",
			EntityType: AutomationEntityCandidate,
			Reference:  normalized.Candidate,
		}}, nil

	case AutomationToolCandidateLink:
		var arguments CandidateLinkArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "candidate"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeCandidateLinkArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{{
			Field:      "candidate",
			EntityType: AutomationEntityCandidate,
			Reference:  normalized.Candidate,
		}}, nil

	case AutomationToolJobMatch:
		if err := validateAutomationJobMatchFields(field, raw); err != nil {
			return nil, nil, err
		}
		var arguments JobMatchArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "matches"); err != nil {
			return nil, nil, err
		}
		normalized, references, err := normalizeJobMatchArgs(field, arguments)
		return normalized, references, err

	case AutomationToolJobCreate:
		if err := rejectAutomationNullField(field, raw, "job", "requirements"); err != nil {
			return nil, nil, err
		}
		if err := rejectAutomationNullField(field, raw, "job", "required_documents"); err != nil {
			return nil, nil, err
		}
		if err := validateAutomationJobCreateFields(field, raw); err != nil {
			return nil, nil, err
		}
		var arguments JobCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "event_id", "group_id", "job"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeJobCreateArgs(field, arguments)
		return normalized, nil, err

	case AutomationToolLeadCreate:
		var arguments LeadCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "candidate", "job"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeLeadCreateArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{
			{Field: "candidate", EntityType: AutomationEntityCandidate, Reference: normalized.Candidate},
			{Field: "job", EntityType: AutomationEntityJob, Reference: normalized.Job},
		}, nil

	case AutomationToolLeadTransition:
		var arguments LeadTransitionArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "lead", "from_stage", "to_stage"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeLeadTransitionArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{{
			Field:      "lead",
			EntityType: AutomationEntityLead,
			Reference:  normalized.Lead,
		}}, nil

	case AutomationToolTaskCreate:
		var arguments TaskCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "title", "due_at", "target_type", "target"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeTaskCreateArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{{
			Field:      "target",
			EntityType: normalized.TargetType,
			Reference:  normalized.Target,
		}}, nil

	case AutomationToolNoteCreate:
		var arguments NoteCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "target_type", "target", "body"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeNoteCreateArgs(field, arguments)
		if err != nil {
			return nil, nil, err
		}
		return normalized, []AutomationToolReference{{
			Field:      "target",
			EntityType: string(normalized.TargetType),
			Reference:  normalized.Target,
		}}, nil

	case AutomationToolReplyPrivateText:
		var arguments ReplyPrivateTextArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "intent", "text"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeReplyPrivateTextArgs(field, arguments)
		return normalized, nil, err

	case AutomationToolJobUpdate:
		var arguments JobUpdateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "job", "patch"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeJobUpdateArgs(field, arguments)
		return normalized, automationSingleToolReference("job", AutomationEntityJob, normalized.Job), err

	case AutomationToolOnboardingCreate:
		var arguments OnboardingCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "lead"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeOnboardingCreateArgs(field, arguments)
		return normalized, automationSingleToolReference("lead", AutomationEntityLead, normalized.Lead), err

	case AutomationToolOnboardingUpdate:
		var arguments OnboardingUpdateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "onboarding", "patch"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeOnboardingUpdateArgs(field, arguments)
		return normalized, automationSingleToolReference("onboarding", AutomationEntityOnboarding, normalized.Onboarding), err

	case AutomationToolFleetUpdate:
		var arguments FleetUpdateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "onboarding", "patch"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeFleetUpdateArgs(field, arguments)
		return normalized, automationSingleToolReference("onboarding", AutomationEntityOnboarding, normalized.Onboarding), err

	case AutomationToolServiceCreate:
		var arguments ServiceRequestCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "candidate", "category", "description"); err != nil {
			return nil, nil, err
		}
		normalized, references, err := normalizeServiceRequestCreateArgs(field, arguments)
		return normalized, references, err

	case AutomationToolServiceUpdate:
		var arguments ServiceRequestUpdateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "service_request", "patch"); err != nil {
			return nil, nil, err
		}
		normalized, references, err := normalizeServiceRequestUpdateArgs(field, arguments)
		return normalized, references, err

	case AutomationToolFundCreate:
		var arguments FundCallbackCreateArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "candidate", "amount_minor", "currency", "condition", "promised_by"); err != nil {
			return nil, nil, err
		}
		normalized, references, err := normalizeFundCallbackCreateArgs(field, arguments)
		return normalized, references, err

	case AutomationToolReplyAudienceText:
		var arguments ReplyAudienceTextArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "audience_id", "intent", "text"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeReplyAudienceTextArgs(field, arguments)
		return normalized, nil, err

	case AutomationToolForwardEvent:
		var arguments ForwardEventArgs
		if err := decodeAutomationJSONObject(field, raw, &arguments, "event_id", "rule_id"); err != nil {
			return nil, nil, err
		}
		normalized, err := normalizeForwardEventArgs(field, arguments)
		return normalized, nil, err

	default:
		return nil, nil, NewAutomationValidationError(field, "unsupported automation tool")
	}
}
