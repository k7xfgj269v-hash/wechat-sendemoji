package main

import (
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	automationReasonR0InformationOnly        = "r0_information_only"
	automationReasonR0WorkbenchQuery         = "r0_workbench_query"
	automationReasonR1InternalWrite          = "r1_internal_write"
	automationReasonR2OriginalPrivateText    = "r2_original_private_text"
	automationReasonR2ConfiguredAudience     = "r2_configured_audience"
	automationReasonSuspiciousExternalReview = "suspicious_external_review"
	automationReasonR3WorkbenchAllowOnce     = "r3_workbench_allow_once"
	automationReasonR3TerminalCandidate      = "r3_terminal_candidate"
	automationReasonR3TerminalLead           = "r3_terminal_lead"
	automationReasonR3SensitiveCommitment    = "r3_sensitive_commitment"
	automationReasonR3BulkOutreach           = "r3_bulk_outreach"
	automationReasonR3ConfiguredForward      = "r3_configured_forward"
	automationReasonR4ArbitraryTarget        = "r4_arbitrary_target"
	automationReasonR4ForbiddenCapability    = "r4_forbidden_capability"
	automationReasonManualReview             = "manual_side_effect_review"
	automationReasonR1Disabled               = "r1_auto_approve_disabled"
	automationReasonR1ToolNotAllowed         = "r1_tool_not_allowlisted"
	automationReasonR1Confidence             = "r1_confidence_below_threshold"
	automationReasonR2Disabled               = "r2_auto_approve_disabled"
	automationReasonR2IntentNotAllowed       = "r2_intent_not_allowlisted"
	automationReasonR2Confidence             = "r2_confidence_below_threshold"
	automationReasonR2RunNotExclusive        = "r2_run_not_exclusive"
	automationReasonR2ScopeMissing           = "r2_audience_scope_missing"
	automationReasonR2ContentNotApproved     = "r2_content_not_approved"
	automationReasonR2ScopeExpired           = "r2_audience_scope_expired"
	automationReasonR2RecipientCap           = "r2_recipient_cap_exceeded"
)

var (
	automationForbiddenURLPattern  = regexp.MustCompile(`(?i)\b(?:https?|file)://`)
	automationForbiddenPathPattern = regexp.MustCompile(
		`(?i)(?:^|[\s"'(])(?:~[/\\]|/(?:Applications|Library|System|Users|Volumes|dev|etc|home|opt|private|proc|root|sys|tmp|usr|var)/|[a-z]:\\)`,
	)
	automationForbiddenShellPattern = regexp.MustCompile(
		`(?i)(?:\brm\s+-rf\b|\bsudo\s+|\b(?:bash|sh|zsh|cmd|powershell)\s+-[a-z]*c\b|\bcurl\b[^\n|]*\||\bwget\s+|\b(?:run|execute)\s+(?:[a-z0-9_.-]+/)?(?:bash|cat|chmod|chown|kill|ls|nc|node|perl|python|ruby|sh|ssh|zsh)\b)`,
	)
	automationForbiddenSQLPattern = regexp.MustCompile(
		`(?i)\b(?:select\b.{0,160}\bfrom|drop|truncate|alter|delete\s+from|insert\s+into|update\s+\S+\s+set|create\s+(?:table|database|user)|grant|revoke)\b`,
	)
	automationForbiddenCredentialPattern = regexp.MustCompile(
		`(?i)\b(?:ask|confirm|enter|give|need|provide|reply\s+with|request|reveal|send|share|tell|upload|what(?:'s|\s+is))\b.{0,64}\b(?:password|passcode|credential|api[\s-]*key|access\s*token|private\s*key|verification\s*code|2fa\s*code|secret)\b`,
	)
	automationForbiddenCredentialValuePattern = regexp.MustCompile(
		`(?i)\b(?:passwords?|passcodes?|credentials?|api[\s-]*keys?|access\s*tokens?|private\s*keys?|verification\s*codes?|2fa\s*codes?|client\s*secrets?|secret\s*keys?)\b`,
	)
	automationForbiddenPaymentPattern = regexp.MustCompile(
		`(?i)\b(?:(?:make|request|submit)\b.{0,32}\bpayment|(?:pay|send|transfer|wire|remit)\b.{0,64}\b(?:fee|money|funds?|payment|usd|eur|rmb|cny|bitcoin|btc|eth|usdt)|bank\s+transfer|wallet\s+address)\b`,
	)
	automationForbiddenPaymentValuePattern = regexp.MustCompile(
		`(?i)\b(?:bank\s+account|routing\s+number|credit\s+card|debit\s+card|wallet\s+address|payment\s+link|payment\s+request|crypto(?:currency)?\s+address)\b`,
	)
	automationSensitiveCommitmentPattern = regexp.MustCompile(
		`(?i)\b(?:you(?:'re| are| have been)\s+hired|(?:we|company)\b.{0,48}\b(?:guarantee|hir(?:e|ed|ing)|promise)|(?:job\s+)?offer|application\b.{0,48}\b(?:archive|archived|rejected|not\s+moving\s+forward)|(?:archive|archived|reject|rejected)\b.{0,48}\b(?:application|candidate)|onboarding|compensation|salary|equity|bonus|legally\s+binding|non-disclosure|nda)\b`,
	)
)

