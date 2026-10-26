package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDecodeAutomationToolEnvelopeSupportsEveryTool(t *testing.T) {
	calls := []any{
		automationTestCall("classify", AutomationToolMessageClassify, 0.98, []any{
			map[string]any{"source": "source_message", "field": "text", "excerpt": "backend role"},
		}, map[string]any{
			"labels":  []string{AutomationLabelCandidateInterest, AutomationLabelJobInquiry},
			"summary": " Candidate asks about a backend role. ",
		}),
		automationTestCall("candidate-create", AutomationToolCandidateCreate, 0.95, []any{}, map[string]any{
			"full_name": " Ada Lovelace ",
			"phone":     " 123 ",
			"email":     " ada@example.com ",
			"location":  " Berlin ",
			"skills":    []string{" Go ", "C"},
			"owner":     " recruiter ",
			"summary":   " Strong backend profile. ",
		}),
		automationTestCall("candidate-update", AutomationToolCandidateUpdate, 0.93, []any{}, map[string]any{
			"candidate": map[string]any{"from_call": "candidate-create"},
			"patch": map[string]any{
				"location": " Munich ",
				"skills":   []string{"Go", "Distributed Systems"},
				"status":   CandidateStatusPaused,
			},
		}),
		automationTestCall("candidate-link", AutomationToolCandidateLink, 0.91, []any{}, map[string]any{
			"candidate": map[string]any{"from_call": "candidate-update"},
		}),
		automationTestCall("job-match", AutomationToolJobMatch, 1, []any{}, map[string]any{
			"matches": []any{
				map[string]any{
					"job":       map[string]any{"id": "job_00000001", "expected_version": 3},
					"score":     1,
					"rationale": " Exact backend fit. ",
				},
			},
		}),
		automationTestCall("lead-create", AutomationToolLeadCreate, 0.92, []any{}, map[string]any{
			"candidate": map[string]any{"from_call": "candidate-create"},
			"job":       map[string]any{"id": "job_00000001", "expected_version": 3},
			"owner":     " recruiter ",
		}),
		automationTestCall("lead-transition", AutomationToolLeadTransition, 0.90, []any{}, map[string]any{
			"lead":       map[string]any{"from_call": "lead-create"},
			"from_stage": LeadStageSourced,
			"to_stage":   LeadStageScreening,
		}),
		automationTestCall("task-create", AutomationToolTaskCreate, 0.89, []any{}, map[string]any{
			"title":       " Schedule screen ",
			"description": " Contact the candidate. ",
			"owner":       " recruiter ",
			"due_at":      "2026-08-01T10:00:00Z",
			"target_type": AutomationEntityCandidate,
			"target":      map[string]any{"from_call": "candidate-create"},
		}),
		automationTestCall("note-create", AutomationToolNoteCreate, 0.88, []any{}, map[string]any{
			"target_type": NoteTargetLead,
			"target":      map[string]any{"from_call": "lead-create"},
			"author":      " recruiter ",
			"body":        " Candidate is available next week. ",
		}),
		automationTestCall("reply", AutomationToolReplyPrivateText, 0, []any{}, map[string]any{
			"intent": AutomationReplyIntentScheduling,
			"text":   " Are you available next Tuesday? ",
		}),
	}

	envelope, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, calls))
	if err != nil {
		t.Fatalf("DecodeAutomationToolEnvelope() error = %v", err)
	}
	if envelope.SchemaVersion != AutomationToolEnvelopeVersion {
		t.Fatalf("SchemaVersion = %q", envelope.SchemaVersion)
	}
	if len(envelope.Actions) != len(calls) {
		t.Fatalf("len(Actions) = %d, want %d", len(envelope.Actions), len(calls))
	}

	classifyValue, err := DecodeAutomationToolArguments(envelope.Actions[0].Tool, envelope.Actions[0].Arguments)
	if err != nil {
		t.Fatalf("decode classify arguments: %v", err)
	}
	classify := classifyValue.(MessageClassifyArgs)
	if classify.Summary != "Candidate asks about a backend role." {
		t.Fatalf("classify summary = %q", classify.Summary)
	}

	createValue, err := DecodeAutomationToolArguments(envelope.Actions[1].Tool, envelope.Actions[1].Arguments)
	if err != nil {
		t.Fatalf("decode candidate create arguments: %v", err)
	}
	create := createValue.(CandidateCreateArgs)
	if create.FullName != "Ada Lovelace" || create.Phone != "123" {
		t.Fatalf("candidate create was not normalized: %+v", create)
	}
	if len(create.Skills) != 2 || create.Skills[0] != "Go" {
		t.Fatalf("candidate skills = %#v", create.Skills)
	}

	references, err := AutomationToolCallReferences(envelope.Actions[5])
	if err != nil {
		t.Fatalf("AutomationToolCallReferences() error = %v", err)
	}
	if len(references) != 2 ||
		references[0].EntityType != AutomationEntityCandidate ||
		references[1].EntityType != AutomationEntityJob {
		t.Fatalf("lead references = %#v", references)
	}

	if err := ValidateAutomationEnvelopeEvidence(envelope, func(evidence AutomationEvidence) (string, bool) {
		if evidence.Source == "source_message" && evidence.Field == "text" {
			return "I am interested in the backend role.", true
		}
		return "", false
	}); err != nil {
		t.Fatalf("ValidateAutomationEnvelopeEvidence() error = %v", err)
	}
}

func TestAutomationToolEnvelopeSupportsGenericWorkbenchWrappers(t *testing.T) {
	data := automationTestEnvelopeJSON(t, []any{
		automationTestCall(
			"query",
			AutomationToolWorkbenchQuery,
			1,
			[]any{},
			map[string]any{
				"operation":      WorkbenchOperationCandidatesList,
				"arguments_json": `{"limit":1,"offset":0}`,
			},
		),
	})
	envelope, err := DecodeAutomationToolEnvelope(data)
	if err != nil {
		t.Fatalf("DecodeAutomationToolEnvelope() error = %v", err)
	}
	if len(envelope.Actions) != 1 || envelope.Actions[0].Tool != AutomationToolWorkbenchQuery {
		t.Fatalf("generic wrapper actions = %+v", envelope.Actions)
	}
	value, err := DecodeAutomationToolArguments(
		envelope.Actions[0].Tool,
		envelope.Actions[0].Arguments,
	)
	if err != nil {
		t.Fatalf("DecodeAutomationToolArguments() error = %v", err)
	}
	request, ok := value.(WorkbenchToolRequest)
	if !ok {
		t.Fatalf("generic wrapper arguments type = %T", value)
	}
	if request.Operation != WorkbenchOperationCandidatesList ||
		request.ArgumentsJSON != `{"limit":1,"offset":0}` {
		t.Fatalf("canonical generic wrapper = %+v", request)
	}
}

