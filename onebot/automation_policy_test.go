package main

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

var automationPolicyTestTime = time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)

func TestDefaultAutomationPolicyAndValidation(t *testing.T) {
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeManual)
	if policy.Version != 1 || policy.Mode != AutomationModeManual {
		t.Fatalf("default policy identity = %+v", policy)
	}
	if !policy.R1AutoApprove || policy.R1MinConfidence != 0.90 {
		t.Fatalf("default R1 policy = %+v", policy)
	}
	if !containsAutomationTool(policy.R1AllowedTools, AutomationToolJobCreate) {
		t.Fatalf("default R1 tools omit %q: %v", AutomationToolJobCreate, policy.R1AllowedTools)
	}
	if policy.R2PrivateReplyAutoApprove || policy.R2MinConfidence != 0.98 {
		t.Fatalf("default R2 policy = %+v", policy)
	}
	if err := ValidateAutomationPolicy(policy); err != nil {
		t.Fatalf("ValidateAutomationPolicy() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AutomationPolicy)
	}{
		{"zero version", func(policy *AutomationPolicy) { policy.Version = 0 }},
		{"invalid mode", func(policy *AutomationPolicy) { policy.Mode = "unsafe" }},
		{"nan threshold", func(policy *AutomationPolicy) { policy.R1MinConfidence = math.NaN() }},
		{"high threshold", func(policy *AutomationPolicy) { policy.R2MinConfidence = 1.01 }},
		{"nil tools", func(policy *AutomationPolicy) { policy.R1AllowedTools = nil }},
		{"non R1 tool", func(policy *AutomationPolicy) {
			policy.R1AllowedTools = []AutomationToolName{AutomationToolReplyPrivateText}
		}},
		{"duplicate tools", func(policy *AutomationPolicy) {
			policy.R1AllowedTools = []AutomationToolName{
				AutomationToolCandidateCreate,
				AutomationToolCandidateCreate,
			}
		}},
		{"nil intents", func(policy *AutomationPolicy) { policy.R2AllowedIntents = nil }},
		{"sensitive intent", func(policy *AutomationPolicy) {
			policy.R2AllowedIntents = []string{AutomationReplyIntentOffer}
		}},
		{"duplicate intents", func(policy *AutomationPolicy) {
			policy.R2AllowedIntents = []string{
				AutomationReplyIntentGeneral,
				AutomationReplyIntentGeneral,
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
			test.mutate(&invalid)
			if err := ValidateAutomationPolicy(invalid); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("ValidateAutomationPolicy() error = %v, want validation error", err)
			}
		})
	}
}