type ServerAutomationRiskEvaluator struct {
	audiences AutomationAudienceResolver
	crm       *CRMStore
}

func NewServerAutomationRiskEvaluator(audiences ...AutomationAudienceResolver) *ServerAutomationRiskEvaluator {
	evaluator := &ServerAutomationRiskEvaluator{}
	if len(audiences) > 0 {
		evaluator.audiences = audiences[0]
	}
	return evaluator
}

func DefaultAutomationPolicy(now time.Time, mode AutomationMode) AutomationPolicy {
	return AutomationPolicy{
		Version:         1,
		Mode:            mode,
		R1AutoApprove:   true,
		R1MinConfidence: 0.90,
		R1AllowedTools: []AutomationToolName{
			AutomationToolCandidateCreate,
			AutomationToolCandidateUpdate,
			AutomationToolCandidateLink,
			AutomationToolJobCreate,
			AutomationToolLeadCreate,
			AutomationToolLeadTransition,
			AutomationToolTaskCreate,
			AutomationToolNoteCreate,
			AutomationToolJobUpdate,
			AutomationToolOnboardingCreate,
			AutomationToolOnboardingUpdate,
			AutomationToolFleetUpdate,
			AutomationToolServiceCreate,
			AutomationToolServiceUpdate,
			AutomationToolWorkbenchMutate,
		},
		R2PrivateReplyAutoApprove: false,
		R2MinConfidence:           0.98,
		R2AllowedIntents: []string{
			AutomationReplyIntentGeneral,
			AutomationReplyIntentScheduling,
			AutomationReplyIntentInformation,
		},
		UpdatedAt: now.UTC(),
	}
}

func ValidateAutomationPolicy(policy AutomationPolicy) error {
	if policy.Version == 0 {
		return NewAutomationValidationError("version", "must be greater than zero")
	}
	if !IsValidAutomationMode(policy.Mode) {
		return NewAutomationValidationError("mode", "must be manual or automatic")
	}
	if err := validatePolicyConfidence("r1_min_confidence", policy.R1MinConfidence); err != nil {
		return err
	}
	if err := validatePolicyConfidence("r2_min_confidence", policy.R2MinConfidence); err != nil {
		return err
	}
	if policy.R1AllowedTools == nil {
		return NewAutomationValidationError("r1_allowed_tools", "must not be null")
	}
	if len(policy.R1AllowedTools) > 32 {
		return NewAutomationValidationError("r1_allowed_tools", "must contain at most 32 tools")
	}
	seenTools := make(map[AutomationToolName]struct{}, len(policy.R1AllowedTools))
	for i, tool := range policy.R1AllowedTools {
		if !isR1PolicyTool(tool) {
			return NewAutomationValidationError(indexedField("r1_allowed_tools", i), "must be an R1-capable tool")
		}
		if _, exists := seenTools[tool]; exists {
			return NewAutomationValidationError(indexedField("r1_allowed_tools", i), "duplicates an earlier tool")
		}
		seenTools[tool] = struct{}{}
	}
	if policy.R2AllowedIntents == nil {
		return NewAutomationValidationError("r2_allowed_intents", "must not be null")
	}
	if len(policy.R2AllowedIntents) > 32 {
		return NewAutomationValidationError("r2_allowed_intents", "must contain at most 32 intents")
	}
	seenIntents := make(map[string]struct{}, len(policy.R2AllowedIntents))
	for i, intent := range policy.R2AllowedIntents {
		if !isR2PolicyIntent(intent) {
			return NewAutomationValidationError(indexedField("r2_allowed_intents", i), "must be a low-risk private reply intent")
		}
		if _, exists := seenIntents[intent]; exists {
			return NewAutomationValidationError(indexedField("r2_allowed_intents", i), "duplicates an earlier intent")
		}
		seenIntents[intent] = struct{}{}
	}
	return nil
}