func TestAutomationToolDefinitionsKeepLegacyAndGenericContractsSeparate(t *testing.T) {
	definitions, err := AutomationToolDefinitions([]AutomationToolName{
		AutomationToolCandidateCreate,
		AutomationToolWorkbenchQuery,
	})
	if err != nil {
		t.Fatalf("AutomationToolDefinitions() error = %v", err)
	}
	if len(definitions) != 2 {
		t.Fatalf("definitions = %d, want 2", len(definitions))
	}
	for _, definition := range definitions {
		var schema map[string]any
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", definition.Name, err)
		}
		properties := schema["properties"].(map[string]any)
		switch definition.Name {
		case AutomationToolCandidateCreate:
			if properties["full_name"] == nil || properties["arguments_json"] != nil {
				t.Fatalf("legacy schema changed: %#v", properties)
			}
		case AutomationToolWorkbenchQuery:
			if len(properties) != 2 || properties["operation"] == nil || properties["arguments_json"] == nil {
				t.Fatalf("generic wrapper schema = %#v", properties)
			}
		default:
			t.Fatalf("unexpected definition %q", definition.Name)
		}
	}
}

func TestDecodeAutomationToolEnvelopeRejectsUnknownAndDuplicateFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown envelope field",
			raw:  `{"schema_version":"automation.actions.v1","actions":[],"risk":"R0"}`,
		},
		{
			name: "provider supplied action risk",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[],"arguments":{"labels":[],"summary":"ok"},"risk":"R0"}]}`,
		},
		{
			name: "provider supplied recipient",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"reply.private_text","confidence":1,"evidence":[],"arguments":{"intent":"general","text":"hello","recipient":"wxid_other"}}]}`,
		},
		{
			name: "provider supplied approval",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.create","confidence":1,"evidence":[],"arguments":{"full_name":"Ada","skills":[],"approved":true}}]}`,
		},
		{
			name: "unknown nested reference field",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.link","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","expected_version":1,"policy":"allow"}}}]}`,
		},
		{
			name: "unknown patch field",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.update","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","expected_version":1},"patch":{"salary":"100000"}}}]}`,
		},
		{
			name: "duplicate envelope key",
			raw:  `{"schema_version":"automation.actions.v1","schema_version":"automation.actions.v1","actions":[]}`,
		},
		{
			name: "duplicate action key",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"confidence":0,"evidence":[],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "unknown evidence field",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[{"source":"source_message","excerpt":"ok","recipient":"wxid_other"}],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "duplicate evidence key",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[{"source":"source_message","excerpt":"ok","excerpt":"again"}],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "duplicate nested key",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.link","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","id":"cand_00000002","expected_version":1}}}]}`,
		},
		{
			name: "unknown array item field",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.match","confidence":1,"evidence":[],"arguments":{"matches":[{"job":{"id":"job_00000001","expected_version":1},"score":1,"rationale":"fit","policy":"allow"}]}}]}`,
		},
		{
			name: "duplicate array item key",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.match","confidence":1,"evidence":[],"arguments":{"matches":[{"job":{"id":"job_00000001","expected_version":1},"score":1,"score":0,"rationale":"fit"}]}}]}`,
		},
		{
			name: "escaped duplicate key",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[],"arguments":{"summary":"ok","\u0073ummary":"again","labels":[]}}]}`,
		},
		{
			name: "trailing JSON",
			raw:  `{"schema_version":"automation.actions.v1","actions":[]} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationRequireRejected(t, []byte(test.raw))
		})
	}
}

func TestDecodeAutomationToolEnvelopeRejectsCaseVariantObjectKeysAtEveryLevel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "envelope exact plus case variant",
			raw:  `{"schema_version":"automation.actions.v1","actions":[],"Actions":[]}`,
		},
		{
			name: "action exact plus case variant",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"Confidence":0,"evidence":[],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "evidence mixed case alias",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[{"source":"source_message","Entity_ID":"cand_00000001","excerpt":"ok"}],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "arguments exact plus case variant",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.create","confidence":1,"evidence":[],"arguments":{"full_name":"Ada","skills":[],"owner":"first","Owner":"second"}}]}`,
		},
		{
			name: "nested reference mixed case alias",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.link","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","Expected_Version":1}}}]}`,
		},
		{
			name: "nested patch exact plus case variant",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.update","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","expected_version":1},"patch":{"owner":"first","Owner":"second"}}}]}`,
		},
		{
			name: "array element exact plus case variant",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.match","confidence":1,"evidence":[],"arguments":{"matches":[{"job":{"id":"job_00000001","expected_version":1},"score":1,"Score":0,"rationale":"fit"}]}}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationRequireRejected(t, []byte(test.raw))
		})
	}
}