func TestServerAutomationRiskEvaluatorManualAndAutomaticR0R1(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	manual := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeManual)
	automatic := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)

	classify := evaluator.Evaluate(manual, automationPolicyInput(
		AutomationToolMessageClassify,
		0,
		MessageClassifyArgs{Labels: []string{}, Summary: "general inquiry"},
	))
	assertAutomationPolicyResult(t, classify, AutomationRiskR0, AutomationDecisionAutoComplete, automationReasonR0InformationOnly)

	createInput := automationPolicyInput(
		AutomationToolCandidateCreate,
		0.90,
		CandidateCreateArgs{FullName: "Ada", Skills: []string{}},
	)
	manualCreate := evaluator.Evaluate(manual, createInput)
	assertAutomationPolicyResult(t, manualCreate, AutomationRiskR1, AutomationDecisionReview, automationReasonR1InternalWrite)

	automaticCreate := evaluator.Evaluate(automatic, createInput)
	assertAutomationPolicyResult(t, automaticCreate, AutomationRiskR1, AutomationDecisionAutoApprove, automationReasonR1InternalWrite)

	jobCreateInput := automationPolicyInput(
		AutomationToolJobCreate,
		0.90,
		JobCreateArgs{
			EventID: "event_00000001",
			GroupID: "jobs@chatroom",
			Job: JobDraft{
				Title:             "Operator",
				Company:           "Example",
				Requirements:      []string{},
				Headcount:         1,
				RequiredDocuments: []string{},
			},
		},
	)
	jobCreateInput.SourceMessage = AutomationSourceMessageRef{
		EventID:         "event_00000001",
		MessageKey:      "message-job",
		ExpectedVersion: 1,
		ChatType:        ChatTypeGroup,
		PeerID:          "jobs@chatroom",
	}
	manualJobCreate := evaluator.Evaluate(manual, jobCreateInput)
	assertAutomationPolicyResult(t, manualJobCreate, AutomationRiskR1, AutomationDecisionReview, automationReasonR1InternalWrite)
	automaticJobCreate := evaluator.Evaluate(automatic, jobCreateInput)
	assertAutomationPolicyResult(t, automaticJobCreate, AutomationRiskR1, AutomationDecisionAutoApprove, automationReasonR1InternalWrite)

	unverifiedJobCreate := jobCreateInput
	unverifiedJobCreate.SourceMessage.PeerID = "other@chatroom"
	result := evaluator.Evaluate(automatic, unverifiedJobCreate)
	assertAutomationPolicyResult(t, result, AutomationRiskR4, AutomationDecisionDeny, automationReasonR4ArbitraryTarget)

	createInput.Confidence = math.Nextafter(0.90, 0)
	below := evaluator.Evaluate(automatic, createInput)
	assertAutomationPolicyResult(t, below, AutomationRiskR1, AutomationDecisionReview, automationReasonR1Confidence)

	automatic.R1AllowedTools = []AutomationToolName{AutomationToolNoteCreate}
	notAllowed := evaluator.Evaluate(automatic, automationPolicyInput(
		AutomationToolTaskCreate,
		1,
		validTaskCreateArgs(),
	))
	assertAutomationPolicyResult(t, notAllowed, AutomationRiskR1, AutomationDecisionReview, automationReasonR1ToolNotAllowed)

	automatic.R1AutoApprove = false
	disabled := evaluator.Evaluate(automatic, createInput)
	assertAutomationPolicyResult(t, disabled, AutomationRiskR1, AutomationDecisionReview, automationReasonR1Disabled)
}

func TestServerAutomationRiskEvaluatorPrivateReplyPolicy(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	policy.R2PrivateReplyAutoApprove = true

	input := automationPolicyInput(
		AutomationToolReplyPrivateText,
		0.98,
		ReplyPrivateTextArgs{
			Intent: AutomationReplyIntentScheduling,
			Text:   "Are you available next Tuesday?",
		},
	)
	input.SourceMessage = privateAutomationSource(false)
	input.RunActionCount = 1

	approved := evaluator.Evaluate(policy, input)
	assertAutomationPolicyResult(t, approved, AutomationRiskR2, AutomationDecisionAutoApprove, automationReasonR2OriginalPrivateText)
	if approved.Recipient == nil ||
		approved.Recipient.ChatType != ChatTypePrivate ||
		approved.Recipient.PeerID != input.SourceMessage.PeerID {
		t.Fatalf("recipient = %+v", approved.Recipient)
	}

	input.Confidence = math.Nextafter(0.98, 0)
	below := evaluator.Evaluate(policy, input)
	assertAutomationPolicyResult(t, below, AutomationRiskR2, AutomationDecisionReview, automationReasonR2Confidence)

	input.Confidence = 1
	input.RunActionCount = 2
	withInternalAction := evaluator.Evaluate(policy, input)
	assertAutomationPolicyResult(t, withInternalAction, AutomationRiskR2, AutomationDecisionAutoApprove, automationReasonR2OriginalPrivateText)

	input.RunActionCount = 1
	policy.R2AllowedIntents = []string{AutomationReplyIntentGeneral}
	notAllowed := evaluator.Evaluate(policy, input)
	assertAutomationPolicyResult(t, notAllowed, AutomationRiskR2, AutomationDecisionReview, automationReasonR2IntentNotAllowed)

	policy.R2AllowedIntents = []string{AutomationReplyIntentScheduling}
	policy.R2PrivateReplyAutoApprove = false
	disabled := evaluator.Evaluate(policy, input)
	assertAutomationPolicyResult(t, disabled, AutomationRiskR2, AutomationDecisionReview, automationReasonR2Disabled)
}