func (e *ServerAutomationRiskEvaluator) Evaluate(policy AutomationPolicy, input AutomationPolicyInput) AutomationPolicyResult {
	if err := ValidateAutomationPolicy(policy); err != nil {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}
	if math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) ||
		input.Confidence < 0 || input.Confidence > 1 {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}

	if IsWorkbenchToolName(input.Tool) {
		decoded, err := DecodeWorkbenchToolRequest(input.Tool, input.Payload)
		if err != nil {
			return automationPolicyResult(
				AutomationRiskR4,
				AutomationDecisionDeny,
				nil,
				automationReasonR4ForbiddenCapability,
			)
		}
		return e.evaluateWorkbenchPolicy(policy, input, decoded)
	}

	arguments, err := DecodeAutomationToolArguments(input.Tool, input.Payload)
	if err != nil {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}

	switch input.Tool {
	case AutomationToolMessageClassify, AutomationToolJobMatch:
		return automationPolicyResult(
			AutomationRiskR0,
			AutomationDecisionAutoComplete,
			nil,
			automationReasonR0InformationOnly,
		)

	case AutomationToolCandidateUpdate:
		update := arguments.(CandidateUpdateArgs)
		if update.Patch.Status != nil &&
			(*update.Patch.Status == CandidateStatusPlaced || *update.Patch.Status == CandidateStatusArchived) {
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3TerminalCandidate,
			)
		}
		return evaluateR1Policy(policy, input)

	case AutomationToolJobUpdate:
		update := arguments.(JobUpdateArgs)
		if update.Patch.CompensationSummary != nil {
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3SensitiveCommitment,
			)
		}
		if update.Patch.Status != nil &&
			(*update.Patch.Status == JobStatusClosed || *update.Patch.Status == JobStatusArchived) {
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3TerminalLead,
			)
		}
		return evaluateR1Policy(policy, input)

	case AutomationToolLeadTransition:
		transition := arguments.(LeadTransitionArgs)
		switch transition.ToStage {
		case LeadStageScreening, LeadStageInterview:
			return evaluateR1Policy(policy, input)
		default:
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3TerminalLead,
			)
		}

	case AutomationToolCandidateCreate,
		AutomationToolCandidateLink,
		AutomationToolLeadCreate,
		AutomationToolTaskCreate,
		AutomationToolNoteCreate,
		AutomationToolOnboardingCreate,
		AutomationToolOnboardingUpdate,
		AutomationToolFleetUpdate:
		return evaluateR1Policy(policy, input)

	case AutomationToolJobCreate:
		create := arguments.(JobCreateArgs)
		if input.SourceMessage.ChatType != ChatTypeGroup ||
			input.SourceMessage.EventID != create.EventID ||
			input.SourceMessage.PeerID != create.GroupID {
			return automationPolicyResult(
				AutomationRiskR4,
				AutomationDecisionDeny,
				nil,
				automationReasonR4ArbitraryTarget,
			)
		}
		return evaluateR1Policy(policy, input)

	case AutomationToolServiceCreate:
		create := arguments.(ServiceRequestCreateArgs)
		if create.RequiresHuman || isSensitiveServiceCategory(create.Category) {
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3SensitiveCommitment,
			)
		}
		return evaluateR1Policy(policy, input)

	case AutomationToolServiceUpdate:
		update := arguments.(ServiceRequestUpdateArgs)
		if (update.Patch.RequiresHuman != nil && *update.Patch.RequiresHuman) ||
			(update.Patch.Category != nil && isSensitiveServiceCategory(*update.Patch.Category)) {
			return automationPolicyResult(
				AutomationRiskR3,
				AutomationDecisionReview,
				nil,
				automationReasonR3SensitiveCommitment,
			)
		}
		return evaluateR1Policy(policy, input)

	case AutomationToolFundCreate:
		return automationPolicyResult(
			AutomationRiskR3,
			AutomationDecisionReview,
			nil,
			automationReasonR3SensitiveCommitment,
		)

	case AutomationToolForwardEvent:
		return automationPolicyResult(
			AutomationRiskR3,
			AutomationDecisionReview,
			nil,
			automationReasonR3ConfiguredForward,
		)

	case AutomationToolReplyPrivateText:
		reply := arguments.(ReplyPrivateTextArgs)
		return evaluatePrivateReplyPolicy(policy, input, reply)

	case AutomationToolReplyAudienceText:
		reply := arguments.(ReplyAudienceTextArgs)
		return e.evaluateAudienceReplyPolicy(policy, input, reply)

	default:
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}
}