func TestDecodeAutomationToolEnvelopeRejectsMalformedProviderBytes(t *testing.T) {
	valid := automationTestEnvelopeJSON(t, []any{})

	invalidUTF8 := []byte(`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[],"arguments":{"labels":[],"summary":"`)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}}]}`)...)

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "invalid UTF-8 inside string",
			data: invalidUTF8,
		},
		{
			name: "leading form feed",
			data: append([]byte{'\f'}, valid...),
		},
		{
			name: "trailing vertical tab",
			data: append(append([]byte{}, valid...), '\v'),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationRequireRejected(t, test.data)
		})
	}

	withJSONWhitespace := append([]byte(" \t\r\n"), valid...)
	withJSONWhitespace = append(withJSONWhitespace, '\n', '\r', '\t', ' ')
	if _, err := DecodeAutomationToolEnvelope(withJSONWhitespace); err != nil {
		t.Fatalf("valid surrounding JSON whitespace rejected: %v", err)
	}
}

func TestDecodeAutomationToolEnvelopeRejectsInvalidVersionsToolsAndRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unsupported schema",
			raw:  `{"schema_version":"automation.actions.v2","actions":[]}`,
		},
		{
			name: "missing schema",
			raw:  `{"actions":[]}`,
		},
		{
			name: "unsupported tool",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"shell.exec","confidence":1,"evidence":[],"arguments":{}}]}`,
		},
		{
			name: "missing confidence",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","evidence":[],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "missing zero score",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.match","confidence":1,"evidence":[],"arguments":{"matches":[{"job":{"id":"job_00000001","expected_version":1},"rationale":"fit"}]}}]}`,
		},
		{
			name: "empty call id",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":" ","tool":"message.classify","confidence":1,"evidence":[],"arguments":{"labels":[],"summary":"ok"}}]}`,
		},
		{
			name: "arguments are array",
			raw:  `{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[],"arguments":[]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationRequireRejected(t, []byte(test.raw))
		})
	}
}

func TestDecodeAutomationToolEnvelopeRejectsNullArrays(t *testing.T) {
	tests := []string{
		`{"schema_version":"automation.actions.v1","actions":null}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":null,"arguments":{"labels":[],"summary":"ok"}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"message.classify","confidence":1,"evidence":[],"arguments":{"labels":null,"summary":"ok"}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.create","confidence":1,"evidence":[],"arguments":{"full_name":"Ada","skills":null}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"candidate.update","confidence":1,"evidence":[],"arguments":{"candidate":{"id":"cand_00000001","expected_version":1},"patch":{"full_name":"Ada","skills":null}}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.match","confidence":1,"evidence":[],"arguments":{"matches":null}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.create","confidence":1,"evidence":[],"arguments":{"event_id":"event_00000001","group_id":"jobs@chatroom","job":{"title":"Operator","company":"Example","requirements":null,"headcount":1,"required_documents":[]}}}]}`,
		`{"schema_version":"automation.actions.v1","actions":[{"call_id":"x","tool":"job.create","confidence":1,"evidence":[],"arguments":{"event_id":"event_00000001","group_id":"jobs@chatroom","job":{"title":"Operator","company":"Example","requirements":[],"headcount":1,"required_documents":null}}}]}`,
	}

	for i, raw := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			automationRequireRejected(t, []byte(raw))
		})
	}
}

func TestAutomationJobCreateContractIsStrictBoundedAndProviderCompatible(t *testing.T) {
	arguments := map[string]any{
		"event_id": " event_00000001 ",
		"group_id": " jobs@chatroom ",
		"job": map[string]any{
			"title":                  " Operator ",
			"company":                " Example Manufacturing ",
			"location":               " Synthetic Park ",
			"employment_type":        " contract ",
			"description":            " Synthetic demand only. ",
			"requirements":           []string{" Go ", "assembly"},
			"demand_code":            " SYN-001 ",
			"site_name":              " Synthetic Site ",
			"onsite_contact":         " Synthetic Contact ",
			"headcount":              12,
			"priority":               JobPriorityHigh,
			"start_at":               "2026-08-01T00:00:00Z",
			"deadline_at":            "2026-08-02T00:00:00Z",
			"shift":                  " day ",
			"work_schedule":          " five synthetic days ",
			"compensation_summary":   " hourly compensation ",
			"accommodation":          " synthetic accommodation ",
			"transport_requirements": " synthetic shuttle ",
			"required_documents":     []string{" Passport ", "ID"},
			"owner":                  " synthetic_recruiter ",
			"status":                 JobStatusOpen,
		},
	}
	data := automationTestEnvelopeJSON(t, []any{
		automationTestCall("provider-job-create", AutomationToolJobCreate, 0.99, []any{}, arguments),
	})
	envelope, err := DecodeAutomationToolEnvelope(data)
	if err != nil {
		t.Fatalf("DecodeAutomationToolEnvelope() error = %v", err)
	}
	value, err := DecodeAutomationToolArguments(
		envelope.Actions[0].Tool,
		envelope.Actions[0].Arguments,
	)
	if err != nil {
		t.Fatalf("DecodeAutomationToolArguments() error = %v", err)
	}
	args := value.(JobCreateArgs)
	if args.EventID != "event_00000001" ||
		args.GroupID != "jobs@chatroom" ||
		args.Job.Title != "Operator" ||
		args.Job.Company != "Example Manufacturing" ||
		args.Job.Priority != JobPriorityHigh ||
		args.Job.Status != JobStatusOpen {
		t.Fatalf("normalized job.create arguments = %+v", args)
	}
	if fmt.Sprint(args.Job.Requirements) != fmt.Sprint([]string{"assembly", "Go"}) ||
		fmt.Sprint(args.Job.RequiredDocuments) != fmt.Sprint([]string{"ID", "Passport"}) {
		t.Fatalf("normalized job.create lists = %+v", args.Job)
	}

	definitions, err := AutomationToolDefinitions([]AutomationToolName{AutomationToolJobCreate})
	if err != nil {
		t.Fatalf("AutomationToolDefinitions() error = %v", err)
	}
	if len(definitions) != 1 ||
		definitions[0].Name != AutomationToolJobCreate ||
		!definitions[0].Strict ||
		definitions[0].Description == "" {
		t.Fatalf("job.create definition = %+v", definitions)
	}
	var schema map[string]any
	if err := json.Unmarshal(definitions[0].Parameters, &schema); err != nil {
		t.Fatalf("decode job.create schema: %v", err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("job.create schema is not closed: %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	jobSchema := properties["job"].(map[string]any)
	if jobSchema["additionalProperties"] != false {
		t.Fatalf("job.create job schema is not closed: %#v", jobSchema)
	}
	jobProperties := jobSchema["properties"].(map[string]any)
	for _, forbidden := range []string{
		"recipient", "recipients", "url", "sql", "shell", "credentials", "payment",
	} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("job.create schema exposes outer field %q", forbidden)
		}
		if _, exists := jobProperties[forbidden]; exists {
			t.Fatalf("job.create schema exposes job field %q", forbidden)
		}
	}

	providerOutput, err := NormalizeAutomationProviderOutput(
		AutomationProviderOutput{ToolCalls: envelope.Actions},
		[]AutomationToolName{AutomationToolJobCreate},
	)
	if err != nil {
		t.Fatalf("NormalizeAutomationProviderOutput() error = %v", err)
	}
	if len(providerOutput.ToolCalls) != 1 ||
		providerOutput.ToolCalls[0].CallID != "provider-job-create" ||
		providerOutput.ToolCalls[0].Tool != AutomationToolJobCreate {
		t.Fatalf("normalized provider job.create call = %+v", providerOutput.ToolCalls)
	}
}

func TestAutomationJobCreateContractRejectsForbiddenAndInvalidFields(t *testing.T) {
	validArguments := func() map[string]any {
		return map[string]any{
			"event_id": "event_00000001",
			"group_id": "jobs@chatroom",
			"job": map[string]any{
				"title":              "Operator",
				"company":            "Example Manufacturing",
				"requirements":       []string{},
				"headcount":          1,
				"required_documents": []string{},
			},
		}
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"recipient", func(arguments map[string]any) { arguments["recipient"] = "wxid_other" }},
		{"recipients", func(arguments map[string]any) { arguments["recipients"] = []string{"wxid_other"} }},
		{"unknown outer field", func(arguments map[string]any) { arguments["approval"] = true }},
		{"unknown job field", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["recipient"] = "wxid_other"
		}},
		{"url content", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["description"] = "Open https://example.invalid/job."
		}},
		{"sql content", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["description"] = "Execute DROP TABLE jobs."
		}},
		{"shell content", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["requirements"] = []string{"Run rm -rf /tmp/jobs."}
		}},
		{"credential content", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["required_documents"] = []string{"API key"}
		}},
		{"payment content", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["compensation_summary"] = "Make payment by bank transfer."
		}},
		{"missing company", func(arguments map[string]any) {
			delete(arguments["job"].(map[string]any), "company")
		}},
		{"invalid event", func(arguments map[string]any) { arguments["event_id"] = "event-external" }},
		{"invalid group", func(arguments map[string]any) { arguments["group_id"] = "wxid_private" }},
		{"oversized title", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["title"] = strings.Repeat("x", 161)
		}},
		{"headcount overflow", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["headcount"] = 1000001
		}},
		{"closed status", func(arguments map[string]any) {
			arguments["job"].(map[string]any)["status"] = JobStatusClosed
		}},
		{"deadline before start", func(arguments map[string]any) {
			job := arguments["job"].(map[string]any)
			job["start_at"] = "2026-08-02T00:00:00Z"
			job["deadline_at"] = "2026-08-01T00:00:00Z"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := validArguments()
			test.mutate(arguments)
			automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
				automationTestCall("job-create", AutomationToolJobCreate, 1, []any{}, arguments),
			}))
		})
	}
}

func TestDecodeAutomationToolEnvelopeAllowsForwardAndEarlierReferences(t *testing.T) {
	calls := []any{
		automationTestCall("link-before", AutomationToolCandidateLink, 1, []any{}, map[string]any{
			"candidate": map[string]any{"from_call": "create"},
		}),
		automationTestCall("create", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
			"full_name": "Ada",
			"skills":    []string{},
		}),
		automationTestCall("link-after", AutomationToolCandidateLink, 1, []any{}, map[string]any{
			"candidate": map[string]any{"from_call": "create"},
		}),
	}

	envelope, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, calls))
	if err != nil {
		t.Fatalf("DecodeAutomationToolEnvelope() error = %v", err)
	}
	if len(envelope.Actions) != len(calls) ||
		envelope.Actions[0].CallID != "link-before" ||
		envelope.Actions[1].CallID != "create" ||
		envelope.Actions[2].CallID != "link-after" {
		t.Fatalf("decoded call order = %+v", envelope.Actions)
	}
}

func TestDecodeAutomationToolEnvelopeEnforcesReferences(t *testing.T) {
	tests := []struct {
		name  string
		calls []any
		want  string
	}{
		{
			name: "existing reference missing version",
			calls: []any{
				automationTestCall("link", AutomationToolCandidateLink, 1, []any{}, map[string]any{
					"candidate": map[string]any{"id": "cand_00000001"},
				}),
			},
			want: "expected_version",
		},
		{
			name: "reference uses both modes",
			calls: []any{
				automationTestCall("create", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
					"full_name": "Ada",
					"skills":    []string{},
				}),
				automationTestCall("link", AutomationToolCandidateLink, 1, []any{}, map[string]any{
					"candidate": map[string]any{
						"id":               "cand_00000001",
						"expected_version": 1,
						"from_call":        "create",
					},
				}),
			},
			want: "must use exactly one reference mode",
		},
		{
			name: "call reference carries version",
			calls: []any{
				automationTestCall("create", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
					"full_name": "Ada",
					"skills":    []string{},
				}),
				automationTestCall("link", AutomationToolCandidateLink, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "create", "expected_version": 1},
				}),
			},
			want: "must be absent for a call reference",
		},
		{
			name: "wrong entity id prefix",
			calls: []any{
				automationTestCall("link", AutomationToolCandidateLink, 1, []any{}, map[string]any{
					"candidate": map[string]any{"id": "job_00000001", "expected_version": 1},
				}),
			},
			want: "does not match the expected entity type",
		},
		{
			name: "unknown call",
			calls: []any{
				automationTestCall("link", AutomationToolCandidateLink, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "missing"},
				}),
			},
			want: "must reference a call in the same provider response",
		},
		{
			name: "self reference",
			calls: []any{
				automationTestCall("lead", AutomationToolLeadTransition, 1, []any{}, map[string]any{
					"lead":       map[string]any{"from_call": "lead"},
					"from_stage": LeadStageSourced,
					"to_stage":   LeadStageScreening,
				}),
			},
			want: "must not reference its own call_id",
		},
		{
			name: "incompatible forward result",
			calls: []any{
				automationTestCall("transition", AutomationToolLeadTransition, 1, []any{}, map[string]any{
					"lead":       map[string]any{"from_call": "create"},
					"from_stage": LeadStageSourced,
					"to_stage":   LeadStageScreening,
				}),
				automationTestCall("create", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
					"full_name": "Ada",
					"skills":    []string{},
				}),
			},
			want: "does not produce a lead entity",
		},
		{
			name: "two call cycle",
			calls: []any{
				automationTestCall("first", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "second"},
					"patch":     map[string]any{"summary": "first"},
				}),
				automationTestCall("second", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "first"},
					"patch":     map[string]any{"summary": "second"},
				}),
			},
			want: "must not create a dependency cycle",
		},
		{
			name: "three call cycle",
			calls: []any{
				automationTestCall("first", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "second"},
					"patch":     map[string]any{"summary": "first"},
				}),
				automationTestCall("second", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "third"},
					"patch":     map[string]any{"summary": "second"},
				}),
				automationTestCall("third", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
					"candidate": map[string]any{"from_call": "first"},
					"patch":     map[string]any{"summary": "third"},
				}),
			},
			want: "must not create a dependency cycle",
		},
		{
			name: "duplicate call id",
			calls: []any{
				automationTestCall("same", AutomationToolMessageClassify, 1, []any{}, map[string]any{
					"labels":  []string{},
					"summary": "one",
				}),
				automationTestCall("same", AutomationToolMessageClassify, 1, []any{}, map[string]any{
					"labels":  []string{},
					"summary": "two",
				}),
			},
			want: "duplicates an earlier call_id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, test.calls))
			if !errors.Is(err, ErrAutomationValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeAutomationToolEnvelope() error = %v, want validation error containing %q", err, test.want)
			}
		})
	}
}

func TestNormalizeAutomationProviderOutputAllowsForwardAndEarlierReferences(t *testing.T) {
	calls := []AutomationToolCall{
		automationProviderTestCall(t, "link-before", AutomationToolCandidateLink, map[string]any{
			"candidate": map[string]any{"from_call": "create"},
		}),
		automationProviderTestCall(t, "create", AutomationToolCandidateCreate, map[string]any{
			"full_name": "Ada",
			"skills":    []string{},
		}),
		automationProviderTestCall(t, "link-after", AutomationToolCandidateLink, map[string]any{
			"candidate": map[string]any{"from_call": "create"},
		}),
	}

	output, err := NormalizeAutomationProviderOutput(
		AutomationProviderOutput{ToolCalls: calls},
		[]AutomationToolName{AutomationToolCandidateCreate, AutomationToolCandidateLink},
	)
	if err != nil {
		t.Fatalf("NormalizeAutomationProviderOutput() error = %v", err)
	}
	if len(output.ToolCalls) != len(calls) ||
		output.ToolCalls[0].CallID != "link-before" ||
		output.ToolCalls[1].CallID != "create" ||
		output.ToolCalls[2].CallID != "link-after" {
		t.Fatalf("normalized call order = %+v", output.ToolCalls)
	}
}

func TestNormalizeAutomationProviderOutputEnforcesReferences(t *testing.T) {
	tests := []struct {
		name  string
		calls []AutomationToolCall
		want  string
	}{
		{
			name: "unknown call",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "link", AutomationToolCandidateLink, map[string]any{
					"candidate": map[string]any{"from_call": "missing"},
				}),
			},
			want: "must reference a call in the same provider response",
		},
		{
			name: "duplicate call id",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "same", AutomationToolCandidateCreate, map[string]any{
					"full_name": "Ada",
					"skills":    []string{},
				}),
				automationProviderTestCall(t, "same", AutomationToolCandidateCreate, map[string]any{
					"full_name": "Grace",
					"skills":    []string{},
				}),
			},
			want: "duplicates an earlier call_id",
		},
		{
			name: "incompatible forward result",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "transition", AutomationToolLeadTransition, map[string]any{
					"lead":       map[string]any{"from_call": "create"},
					"from_stage": LeadStageSourced,
					"to_stage":   LeadStageScreening,
				}),
				automationProviderTestCall(t, "create", AutomationToolCandidateCreate, map[string]any{
					"full_name": "Ada",
					"skills":    []string{},
				}),
			},
			want: "does not produce a lead entity",
		},
		{
			name: "self reference",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "self", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "self"},
					"patch":     map[string]any{"summary": "self"},
				}),
			},
			want: "must not reference its own call_id",
		},
		{
			name: "two call cycle",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "first", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "second"},
					"patch":     map[string]any{"summary": "first"},
				}),
				automationProviderTestCall(t, "second", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "first"},
					"patch":     map[string]any{"summary": "second"},
				}),
			},
			want: "must not create a dependency cycle",
		},
		{
			name: "three call cycle",
			calls: []AutomationToolCall{
				automationProviderTestCall(t, "first", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "second"},
					"patch":     map[string]any{"summary": "first"},
				}),
				automationProviderTestCall(t, "second", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "third"},
					"patch":     map[string]any{"summary": "second"},
				}),
				automationProviderTestCall(t, "third", AutomationToolCandidateUpdate, map[string]any{
					"candidate": map[string]any{"from_call": "first"},
					"patch":     map[string]any{"summary": "third"},
				}),
			},
			want: "must not create a dependency cycle",
		},
	}
	allowedTools := []AutomationToolName{
		AutomationToolCandidateCreate,
		AutomationToolCandidateUpdate,
		AutomationToolCandidateLink,
		AutomationToolLeadTransition,
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeAutomationProviderOutput(
				AutomationProviderOutput{ToolCalls: test.calls},
				allowedTools,
			)
			if !errors.Is(err, ErrAutomationValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeAutomationProviderOutput() error = %v, want validation error containing %q", err, test.want)
			}
		})
	}
}

func TestAutomationEntityReferenceSchemaAllowsAnySameResponseCallOrder(t *testing.T) {
	schema, err := AutomationToolSchema(AutomationToolCandidateLink)
	if err != nil {
		t.Fatalf("AutomationToolSchema() error = %v", err)
	}
	if bytes.Contains(schema, []byte("earlier")) {
		t.Fatalf("entity reference schema still requires earlier calls: %s", schema)
	}
	if !bytes.Contains(schema, []byte("same provider response")) {
		t.Fatalf("entity reference schema does not describe same-response calls: %s", schema)
	}
}

func TestDecodeAutomationToolEnvelopeEnforcesToolEnumsAndLimits(t *testing.T) {
	tests := []struct {
		name string
		call any
	}{
		{
			name: "unknown classification label",
			call: automationTestCall("x", AutomationToolMessageClassify, 1, []any{}, map[string]any{
				"labels":  []string{"unknown"},
				"summary": "ok",
			}),
		},
		{
			name: "duplicate classification label",
			call: automationTestCall("x", AutomationToolMessageClassify, 1, []any{}, map[string]any{
				"labels":  []string{AutomationLabelGeneral, AutomationLabelGeneral},
				"summary": "ok",
			}),
		},
		{
			name: "too many classification labels",
			call: automationTestCall("x", AutomationToolMessageClassify, 1, []any{}, map[string]any{
				"labels": []string{
					AutomationLabelCandidateInterest,
					AutomationLabelJobInquiry,
					AutomationLabelAvailability,
					AutomationLabelCompensation,
					AutomationLabelInterview,
					AutomationLabelGeneral,
				},
				"summary": "ok",
			}),
		},
		{
			name: "empty candidate patch",
			call: automationTestCall("x", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
				"candidate": map[string]any{"id": "cand_00000001", "expected_version": 1},
				"patch":     map[string]any{},
			}),
		},
		{
			name: "invalid candidate status",
			call: automationTestCall("x", AutomationToolCandidateUpdate, 1, []any{}, map[string]any{
				"candidate": map[string]any{"id": "cand_00000001", "expected_version": 1},
				"patch":     map[string]any{"status": "deleted"},
			}),
		},
		{
			name: "invalid job score",
			call: automationTestCall("x", AutomationToolJobMatch, 1, []any{}, map[string]any{
				"matches": []any{
					map[string]any{
						"job":       map[string]any{"id": "job_00000001", "expected_version": 1},
						"score":     1.01,
						"rationale": "fit",
					},
				},
			}),
		},
		{
			name: "duplicate job match",
			call: automationTestCall("x", AutomationToolJobMatch, 1, []any{}, map[string]any{
				"matches": []any{
					map[string]any{
						"job":       map[string]any{"id": "job_00000001", "expected_version": 1},
						"score":     0,
						"rationale": "first",
					},
					map[string]any{
						"job":       map[string]any{"id": "job_00000001", "expected_version": 1},
						"score":     1,
						"rationale": "second",
					},
				},
			}),
		},
		{
			name: "invalid lead transition",
			call: automationTestCall("x", AutomationToolLeadTransition, 1, []any{}, map[string]any{
				"lead":       map[string]any{"id": "lead_00000001", "expected_version": 1},
				"from_stage": LeadStageSourced,
				"to_stage":   LeadStageOffer,
			}),
		},
		{
			name: "non UTC due time",
			call: automationTestCall("x", AutomationToolTaskCreate, 1, []any{}, map[string]any{
				"title":       "Follow up",
				"due_at":      "2026-08-01T10:00:00+02:00",
				"target_type": AutomationEntityCandidate,
				"target":      map[string]any{"id": "cand_00000001", "expected_version": 1},
			}),
		},
		{
			name: "task target type mismatch",
			call: automationTestCall("x", AutomationToolTaskCreate, 1, []any{}, map[string]any{
				"title":       "Follow up",
				"due_at":      "2026-08-01T10:00:00Z",
				"target_type": AutomationEntityLead,
				"target":      map[string]any{"id": "cand_00000001", "expected_version": 1},
			}),
		},
		{
			name: "invalid note target type",
			call: automationTestCall("x", AutomationToolNoteCreate, 1, []any{}, map[string]any{
				"target_type": "job",
				"target":      map[string]any{"id": "job_00000001", "expected_version": 1},
				"body":        "note",
			}),
		},
		{
			name: "invalid reply intent",
			call: automationTestCall("x", AutomationToolReplyPrivateText, 1, []any{}, map[string]any{
				"intent": "send_money",
				"text":   "hello",
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{test.call}))
		})
	}
}

func TestDecodeAutomationToolEnvelopeAcceptsAndRejectsExactBoundaries(t *testing.T) {
	for _, confidence := range []float64{0, 1} {
		t.Run(fmt.Sprintf("confidence-%v", confidence), func(t *testing.T) {
			data := automationTestEnvelopeJSON(t, []any{
				automationTestCall("x", AutomationToolMessageClassify, confidence, []any{}, map[string]any{
					"labels":  []string{},
					"summary": "ok",
				}),
			})
			if _, err := DecodeAutomationToolEnvelope(data); err != nil {
				t.Fatalf("boundary confidence rejected: %v", err)
			}
		})
	}
	for _, confidence := range []float64{-0.01, 1.01} {
		t.Run(fmt.Sprintf("invalid-confidence-%v", confidence), func(t *testing.T) {
			automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
				automationTestCall("x", AutomationToolMessageClassify, confidence, []any{}, map[string]any{
					"labels":  []string{},
					"summary": "ok",
				}),
			}))
		})
	}

	maxActions := make([]any, AutomationMaxActionsPerRun)
	for i := range maxActions {
		maxActions[i] = automationTestCall(
			fmt.Sprintf("call-%d", i),
			AutomationToolMessageClassify,
			1,
			[]any{},
			map[string]any{"labels": []string{}, "summary": "ok"},
		)
	}
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, maxActions)); err != nil {
		t.Fatalf("maximum actions rejected: %v", err)
	}
	tooManyActions := append(append([]any{}, maxActions...), automationTestCall(
		"overflow",
		AutomationToolMessageClassify,
		1,
		[]any{},
		map[string]any{"labels": []string{}, "summary": "ok"},
	))
	automationRequireRejected(t, automationTestEnvelopeJSON(t, tooManyActions))

	maxEvidence := make([]any, AutomationMaxEvidencePerAction)
	for i := range maxEvidence {
		maxEvidence[i] = map[string]any{"source": "source_message", "excerpt": fmt.Sprintf("evidence-%d", i)}
	}
	maxEvidenceCall := automationTestCall("evidence", AutomationToolMessageClassify, 1, maxEvidence, map[string]any{
		"labels":  []string{},
		"summary": "ok",
	})
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{maxEvidenceCall})); err != nil {
		t.Fatalf("maximum evidence rejected: %v", err)
	}
	tooMuchEvidence := append(append([]any{}, maxEvidence...), map[string]any{"source": "source_message", "excerpt": "overflow"})
	automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
		automationTestCall("evidence", AutomationToolMessageClassify, 1, tooMuchEvidence, map[string]any{
			"labels":  []string{},
			"summary": "ok",
		}),
	}))

	maxExcerpt := strings.Repeat("界", 500)
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("evidence", AutomationToolMessageClassify, 1, []any{
			map[string]any{"source": "source_message", "excerpt": maxExcerpt},
		}, map[string]any{"labels": []string{}, "summary": "ok"}),
	})); err != nil {
		t.Fatalf("500-rune evidence rejected: %v", err)
	}
	automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
		automationTestCall("evidence", AutomationToolMessageClassify, 1, []any{
			map[string]any{"source": "source_message", "excerpt": maxExcerpt + "界"},
		}, map[string]any{"labels": []string{}, "summary": "ok"}),
	}))

	maxReply := strings.Repeat("界", 4000)
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("reply", AutomationToolReplyPrivateText, 1, []any{}, map[string]any{
			"intent": AutomationReplyIntentGeneral,
			"text":   maxReply,
		}),
	})); err != nil {
		t.Fatalf("4000-rune reply rejected: %v", err)
	}
	automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
		automationTestCall("reply", AutomationToolReplyPrivateText, 1, []any{}, map[string]any{
			"intent": AutomationReplyIntentGeneral,
			"text":   maxReply + "界",
		}),
	}))

	base := automationTestEnvelopeJSON(t, []any{})
	exactSize := append(append([]byte{}, base...), bytes.Repeat([]byte(" "), AutomationMaxProviderBytes-len(base))...)
	if len(exactSize) != AutomationMaxProviderBytes {
		t.Fatalf("exact-size fixture length = %d", len(exactSize))
	}
	if _, err := DecodeAutomationToolEnvelope(exactSize); err != nil {
		t.Fatalf("exact provider byte limit rejected: %v", err)
	}
	automationRequireRejected(t, append(exactSize, ' '))
}

func TestDecodeAutomationToolEnvelopeEnforcesExactListBoundaries(t *testing.T) {
	maxLabels := []string{
		AutomationLabelCandidateInterest,
		AutomationLabelJobInquiry,
		AutomationLabelAvailability,
		AutomationLabelCompensation,
		AutomationLabelInterview,
	}
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("labels", AutomationToolMessageClassify, 1, []any{}, map[string]any{
			"labels":  maxLabels,
			"summary": "ok",
		}),
	})); err != nil {
		t.Fatalf("maximum classification labels rejected: %v", err)
	}

	maxSkills := make([]string, 32)
	for i := range maxSkills {
		maxSkills[i] = fmt.Sprintf("skill-%d", i)
	}
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("skills", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
			"full_name": "Ada",
			"skills":    maxSkills,
		}),
	})); err != nil {
		t.Fatalf("maximum candidate skills rejected: %v", err)
	}
	automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
		automationTestCall("skills", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
			"full_name": "Ada",
			"skills":    append(append([]string{}, maxSkills...), "overflow"),
		}),
	}))

	maxMatches := make([]any, 50)
	for i := range maxMatches {
		maxMatches[i] = map[string]any{
			"job": map[string]any{
				"id":               fmt.Sprintf("job_%08d", i+1),
				"expected_version": 1,
			},
			"score":     1,
			"rationale": "fit",
		}
	}
	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("matches", AutomationToolJobMatch, 1, []any{}, map[string]any{
			"matches": maxMatches,
		}),
	})); err != nil {
		t.Fatalf("maximum job matches rejected: %v", err)
	}
	automationRequireRejected(t, automationTestEnvelopeJSON(t, []any{
		automationTestCall("matches", AutomationToolJobMatch, 1, []any{}, map[string]any{
			"matches": append(append([]any{}, maxMatches...), map[string]any{
				"job": map[string]any{
					"id":               "job_00000051",
					"expected_version": 1,
				},
				"score":     1,
				"rationale": "overflow",
			}),
		}),
	}))

	if _, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("empty-matches", AutomationToolJobMatch, 1, []any{}, map[string]any{
			"matches": []any{},
		}),
	})); err != nil {
		t.Fatalf("empty valid job match list rejected: %v", err)
	}
}

func TestDecodeAutomationToolEnvelopeEnforcesCanonicalPayloadBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact maximum", size: AutomationMaxActionPayloadBytes},
		{name: "one byte over", size: AutomationMaxActionPayloadBytes + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := automationJobMatchArgumentsOfCanonicalSize(t, test.size)
			data := automationTestEnvelopeJSON(t, []any{
				automationTestCall("matches", AutomationToolJobMatch, 1, []any{}, arguments),
			})
			envelope, err := DecodeAutomationToolEnvelope(data)
			if test.wantErr {
				if !errors.Is(err, ErrAutomationValidation) {
					t.Fatalf("DecodeAutomationToolEnvelope() error = %v, want automation validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeAutomationToolEnvelope() error = %v", err)
			}
			if got := len(envelope.Actions[0].Arguments); got != test.size {
				t.Fatalf("canonical argument size = %d, want %d", got, test.size)
			}
		})
	}
}

func TestValidateAutomationEnvelopeEvidenceRejectsMissingContextAndExcerpt(t *testing.T) {
	envelope, err := DecodeAutomationToolEnvelope(automationTestEnvelopeJSON(t, []any{
		automationTestCall("classify", AutomationToolMessageClassify, 1, []any{
			map[string]any{"source": "source_message", "field": "text", "excerpt": "backend role"},
		}, map[string]any{
			"labels":  []string{AutomationLabelJobInquiry},
			"summary": "Job inquiry",
		}),
	}))
	if err != nil {
		t.Fatalf("decode evidence fixture: %v", err)
	}
	if err := ValidateAutomationEnvelopeEvidence(envelope, nil); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("nil resolver error = %v", err)
	}
	if err := ValidateAutomationEnvelopeEvidence(envelope, func(AutomationEvidence) (string, bool) {
		return "", false
	}); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("missing context error = %v", err)
	}
	if err := ValidateAutomationEnvelopeEvidence(envelope, func(AutomationEvidence) (string, bool) {
		return "frontend role", true
	}); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("missing excerpt error = %v", err)
	}
}

func TestAutomationStateTransitionsAreClosed(t *testing.T) {
	runStatuses := []AutomationRunStatus{
		AutomationRunQueued,
		AutomationRunRunning,
		AutomationRunSucceeded,
		AutomationRunFailed,
	}
	runTransitions := map[[2]AutomationRunStatus]bool{
		{AutomationRunQueued, AutomationRunRunning}:    true,
		{AutomationRunRunning, AutomationRunSucceeded}: true,
		{AutomationRunRunning, AutomationRunFailed}:    true,
	}
	for _, from := range runStatuses {
		if !IsValidAutomationRunStatus(from) {
			t.Errorf("IsValidAutomationRunStatus(%q) = false", from)
		}
		for _, to := range runStatuses {
			want := runTransitions[[2]AutomationRunStatus{from, to}]
			if got := CanTransitionAutomationRunStatus(from, to); got != want {
				t.Errorf("CanTransitionAutomationRunStatus(%q, %q) = %v, want %v", from, to, got, want)
			}
			err := ValidateAutomationRunTransition(from, to)
			if want && err != nil {
				t.Errorf("ValidateAutomationRunTransition(%q, %q) error = %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrAutomationValidation) {
				t.Errorf("ValidateAutomationRunTransition(%q, %q) error = %v, want validation error", from, to, err)
			}
		}
	}

	actionStatuses := []AutomationActionStatus{
		AutomationActionProposed,
		AutomationActionAwaitingReview,
		AutomationActionApproved,
		AutomationActionExecuting,
		AutomationActionSucceeded,
		AutomationActionRejected,
		AutomationActionStale,
		AutomationActionSuperseded,
		AutomationActionFailed,
		AutomationActionDenied,
		AutomationActionUnknown,
	}
	actionTransitions := map[[2]AutomationActionStatus]bool{
		{AutomationActionProposed, AutomationActionSucceeded}:        true,
		{AutomationActionProposed, AutomationActionAwaitingReview}:   true,
		{AutomationActionProposed, AutomationActionApproved}:         true,
		{AutomationActionProposed, AutomationActionDenied}:           true,
		{AutomationActionAwaitingReview, AutomationActionApproved}:   true,
		{AutomationActionAwaitingReview, AutomationActionRejected}:   true,
		{AutomationActionAwaitingReview, AutomationActionDenied}:     true,
		{AutomationActionAwaitingReview, AutomationActionStale}:      true,
		{AutomationActionAwaitingReview, AutomationActionSuperseded}: true,
		{AutomationActionApproved, AutomationActionExecuting}:        true,
		{AutomationActionExecuting, AutomationActionSucceeded}:       true,
		{AutomationActionExecuting, AutomationActionFailed}:          true,
		{AutomationActionExecuting, AutomationActionStale}:           true,
		{AutomationActionExecuting, AutomationActionSuperseded}:      true,
		{AutomationActionExecuting, AutomationActionUnknown}:         true,
	}
	terminalStatuses := map[AutomationActionStatus]bool{
		AutomationActionSucceeded:  true,
		AutomationActionRejected:   true,
		AutomationActionStale:      true,
		AutomationActionSuperseded: true,
		AutomationActionFailed:     true,
		AutomationActionDenied:     true,
		AutomationActionUnknown:    true,
	}
	for _, from := range actionStatuses {
		if !IsValidAutomationActionStatus(from) {
			t.Errorf("IsValidAutomationActionStatus(%q) = false", from)
		}
		if got := IsTerminalAutomationActionStatus(from); got != terminalStatuses[from] {
			t.Errorf("IsTerminalAutomationActionStatus(%q) = %v, want %v", from, got, terminalStatuses[from])
		}
		for _, to := range actionStatuses {
			want := actionTransitions[[2]AutomationActionStatus{from, to}]
			if got := CanTransitionAutomationActionStatus(from, to); got != want {
				t.Errorf("CanTransitionAutomationActionStatus(%q, %q) = %v, want %v", from, to, got, want)
			}
			err := ValidateAutomationActionTransition(from, to)
			if want && err != nil {
				t.Errorf("ValidateAutomationActionTransition(%q, %q) error = %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrAutomationValidation) {
				t.Errorf("ValidateAutomationActionTransition(%q, %q) error = %v, want validation error", from, to, err)
			}
		}
	}

	outboxStatuses := []AutomationOutboxStatus{
		AutomationOutboxPending,
		AutomationOutboxDispatching,
		AutomationOutboxSent,
		AutomationOutboxFailed,
		AutomationOutboxUnknown,
	}
	outboxTransitions := map[[2]AutomationOutboxStatus]bool{
		{AutomationOutboxPending, AutomationOutboxDispatching}: true,
		{AutomationOutboxPending, AutomationOutboxFailed}:      true,
		{AutomationOutboxDispatching, AutomationOutboxSent}:    true,
		{AutomationOutboxDispatching, AutomationOutboxFailed}:  true,
		{AutomationOutboxDispatching, AutomationOutboxUnknown}: true,
		{AutomationOutboxFailed, AutomationOutboxPending}:      true,
	}
	for _, from := range outboxStatuses {
		if !IsValidAutomationOutboxStatus(from) {
			t.Errorf("IsValidAutomationOutboxStatus(%q) = false", from)
		}
		for _, to := range outboxStatuses {
			want := outboxTransitions[[2]AutomationOutboxStatus{from, to}]
			if got := CanTransitionAutomationOutboxStatus(from, to); got != want {
				t.Errorf("CanTransitionAutomationOutboxStatus(%q, %q) = %v, want %v", from, to, got, want)
			}
			err := ValidateAutomationOutboxTransition(from, to)
			if want && err != nil {
				t.Errorf("ValidateAutomationOutboxTransition(%q, %q) error = %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrAutomationValidation) {
				t.Errorf("ValidateAutomationOutboxTransition(%q, %q) error = %v, want validation error", from, to, err)
			}
		}
	}

	if IsValidAutomationRunStatus("invalid") ||
		IsValidAutomationActionStatus("invalid") ||
		IsTerminalAutomationActionStatus("invalid") ||
		IsValidAutomationOutboxStatus("invalid") {
		t.Fatal("unknown state-machine enum was accepted")
	}
	for _, err := range []error{
		ValidateAutomationRunTransition("invalid", AutomationRunQueued),
		ValidateAutomationRunTransition(AutomationRunQueued, "invalid"),
		ValidateAutomationActionTransition("invalid", AutomationActionProposed),
		ValidateAutomationActionTransition(AutomationActionProposed, "invalid"),
		ValidateAutomationOutboxTransition("invalid", AutomationOutboxPending),
		ValidateAutomationOutboxTransition(AutomationOutboxPending, "invalid"),
	} {
		if !errors.Is(err, ErrAutomationValidation) {
			t.Errorf("invalid enum transition error = %v, want validation error", err)
		}
	}
}

func TestAutomationContractEnumsAreClosed(t *testing.T) {
	for _, mode := range []AutomationMode{AutomationModeManual, AutomationModeAutomatic} {
		if !IsValidAutomationMode(mode) {
			t.Errorf("IsValidAutomationMode(%q) = false", mode)
		}
	}
	if IsValidAutomationMode("invalid") {
		t.Fatal("unknown automation mode was accepted")
	}

	for _, risk := range []AutomationRisk{
		AutomationRiskR0,
		AutomationRiskR1,
		AutomationRiskR2,
		AutomationRiskR3,
		AutomationRiskR4,
	} {
		if !IsValidAutomationRisk(risk) {
			t.Errorf("IsValidAutomationRisk(%q) = false", risk)
		}
	}
	if IsValidAutomationRisk("R5") {
		t.Fatal("unknown automation risk was accepted")
	}

	for _, decision := range []AutomationPolicyDecision{
		AutomationDecisionAutoComplete,
		AutomationDecisionAutoApprove,
		AutomationDecisionReview,
		AutomationDecisionDeny,
	} {
		if !IsValidAutomationPolicyDecision(decision) {
			t.Errorf("IsValidAutomationPolicyDecision(%q) = false", decision)
		}
	}
	if IsValidAutomationPolicyDecision("allow") {
		t.Fatal("unknown automation policy decision was accepted")
	}

	tools := map[AutomationToolName]string{
		AutomationToolMessageClassify:  "",
		AutomationToolCandidateCreate:  AutomationEntityCandidate,
		AutomationToolCandidateUpdate:  AutomationEntityCandidate,
		AutomationToolCandidateLink:    AutomationEntityCandidate,
		AutomationToolJobMatch:         "",
		AutomationToolJobCreate:        AutomationEntityJob,
		AutomationToolLeadCreate:       AutomationEntityLead,
		AutomationToolLeadTransition:   AutomationEntityLead,
		AutomationToolTaskCreate:       AutomationEntityTask,
		AutomationToolNoteCreate:       AutomationEntityNote,
		AutomationToolReplyPrivateText: "",
	}
	for tool, outputType := range tools {
		if !IsValidAutomationToolName(tool) {
			t.Errorf("IsValidAutomationToolName(%q) = false", tool)
		}
		if got := AutomationToolOutputEntityType(tool); got != outputType {
			t.Errorf("AutomationToolOutputEntityType(%q) = %q, want %q", tool, got, outputType)
		}
	}
	if IsValidAutomationToolName("shell.exec") {
		t.Fatal("unknown automation tool was accepted")
	}
	if got := AutomationToolOutputEntityType("shell.exec"); got != "" {
		t.Fatalf("unknown tool output type = %q", got)
	}
}

func TestAutomationContractDecodingIsConcurrentSafe(t *testing.T) {
	data := automationTestEnvelopeJSON(t, []any{
		automationTestCall("create", AutomationToolCandidateCreate, 1, []any{}, map[string]any{
			"full_name": "Ada",
			"skills":    []string{"Go"},
		}),
		automationTestCall("link", AutomationToolCandidateLink, 1, []any{
			map[string]any{"source": "source_message", "field": "text", "excerpt": "backend role"},
		}, map[string]any{
			"candidate": map[string]any{"from_call": "create"},
		}),
	})

	const workers = 32
	const iterations = 50
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				envelope, err := DecodeAutomationToolEnvelope(data)
				if err != nil {
					errs <- fmt.Errorf("worker %d iteration %d decode: %w", worker, iteration, err)
					return
				}
				if len(envelope.Actions) != 2 {
					errs <- fmt.Errorf("worker %d iteration %d actions = %d", worker, iteration, len(envelope.Actions))
					return
				}
				if _, err := DecodeAutomationToolArguments(envelope.Actions[1].Tool, envelope.Actions[1].Arguments); err != nil {
					errs <- fmt.Errorf("worker %d iteration %d arguments: %w", worker, iteration, err)
					return
				}
				if err := ValidateAutomationEnvelopeEvidence(envelope, func(AutomationEvidence) (string, bool) {
					return "Candidate asks about a backend role.", true
				}); err != nil {
					errs <- fmt.Errorf("worker %d iteration %d evidence: %w", worker, iteration, err)
					return
				}
			}
		}(worker)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func automationTestCall(callID string, tool AutomationToolName, confidence float64, evidence []any, arguments any) map[string]any {
	return map[string]any{
		"call_id":    callID,
		"tool":       tool,
		"confidence": confidence,
		"evidence":   evidence,
		"arguments":  arguments,
	}
}

func automationProviderTestCall(
	t *testing.T,
	callID string,
	tool AutomationToolName,
	arguments any,
) AutomationToolCall {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal automation provider call arguments: %v", err)
	}
	return AutomationToolCall{
		CallID:     callID,
		Tool:       tool,
		Confidence: 1,
		Evidence:   []AutomationEvidence{},
		Arguments:  raw,
	}
}

func automationTestEnvelopeJSON(t *testing.T, calls []any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema_version": AutomationToolEnvelopeVersion,
		"actions":        calls,
	})
	if err != nil {
		t.Fatalf("marshal automation envelope fixture: %v", err)
	}
	return data
}

func automationRequireRejected(t *testing.T, data []byte) {
	t.Helper()
	if _, err := DecodeAutomationToolEnvelope(data); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("DecodeAutomationToolEnvelope() error = %v, want automation validation error", err)
	}
}

func automationJobMatchArgumentsOfCanonicalSize(t *testing.T, target int) json.RawMessage {
	t.Helper()
	const matchCount = 9
	matches := make([]AutomationJobMatch, matchCount)
	for i := range matches {
		matches[i] = AutomationJobMatch{
			Job: AutomationEntityRef{
				ID:              fmt.Sprintf("job_%08d", i+1),
				ExpectedVersion: 1,
			},
			Score:     1,
			Rationale: "x",
		}
	}

	arguments := JobMatchArgs{Matches: matches}
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal payload boundary fixture: %v", err)
	}
	remaining := target - len(data)
	for i := range arguments.Matches {
		add := min(remaining, 4000-len(arguments.Matches[i].Rationale))
		arguments.Matches[i].Rationale += strings.Repeat("x", add)
		remaining -= add
	}
	if remaining != 0 {
		t.Fatalf("could not construct canonical payload of %d bytes", target)
	}
	data, err = json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal payload boundary fixture: %v", err)
	}
	if len(data) != target {
		t.Fatalf("payload boundary fixture size = %d, want %d", len(data), target)
	}
	return data
}