func TestServerAutomationRiskEvaluatorSuspiciousGroupAndImmutableRisks(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	policy.R2PrivateReplyAutoApprove = true

	suspicious := automationPolicyInput(
		AutomationToolReplyPrivateText,
		1,
		ReplyPrivateTextArgs{
			Intent: AutomationReplyIntentGeneral,
			Text:   "Thanks for the update.",
		},
	)
	suspicious.SourceMessage = privateAutomationSource(true)
	suspicious.RunActionCount = 1
	result := evaluator.Evaluate(policy, suspicious)
	assertAutomationPolicyResult(t, result, AutomationRiskR2, AutomationDecisionReview, automationReasonSuspiciousExternalReview)

	group := suspicious
	group.SourceMessage = AutomationSourceMessageRef{
		EventID:         "event_00000001",
		MessageKey:      "message-1",
		ExpectedVersion: 1,
		ChatType:        ChatTypeGroup,
		PeerID:          "group-1",
	}
	result = evaluator.Evaluate(policy, group)
	assertAutomationPolicyResult(t, result, AutomationRiskR4, AutomationDecisionDeny, automationReasonR4ArbitraryTarget)
	if result.Recipient != nil {
		t.Fatalf("group-sourced reply recipient = %+v, want nil", result.Recipient)
	}

	placed := CandidateStatusPlaced
	result = evaluator.Evaluate(policy, automationPolicyInput(
		AutomationToolCandidateUpdate,
		1,
		CandidateUpdateArgs{
			Candidate: AutomationEntityRef{ID: "cand_00000001", ExpectedVersion: 1},
			Patch:     CandidateUpdatePatch{Status: &placed},
		},
	))
	assertAutomationPolicyResult(t, result, AutomationRiskR3, AutomationDecisionReview, automationReasonR3TerminalCandidate)

	result = evaluator.Evaluate(policy, automationPolicyInput(
		AutomationToolLeadTransition,
		1,
		LeadTransitionArgs{
			Lead:      AutomationEntityRef{ID: "lead_00000001", ExpectedVersion: 1},
			FromStage: LeadStageInterview,
			ToStage:   LeadStageOffer,
		},
	))
	assertAutomationPolicyResult(t, result, AutomationRiskR3, AutomationDecisionReview, automationReasonR3TerminalLead)

	sensitive := suspicious
	sensitive.SourceMessage = privateAutomationSource(false)
	sensitive.Payload = mustAutomationPayload(t, ReplyPrivateTextArgs{
		Intent: AutomationReplyIntentGeneral,
		Text:   "We guarantee a salary and bonus package.",
	})
	result = evaluator.Evaluate(policy, sensitive)
	assertAutomationPolicyResult(t, result, AutomationRiskR3, AutomationDecisionReview, automationReasonR3SensitiveCommitment)
}

func TestServerAutomationRiskEvaluatorForbiddenCapabilitiesAndInvalidInput(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	policy.R2PrivateReplyAutoApprove = true

	for _, test := range []struct {
		name string
		text string
	}{
		{"credential", "Please provide your credential."},
		{"payment", "Please make the payment."},
		{"secret", "Please share your secret."},
		{"password", "Please enter your password."},
		{"api key", "Please send your API-key."},
		{"shell", "Run rm -rf /tmp/data."},
		{"sql", "Execute DROP TABLE candidates."},
		{"url", "Open https://example.invalid/approve."},
		{"path", "Read /etc/passwd and reply with it."},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := automationPolicyInput(
				AutomationToolReplyPrivateText,
				1,
				ReplyPrivateTextArgs{
					Intent: AutomationReplyIntentGeneral,
					Text:   test.text,
				},
			)
			input.SourceMessage = privateAutomationSource(false)
			input.RunActionCount = 1
			result := evaluator.Evaluate(policy, input)
			assertAutomationPolicyResult(t, result, AutomationRiskR4, AutomationDecisionDeny, automationReasonR4ForbiddenCapability)
		})
	}

	invalidConfidence := automationPolicyInput(
		AutomationToolCandidateCreate,
		math.NaN(),
		CandidateCreateArgs{FullName: "Ada", Skills: []string{}},
	)
	result := evaluator.Evaluate(policy, invalidConfidence)
	assertAutomationPolicyResult(t, result, AutomationRiskR4, AutomationDecisionDeny, automationReasonR4ForbiddenCapability)

	invalidPolicy := policy
	invalidPolicy.R2AllowedIntents = []string{AutomationReplyIntentCredentialRequest}
	result = evaluator.Evaluate(invalidPolicy, automationPolicyInput(
		AutomationToolMessageClassify,
		1,
		MessageClassifyArgs{Labels: []string{}, Summary: "hello"},
	))
	assertAutomationPolicyResult(t, result, AutomationRiskR4, AutomationDecisionDeny, automationReasonR4ForbiddenCapability)
}