func (e *ServerAutomationRiskEvaluator) evaluateWorkbenchPolicy(
	policy AutomationPolicy,
	input AutomationPolicyInput,
	decoded WorkbenchDecodedToolRequest,
) AutomationPolicyResult {
	definition, ok := WorkbenchOperationDefinitionFor(decoded.Tool, decoded.Request.Operation)
	if !ok || !workbenchOperationEffectiveAvailable(definition) ||
		definition.PolicyResolver == nil {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}
	recipient, err := definition.PolicyResolver(e, input, decoded)
	if err != nil {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ArbitraryTarget,
		)
	}
	switch decoded.Tool {
	case AutomationToolWorkbenchQuery:
		return automationPolicyResult(
			AutomationRiskR0,
			AutomationDecisionAutoApprove,
			nil,
			automationReasonR0WorkbenchQuery,
		)
	case AutomationToolWorkbenchMutate:
		return evaluateR1Policy(policy, input)
	case AutomationToolWorkbenchAllowOnce:
		return automationPolicyResult(
			AutomationRiskR3,
			AutomationDecisionReview,
			recipient,
			automationReasonR3WorkbenchAllowOnce,
		)
	default:
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}
}

func workbenchDefaultPolicyResolver(
	evaluator *ServerAutomationRiskEvaluator,
	_ AutomationPolicyInput,
	decoded WorkbenchDecodedToolRequest,
) (*AutomationRecipient, error) {
	switch decoded.Request.Operation {
	case WorkbenchOperationMessagesSendPrivate, WorkbenchOperationMessagesSendGroup:
		if evaluator == nil {
			return nil, NewAutomationValidationError("recipient", "risk evaluator is unavailable")
		}
		return evaluator.workbenchEventRecipient(decoded)
	default:
		return nil, nil
	}
}

func (e *ServerAutomationRiskEvaluator) workbenchEventRecipient(
	decoded WorkbenchDecodedToolRequest,
) (*AutomationRecipient, error) {
	if e == nil || e.crm == nil {
		return nil, NewAutomationValidationError("recipient", "CRM store is unavailable")
	}
	arguments, ok := decoded.Arguments.(*WorkbenchControlledSendArguments)
	if !ok {
		return nil, NewAutomationValidationError("recipient", "controlled send arguments are invalid")
	}
	event, found := automationFindMessageEvent(e.crm.Snapshot(), arguments.EventID)
	if !found {
		return nil, NewAutomationValidationError("recipient.event_id", "message event was not found")
	}
	peerID := event.PeerID
	switch decoded.Request.Operation {
	case WorkbenchOperationMessagesSendPrivate:
		if event.ChatType != ChatTypePrivate {
			return nil, NewAutomationValidationError("recipient.event_id", "must reference a private message event")
		}
	case WorkbenchOperationMessagesSendGroup:
		if event.ChatType != ChatTypeGroup {
			return nil, NewAutomationValidationError("recipient.event_id", "must reference a group message event")
		}
		if strings.TrimSpace(event.GroupID) != "" {
			peerID = event.GroupID
		}
	default:
		return nil, NewAutomationValidationError("recipient.operation", "is not a controlled send")
	}
	if strings.TrimSpace(peerID) == "" {
		return nil, NewAutomationValidationError("recipient.peer_id", "is unavailable")
	}
	return &AutomationRecipient{
		AudienceID:      event.ID,
		AudienceVersion: event.Version,
		ChatType:        event.ChatType,
		PeerID:          peerID,
	}, nil
}

