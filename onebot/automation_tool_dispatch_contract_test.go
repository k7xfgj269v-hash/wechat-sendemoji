package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeWorkbenchToolRequestCanonicalizesTwoStages(t *testing.T) {
	raw := workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `{
		"query":"  Ada  ",
		"limit":1,
		"offset":0
	}`)
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolRequest() error = %v", err)
	}
	if decoded.Tool != AutomationToolWorkbenchQuery ||
		decoded.Request.Operation != WorkbenchOperationCandidatesList {
		t.Fatalf("decoded identity = %+v", decoded)
	}
	arguments, ok := decoded.Arguments.(*WorkbenchCandidateListArguments)
	if !ok {
		t.Fatalf("decoded arguments type = %T", decoded.Arguments)
	}
	if arguments.Limit != 1 || arguments.Offset != 0 {
		t.Fatalf("decoded pagination = %+v", arguments)
	}
	if !json.Valid(decoded.CanonicalArguments) || !json.Valid(decoded.CanonicalRequest) {
		t.Fatal("canonical JSON is invalid")
	}
	if decoded.Request.ArgumentsJSON != string(decoded.CanonicalArguments) {
		t.Fatalf("request arguments_json = %q, canonical = %s", decoded.Request.ArgumentsJSON, decoded.CanonicalArguments)
	}

	value, err := DecodeAutomationToolArguments(AutomationToolWorkbenchQuery, raw)
	if err != nil {
		t.Fatalf("DecodeAutomationToolArguments() error = %v", err)
	}
	request, ok := value.(WorkbenchToolRequest)
	if !ok {
		t.Fatalf("wrapper decode type = %T", value)
	}
	if request != decoded.Request {
		t.Fatalf("wrapper request = %+v, want %+v", request, decoded.Request)
	}
}