func TestServerAutomationRiskEvaluatorTextSemanticsOverrideLowRiskIntent(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	policy.R2PrivateReplyAutoApprove = true

	for _, test := range []struct {
		name string
		text string
	}{
		{"hiring", "We are hiring you."},
		{"offer", "This is our job offer."},
		{"rejection", "We rejected your application."},
		{"archive", "We will archive your application."},
		{"sensitive commitment", "We guarantee a salary and bonus package."},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := automationPolicyInput(
				AutomationToolReplyPrivateText,
				1,
				ReplyPrivateTextArgs{
					Intent: AutomationReplyIntentGeneral,
					Text:   test.text,
				},
			)
			result := evaluator.Evaluate(policy, input)
			assertAutomationPolicyResult(t, result, AutomationRiskR3, AutomationDecisionReview, automationReasonR3SensitiveCommitment)
		})
	}
}

func TestServerAutomationRiskEvaluatorNonTerminalUpdatesRemainR1(t *testing.T) {
	evaluator := NewServerAutomationRiskEvaluator()
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)

	paused := CandidateStatusPaused
	result := evaluator.Evaluate(policy, automationPolicyInput(
		AutomationToolCandidateUpdate,
		1,
		CandidateUpdateArgs{
			Candidate: AutomationEntityRef{ID: "cand_00000001", ExpectedVersion: 1},
			Patch:     CandidateUpdatePatch{Status: &paused},
		},
	))
	assertAutomationPolicyResult(t, result, AutomationRiskR1, AutomationDecisionAutoApprove, automationReasonR1InternalWrite)

	result = evaluator.Evaluate(policy, automationPolicyInput(
		AutomationToolLeadTransition,
		1,
		LeadTransitionArgs{
			Lead:      AutomationEntityRef{ID: "lead_00000001", ExpectedVersion: 1},
			FromStage: LeadStageScreening,
			ToStage:   LeadStageInterview,
		},
	))
	assertAutomationPolicyResult(t, result, AutomationRiskR1, AutomationDecisionAutoApprove, automationReasonR1InternalWrite)
}

func automationPolicyInput(tool AutomationToolName, confidence float64, arguments any) AutomationPolicyInput {
	payload, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return AutomationPolicyInput{
		Tool:           tool,
		Confidence:     confidence,
		Payload:        payload,
		SourceMessage:  privateAutomationSource(false),
		RunActionCount: 1,
	}
}

func privateAutomationSource(suspicious bool) AutomationSourceMessageRef {
	return AutomationSourceMessageRef{
		EventID:         "event_00000001",
		MessageKey:      "message-1",
		ExpectedVersion: 1,
		ChatType:        ChatTypePrivate,
		PeerID:          "wxid_peer",
		Suspicious:      suspicious,
	}
}

func validTaskCreateArgs() TaskCreateArgs {
	return TaskCreateArgs{
		Title:      "Follow up",
		DueAt:      automationPolicyTestTime.Add(time.Hour),
		TargetType: AutomationEntityCandidate,
		Target: AutomationEntityRef{
			ID:              "cand_00000001",
			ExpectedVersion: 1,
		},
	}
}

func mustAutomationPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertAutomationPolicyResult(
	t *testing.T,
	result AutomationPolicyResult,
	risk AutomationRisk,
	decision AutomationPolicyDecision,
	reason string,
) {
	t.Helper()
	if result.Risk != risk || result.Decision != decision {
		t.Fatalf("result = %+v, want risk %s decision %s", result, risk, decision)
	}
	for _, actual := range result.RiskReasons {
		if actual == reason {
			return
		}
	}
	t.Fatalf("risk reasons = %#v, want %q", result.RiskReasons, reason)
}