func (e *ServerAutomationRiskEvaluator) evaluateAudienceReplyPolicy(
	policy AutomationPolicy,
	input AutomationPolicyInput,
	reply ReplyAudienceTextArgs,
) AutomationPolicyResult {
	if e == nil || e.audiences == nil {
		return automationPolicyResult(AutomationRiskR4, AutomationDecisionDeny, nil, automationReasonR4ArbitraryTarget)
	}
	audience, ok := e.audiences.ResolveAutomationAudience(reply.AudienceID)
	if !ok || !audience.Enabled || !containsAutomationIntent(audience.AllowedIntents, reply.Intent) {
		return automationPolicyResult(AutomationRiskR4, AutomationDecisionDeny, nil, automationReasonR4ArbitraryTarget)
	}
	privateReply := ReplyPrivateTextArgs{Intent: reply.Intent, Text: reply.Text}
	if isForbiddenReply(privateReply) {
		return automationPolicyResult(AutomationRiskR4, AutomationDecisionDeny, nil, automationReasonR4ForbiddenCapability)
	}
	recipient := &AutomationRecipient{
		AudienceID:      audience.ID,
		AudienceVersion: audience.Version,
		ChatType:        audience.ChatType,
		PeerID:          audience.PeerID,
	}
	if isSensitiveReply(privateReply) {
		return automationPolicyResult(
			AutomationRiskR3,
			AutomationDecisionReview,
			recipient,
			automationReasonR3SensitiveCommitment,
		)
	}
	reasons := []string{automationReasonR2ConfiguredAudience}
	if input.SourceMessage.Suspicious {
		reasons = append(reasons, automationReasonSuspiciousExternalReview)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	switch {
	case audience.ApprovedContent == "" || audience.ContentSHA256 == "" ||
		audience.ExpiresAt == nil || audience.PerHourLimit < 1 || audience.PerRunRecipientCap < 1:
		reasons = append(reasons, automationReasonR2ScopeMissing)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	case audience.ContentSHA256 != automationPromptSHA256(normalizeAutomationAudienceContent(reply.Text)):
		reasons = append(reasons, automationReasonR2ContentNotApproved)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	case !audience.ExpiresAt.After(time.Now().UTC()):
		reasons = append(reasons, automationReasonR2ScopeExpired)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	case input.RunActionCount > audience.PerRunRecipientCap:
		reasons = append(reasons, automationReasonR2RecipientCap)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if policy.Mode == AutomationModeManual {
		reasons = append(reasons, automationReasonManualReview)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if !policy.R2PrivateReplyAutoApprove {
		reasons = append(reasons, automationReasonR2Disabled)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if !containsAutomationIntent(policy.R2AllowedIntents, reply.Intent) {
		reasons = append(reasons, automationReasonR2IntentNotAllowed)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if input.Confidence < policy.R2MinConfidence {
		reasons = append(reasons, automationReasonR2Confidence)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	return automationPolicyResult(AutomationRiskR2, AutomationDecisionAutoApprove, recipient, reasons...)
}

func evaluateR1Policy(policy AutomationPolicy, input AutomationPolicyInput) AutomationPolicyResult {
	reasons := []string{automationReasonR1InternalWrite}
	if policy.Mode == AutomationModeManual {
		reasons = append(reasons, automationReasonManualReview)
		return automationPolicyResult(AutomationRiskR1, AutomationDecisionReview, nil, reasons...)
	}
	if !policy.R1AutoApprove {
		reasons = append(reasons, automationReasonR1Disabled)
		return automationPolicyResult(AutomationRiskR1, AutomationDecisionReview, nil, reasons...)
	}
	if !containsAutomationTool(policy.R1AllowedTools, input.Tool) {
		reasons = append(reasons, automationReasonR1ToolNotAllowed)
		return automationPolicyResult(AutomationRiskR1, AutomationDecisionReview, nil, reasons...)
	}
	if input.Confidence < policy.R1MinConfidence {
		reasons = append(reasons, automationReasonR1Confidence)
		return automationPolicyResult(AutomationRiskR1, AutomationDecisionReview, nil, reasons...)
	}
	return automationPolicyResult(AutomationRiskR1, AutomationDecisionAutoApprove, nil, reasons...)
}

func evaluatePrivateReplyPolicy(
	policy AutomationPolicy,
	input AutomationPolicyInput,
	reply ReplyPrivateTextArgs,
) AutomationPolicyResult {
	peerID := strings.TrimSpace(input.SourceMessage.PeerID)
	if input.SourceMessage.ChatType != ChatTypePrivate ||
		peerID == "" ||
		peerID != input.SourceMessage.PeerID {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ArbitraryTarget,
		)
	}
	if isForbiddenReply(reply) {
		return automationPolicyResult(
			AutomationRiskR4,
			AutomationDecisionDeny,
			nil,
			automationReasonR4ForbiddenCapability,
		)
	}

	recipient := &AutomationRecipient{
		ChatType: ChatTypePrivate,
		PeerID:   input.SourceMessage.PeerID,
	}
	if isSensitiveReply(reply) {
		reasons := []string{automationReasonR3SensitiveCommitment}
		if input.SourceMessage.Suspicious {
			reasons = append(reasons, automationReasonSuspiciousExternalReview)
		}
		return automationPolicyResult(AutomationRiskR3, AutomationDecisionReview, recipient, reasons...)
	}

	// RunActionCount includes R0/R1 actions and cannot establish bulk outreach.
	// task-004/task-010 must provide an explicit external action count in a later interface update.
	reasons := []string{automationReasonR2OriginalPrivateText}
	if input.SourceMessage.Suspicious {
		reasons = append(reasons, automationReasonSuspiciousExternalReview)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if policy.Mode == AutomationModeManual {
		reasons = append(reasons, automationReasonManualReview)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if !policy.R2PrivateReplyAutoApprove {
		reasons = append(reasons, automationReasonR2Disabled)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if !containsAutomationIntent(policy.R2AllowedIntents, reply.Intent) {
		reasons = append(reasons, automationReasonR2IntentNotAllowed)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	if input.Confidence < policy.R2MinConfidence {
		reasons = append(reasons, automationReasonR2Confidence)
		return automationPolicyResult(AutomationRiskR2, AutomationDecisionReview, recipient, reasons...)
	}
	return automationPolicyResult(AutomationRiskR2, AutomationDecisionAutoApprove, recipient, reasons...)
}

func automationPolicyResult(
	risk AutomationRisk,
	decision AutomationPolicyDecision,
	recipient *AutomationRecipient,
	reasons ...string,
) AutomationPolicyResult {
	return AutomationPolicyResult{
		Risk:        risk,
		Decision:    decision,
		RiskReasons: append([]string(nil), reasons...),
		Recipient:   recipient,
	}
}

func validatePolicyConfidence(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return NewAutomationValidationError(field, "must be finite and between 0 and 1")
	}
	return nil
}

func isR1PolicyTool(tool AutomationToolName) bool {
	switch tool {
	case AutomationToolCandidateCreate,
		AutomationToolCandidateUpdate,
		AutomationToolCandidateLink,
		AutomationToolJobCreate,
		AutomationToolLeadCreate,
		AutomationToolLeadTransition,
		AutomationToolTaskCreate,
		AutomationToolNoteCreate,
		AutomationToolJobUpdate,
		AutomationToolOnboardingCreate,
		AutomationToolOnboardingUpdate,
		AutomationToolFleetUpdate,
		AutomationToolServiceCreate,
		AutomationToolServiceUpdate,
		AutomationToolWorkbenchMutate:
		return true
	default:
		return false
	}
}

func isSensitiveServiceCategory(category ServiceRequestCategory) bool {
	switch category {
	case ServiceRequestCategorySafety, ServiceRequestCategoryDispute, ServiceRequestCategoryFund:
		return true
	default:
		return false
	}
}

func isR2PolicyIntent(intent string) bool {
	switch intent {
	case AutomationReplyIntentGeneral,
		AutomationReplyIntentScheduling,
		AutomationReplyIntentInformation:
		return true
	default:
		return false
	}
}

func containsAutomationTool(tools []AutomationToolName, target AutomationToolName) bool {
	for _, tool := range tools {
		if tool == target {
			return true
		}
	}
	return false
}

func containsAutomationIntent(intents []string, target string) bool {
	for _, intent := range intents {
		if intent == target {
			return true
		}
	}
	return false
}

func isForbiddenReply(reply ReplyPrivateTextArgs) bool {
	switch reply.Intent {
	case AutomationReplyIntentPaymentRequest, AutomationReplyIntentCredentialRequest:
		return true
	}
	text := reply.Text
	return automationForbiddenURLPattern.MatchString(text) ||
		automationForbiddenPathPattern.MatchString(text) ||
		automationForbiddenShellPattern.MatchString(text) ||
		automationForbiddenSQLPattern.MatchString(text) ||
		automationForbiddenCredentialPattern.MatchString(text) ||
		automationForbiddenPaymentPattern.MatchString(text)
}

func isSensitiveReply(reply ReplyPrivateTextArgs) bool {
	switch reply.Intent {
	case AutomationReplyIntentOffer,
		AutomationReplyIntentRejection,
		AutomationReplyIntentHiring,
		AutomationReplyIntentOnboarding,
		AutomationReplyIntentCompensationCommitment,
		AutomationReplyIntentLegalCommitment:
		return true
	}
	return automationSensitiveCommitmentPattern.MatchString(reply.Text)
}