func TestDecodeWorkbenchToolRequestRejectsMalformedOuterAndInnerJSON(t *testing.T) {
	oversized := `{"query":"` + strings.Repeat("x", WorkbenchMaxArgumentsJSONBytes) + `","limit":1,"offset":0}`
	invalidUTF8 := append(
		[]byte(`{"operation":"candidates.list","arguments_json":"{\"query\":\"`),
		0xff,
	)
	invalidUTF8 = append(invalidUTF8, []byte(`\",\"limit\":1,\"offset\":0}"}`)...)
	tests := []struct {
		name string
		tool AutomationToolName
		raw  json.RawMessage
	}{
		{"unknown outer field", AutomationToolWorkbenchQuery, json.RawMessage(`{"operation":"candidates.list","arguments_json":"{\"limit\":1,\"offset\":0}","approval":true}`)},
		{"duplicate outer key", AutomationToolWorkbenchQuery, json.RawMessage(`{"operation":"candidates.list","operation":"jobs.list","arguments_json":"{\"limit\":1,\"offset\":0}"}`)},
		{"null arguments json", AutomationToolWorkbenchQuery, json.RawMessage(`{"operation":"candidates.list","arguments_json":null}`)},
		{"inner null", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `null`)},
		{"inner array", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `[]`)},
		{"inner trailing", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `{"limit":1,"offset":0} {}`)},
		{"inner unknown field", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `{"limit":1,"offset":0,"policy":"allow"}`)},
		{"inner duplicate key", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `{"limit":1,"limit":2,"offset":0}`)},
		{"unknown operation", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperation("shell.exec"), `{}`)},
		{"cross tool operation", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesCreate, `{"candidate":{"full_name":"Ada","skills":[]}}`)},
		{"oversized arguments json", AutomationToolWorkbenchQuery, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, oversized)},
		{"invalid UTF-8", AutomationToolWorkbenchQuery, invalidUTF8},
		{"legacy tool", AutomationToolCandidateCreate, workbenchTestWrapper(t, WorkbenchOperationCandidatesList, `{"limit":1,"offset":0}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeWorkbenchToolRequest(test.tool, test.raw); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("DecodeWorkbenchToolRequest() error = %v, want validation error", err)
			}
		})
	}
}

func TestDecodeWorkbenchToolRequestEnforcesPaginationBoundaries(t *testing.T) {
	for _, fixture := range []string{
		`{}`,
		`{"limit":0}`,
		`{"offset":0}`,
	} {
		raw := workbenchTestWrapper(t, WorkbenchOperationCandidatesList, fixture)
		decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw)
		if err != nil {
			t.Fatalf("default pagination %s rejected: %v", fixture, err)
		}
		arguments := decoded.Arguments.(*WorkbenchCandidateListArguments)
		if arguments.Limit != WorkbenchDefaultPageLimit || arguments.Offset != 0 {
			t.Fatalf("default pagination %s = %+v", fixture, arguments)
		}
	}
	for _, limit := range []int{1, WorkbenchMaxPageLimit} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			raw := workbenchTestWrapper(
				t,
				WorkbenchOperationCandidatesList,
				fmt.Sprintf(`{"limit":%d,"offset":0}`, limit),
			)
			if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw); err != nil {
				t.Fatalf("valid pagination rejected: %v", err)
			}
		})
	}
	for _, fixture := range []string{
		`{"limit":101,"offset":0}`,
		`{"limit":1,"offset":-1}`,
	} {
		raw := workbenchTestWrapper(t, WorkbenchOperationCandidatesList, fixture)
		if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw); !errors.Is(err, ErrAutomationValidation) {
			t.Errorf("pagination %s error = %v, want validation error", fixture, err)
		}
	}
}

func TestDecodeWorkbenchToolRequestEnforcesCanonicalAutomationActionID(t *testing.T) {
	raw := workbenchTestWrapper(
		t,
		WorkbenchOperationAutomationActionsGet,
		`{"id":"aiact_00000001"}`,
	)
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw)
	if err != nil {
		t.Fatalf("canonical automation action id rejected: %v", err)
	}
	arguments, ok := decoded.Arguments.(*WorkbenchIDArguments)
	if !ok || arguments.ID != "aiact_00000001" {
		t.Fatalf("decoded arguments = %#v", decoded.Arguments)
	}

	legacy := workbenchTestWrapper(
		t,
		WorkbenchOperationAutomationActionsGet,
		`{"id":"aiaction_00000001"}`,
	)
	if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, legacy); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("legacy automation action id error = %v, want validation error", err)
	}
}

func TestWorkbenchOperationDecodersRejectTrustedFields(t *testing.T) {
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{
			"lead actor",
			AutomationToolWorkbenchMutate,
			WorkbenchOperationLeadsTransition,
			`{"lead_id":"lead_00000001","expected_version":1,"from_stage":"sourced","to_stage":"screening","actor":"model"}`,
		},
		{
			"note author",
			AutomationToolWorkbenchMutate,
			WorkbenchOperationNotesCreate,
			`{"target_type":"candidate","target_id":"cand_00000001","body":"note","author":"model"}`,
		},
		{
			"audit actor",
			AutomationToolWorkbenchQuery,
			WorkbenchOperationCRMAuditList,
			`{"actor":"operator","limit":1,"offset":0}`,
		},
		{
			"onboarding confirmed",
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationOnboardingsTransition,
			`{"onboarding_id":"onboard_00000001","expected_version":1,"to_status":"completed","confirmed":true}`,
		},
		{
			"frida pid",
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFridaInitialize,
			`{"wechat_pid":123}`,
		},
		{
			"message recipient",
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationMessagesSendPrivate,
			`{"event_id":"event_00000001","intent":"general","text":"hello","recipient":"wxid_other"}`,
		},
		{
			"fund operator",
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmCommitment,
			`{"id":"fund_00000001","expected_version":1,"operator_id":"model"}`,
		},
		{
			"approval flag",
			AutomationToolWorkbenchMutate,
			WorkbenchOperationCandidatesUpdate,
			`{"candidate_id":"cand_00000001","update":{"expected_version":1,"summary":"ok"},"approval":true}`,
		},
		{
			"raw path",
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFridaRescan,
			`{"script_path":"/tmp/inject.js"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := workbenchTestWrapper(t, test.operation, test.arguments)
			if _, err := DecodeWorkbenchToolRequest(test.tool, raw); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("trusted field error = %v, want validation error", err)
			}
		})
	}
}

func TestWorkbenchJobUpdatesSeparateOrdinaryAndHighImpactFields(t *testing.T) {
	ordinary := workbenchTestWrapper(t, WorkbenchOperationJobsUpdate, `{
		"job_id":"job_00000001",
		"update":{"expected_version":1,"title":"Operator"}
	}`)
	if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchMutate, ordinary); err != nil {
		t.Fatalf("ordinary job update rejected: %v", err)
	}
	for _, update := range []string{
		`{"expected_version":1,"status":"closed"}`,
		`{"expected_version":1,"compensation_summary":"Guaranteed payment"}`,
		`{"expected_version":1,"accommodation":"Guaranteed housing"}`,
	} {
		raw := workbenchTestWrapper(
			t,
			WorkbenchOperationJobsUpdate,
			`{"job_id":"job_00000001","update":`+update+`}`,
		)
		if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchMutate, raw); !errors.Is(err, ErrAutomationValidation) {
			t.Errorf("high-impact jobs.update %s error = %v", update, err)
		}
	}
	compensation := workbenchTestWrapper(t, WorkbenchOperationJobsUpdateCompensation, `{
		"job_id":"job_00000001",
		"expected_version":1,
		"compensation_summary":"Hourly rate"
	}`)
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchAllowOnce, compensation)
	if err != nil {
		t.Fatalf("jobs.update_compensation rejected: %v", err)
	}
	arguments := decoded.Arguments.(*WorkbenchJobCompensationArguments)
	if arguments.DomainUpdate().CompensationSummary == nil {
		t.Fatal("compensation conversion lost the update")
	}
}

func TestWorkbenchSafeDTOConversionsInjectTrustedIdentitySeparately(t *testing.T) {
	lead := WorkbenchLeadTransitionArguments{
		LeadID:          "lead_00000001",
		ExpectedVersion: 2,
		FromStage:       LeadStageSourced,
		ToStage:         LeadStageScreening,
		Reason:          "screen",
	}.DomainTransition("operator-1")
	if lead.Actor != "operator-1" || lead.ExpectedVersion != 2 {
		t.Fatalf("lead conversion = %+v", lead)
	}
	note := WorkbenchNoteCreateArguments{
		TargetType: NoteTargetCandidate,
		TargetID:   "cand_00000001",
		Body:       "note",
	}.DomainNote("operator-1")
	if note.Author != "operator-1" || note.Body != "note" {
		t.Fatalf("note conversion = %+v", note)
	}
}

func TestWorkbenchToolResultEnvelopeIsBoundedAndConsistent(t *testing.T) {
	result := NewWorkbenchToolSuccess(
		WorkbenchOperationCandidatesList,
		map[string]any{"items": []string{"a"}},
		&WorkbenchPage{Limit: 1, Offset: 0, Returned: 1, Total: 2, HasMore: true, NextOffset: 1},
	)
	if !result.OK {
		t.Fatalf("success result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal success result: %v", err)
	}
	decoded, err := DecodeWorkbenchToolResult(encoded)
	if err != nil || !decoded.OK {
		t.Fatalf("DecodeWorkbenchToolResult() = %+v, %v", decoded, err)
	}

	oversized := NewWorkbenchToolSuccess(
		WorkbenchOperationCandidatesGet,
		map[string]string{"value": strings.Repeat("x", WorkbenchMaxResultJSONBytes)},
		nil,
	)
	if oversized.OK || oversized.Error == nil || oversized.Error.Code != WorkbenchErrorResultTooLarge {
		t.Fatalf("oversized result = %+v", oversized)
	}
	for _, invalid := range []WorkbenchToolResult{
		{Operation: WorkbenchOperationCandidatesGet, OK: true, Error: &WorkbenchError{Code: WorkbenchErrorInternal, Message: "bad"}},
		{Operation: WorkbenchOperationCandidatesGet, OK: false},
		{Operation: WorkbenchOperationCandidatesGet, OK: false, Data: json.RawMessage(`{}`), Error: &WorkbenchError{Code: WorkbenchErrorInternal, Message: "bad"}},
		{Operation: "unknown", OK: true, Data: json.RawMessage(`{}`)},
	} {
		if err := ValidateWorkbenchToolResult(invalid); !errors.Is(err, ErrAutomationValidation) {
			t.Errorf("ValidateWorkbenchToolResult(%+v) error = %v", invalid, err)
		}
	}
}

func TestWorkbenchAllowOnceDecisionSetRejectsReusableGrants(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "")
	decisions := WorkbenchValidPermissionDecisions(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationMessagesSendPrivate,
	)
	if fmt.Sprint(decisions) != fmt.Sprint([]AutomationPermissionDecision{
		AutomationPermissionAllowOnce,
		AutomationPermissionDenyOnce,
	}) {
		t.Fatalf("allow_once decisions = %#v", decisions)
	}
	for _, forbidden := range []AutomationPermissionDecision{
		AutomationPermissionAllowRun,
		AutomationPermissionAllowPersistent,
		AutomationPermissionDenyPersistent,
	} {
		if containsWorkbenchDecision(decisions, forbidden) {
			t.Errorf("reusable decision %q exposed", forbidden)
		}
	}
}

func TestWorkbenchAllowOncePersistentOutboundGrantCanBeEnabled(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "1")
	outbound := WorkbenchValidPermissionDecisions(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationMessagesSendPrivate,
	)
	if !containsWorkbenchDecision(outbound, AutomationPermissionAllowPersistent) {
		t.Fatalf("persistent outbound decision missing: %#v", outbound)
	}

	nonOutbound := WorkbenchValidPermissionDecisions(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFridaRescan,
	)
	if containsWorkbenchDecision(nonOutbound, AutomationPermissionAllowPersistent) {
		t.Fatalf("persistent non-outbound decision exposed: %#v", nonOutbound)
	}
}

func TestLegacyAutomationToolArgumentDecodingRemainsCompatible(t *testing.T) {
	raw := json.RawMessage(`{"full_name":" Ada ","skills":["Go"]}`)
	value, err := DecodeAutomationToolArguments(AutomationToolCandidateCreate, raw)
	if err != nil {
		t.Fatalf("legacy DecodeAutomationToolArguments() error = %v", err)
	}
	arguments, ok := value.(CandidateCreateArgs)
	if !ok || arguments.FullName != "Ada" {
		t.Fatalf("legacy arguments = %#v", value)
	}
}

func workbenchTestWrapper(
	t *testing.T,
	operation WorkbenchOperation,
	argumentsJSON string,
) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: argumentsJSON,
	})
	if err != nil {
		t.Fatalf("marshal workbench wrapper: %v", err)
	}
	return raw
}
