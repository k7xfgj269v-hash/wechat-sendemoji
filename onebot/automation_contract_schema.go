package main

import (
	"encoding/json"
	"strings"
)

func AutomationToolSchema(tool AutomationToolName) (json.RawMessage, error) {
	schema, err := automationToolSchemaValue(tool)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func AutomationToolDefinitions(tools []AutomationToolName) ([]AutomationToolDefinition, error) {
	normalized, err := normalizeAutomationProviderTools(tools)
	if err != nil {
		return nil, err
	}
	definitions := make([]AutomationToolDefinition, 0, len(normalized))
	for _, tool := range normalized {
		parameters, err := AutomationToolSchema(tool)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, AutomationToolDefinition{
			Name:        tool,
			Description: automationToolDescription(tool),
			Parameters:  parameters,
			Strict:      true,
		})
	}
	return definitions, nil
}

func automationToolSchemaValue(tool AutomationToolName) (map[string]any, error) {
	if IsWorkbenchToolName(tool) {
		specs := workbenchOperationSpecs(tool)
		operations := make([]string, len(specs))
		for i := range specs {
			operations[i] = string(specs[i].catalog.Operation)
		}
		return automationSchemaObject(map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": operations,
			},
			"arguments_json": map[string]any{
				"type":        "string",
				"maxLength":   WorkbenchMaxArgumentsJSONBytes,
				"description": automationWorkbenchArgumentsDescription(specs),
			},
		}, []string{"operation", "arguments_json"}, nil), nil
	}

	ref := func(entity string) map[string]any {
		return automationSchemaObject(
			map[string]any{
				"id":               automationSchemaString(80),
				"expected_version": automationSchemaInteger(1, 0),
				"from_call":        automationSchemaString(80),
			},
			nil,
			map[string]any{
				"oneOf": []any{
					map[string]any{"required": []string{"id", "expected_version"}},
					map[string]any{"required": []string{"from_call"}},
				},
				"description": "Reference to an existing " + entity + " or a tool call in the same provider response.",
			},
		)
	}
	stringList := func(maxItems, maxLength int) map[string]any {
		return map[string]any{
			"type":     "array",
			"maxItems": maxItems,
			"items":    automationSchemaString(maxLength),
		}
	}
	timeSchema := map[string]any{"type": "string", "format": "date-time"}
	booleanSchema := map[string]any{"type": "boolean"}
	jobPriority := automationSchemaEnum("low", "normal", "high", "urgent")
	replyIntent := automationSchemaEnum(
		AutomationReplyIntentGeneral,
		AutomationReplyIntentScheduling,
		AutomationReplyIntentInformation,
		AutomationReplyIntentOffer,
		AutomationReplyIntentRejection,
		AutomationReplyIntentHiring,
		AutomationReplyIntentOnboarding,
		AutomationReplyIntentCompensationCommitment,
		AutomationReplyIntentLegalCommitment,
		AutomationReplyIntentPaymentRequest,
		AutomationReplyIntentCredentialRequest,
	)

	switch tool {
	case AutomationToolMessageClassify:
		return automationSchemaObject(map[string]any{
			"labels": map[string]any{
				"type":     "array",
				"maxItems": 5,
				"items": automationSchemaEnum(
					AutomationLabelCandidateInterest,
					AutomationLabelJobInquiry,
					AutomationLabelAvailability,
					AutomationLabelCompensation,
					AutomationLabelInterview,
					AutomationLabelOffer,
					AutomationLabelRejection,
					AutomationLabelOnboarding,
					AutomationLabelGeneral,
					AutomationLabelSpam,
					AutomationLabelSuspicious,
				),
			},
			"summary": automationSchemaString(4000),
		}, []string{"labels", "summary"}, nil), nil
	case AutomationToolCandidateCreate:
		return automationSchemaObject(map[string]any{
			"full_name": automationSchemaString(120),
			"phone":     automationSchemaString(40),
			"email":     automationSchemaString(254),
			"location":  automationSchemaString(160),
			"skills":    stringList(32, 64),
			"owner":     automationSchemaString(80),
			"summary":   automationSchemaString(4000),
		}, []string{"full_name", "skills"}, nil), nil
	case AutomationToolCandidateUpdate:
		patch := automationSchemaObject(map[string]any{
			"full_name": automationSchemaString(120),
			"wechat_id": automationSchemaString(128),
			"phone":     automationSchemaString(40),
			"email":     automationSchemaString(254),
			"location":  automationSchemaString(160),
			"source":    automationSchemaString(120),
			"skills":    stringList(32, 64),
			"owner":     automationSchemaString(80),
			"status":    automationSchemaEnum("active", "paused", "placed", "archived"),
			"summary":   automationSchemaString(4000),
		}, nil, map[string]any{"minProperties": 1})
		return automationSchemaObject(map[string]any{
			"candidate": ref(AutomationEntityCandidate),
			"patch":     patch,
		}, []string{"candidate", "patch"}, nil), nil
	case AutomationToolCandidateLink:
		return automationSchemaObject(map[string]any{
			"candidate": ref(AutomationEntityCandidate),
		}, []string{"candidate"}, nil), nil
	case AutomationToolJobMatch:
		match := automationSchemaObject(map[string]any{
			"job":       ref(AutomationEntityJob),
			"score":     automationSchemaNumber(0, 1),
			"rationale": automationSchemaString(4000),
		}, []string{"job", "score", "rationale"}, nil)
		return automationSchemaObject(map[string]any{
			"matches": map[string]any{
				"type":     "array",
				"maxItems": 50,
				"items":    match,
			},
		}, []string{"matches"}, nil), nil
	case AutomationToolJobCreate:
		job := automationSchemaObject(map[string]any{
			"title":                  automationSchemaString(160),
			"company":                automationSchemaString(160),
			"location":               automationSchemaString(160),
			"employment_type":        automationSchemaString(80),
			"description":            automationSchemaString(8000),
			"requirements":           stringList(64, 1000),
			"demand_code":            automationSchemaString(120),
			"site_name":              automationSchemaString(160),
			"onsite_contact":         automationSchemaString(160),
			"headcount":              automationSchemaInteger(0, 1000000),
			"priority":               jobPriority,
			"start_at":               timeSchema,
			"deadline_at":            timeSchema,
			"shift":                  automationSchemaString(160),
			"work_schedule":          automationSchemaString(2000),
			"compensation_summary":   automationSchemaString(2000),
			"accommodation":          automationSchemaString(2000),
			"transport_requirements": automationSchemaString(2000),
			"required_documents":     stringList(64, 200),
			"owner":                  automationSchemaString(80),
			"status":                 automationSchemaEnum(string(JobStatusOpen)),
		}, []string{"title", "company", "requirements", "headcount", "required_documents"}, nil)
		return automationSchemaObject(map[string]any{
			"event_id": automationSchemaString(80),
			"group_id": automationSchemaString(256),
			"job":      job,
		}, []string{"event_id", "group_id", "job"}, nil), nil
	case AutomationToolLeadCreate:
		return automationSchemaObject(map[string]any{
			"candidate": ref(AutomationEntityCandidate),
			"job":       ref(AutomationEntityJob),
			"owner":     automationSchemaString(80),
		}, []string{"candidate", "job"}, nil), nil
	case AutomationToolLeadTransition:
		return automationSchemaObject(map[string]any{
			"lead":       ref(AutomationEntityLead),
			"from_stage": automationSchemaEnum("sourced", "screening", "interview", "offer", "hired", "rejected", "withdrawn"),
			"to_stage":   automationSchemaEnum("sourced", "screening", "interview", "offer", "hired", "rejected", "withdrawn"),
		}, []string{"lead", "from_stage", "to_stage"}, nil), nil
	case AutomationToolTaskCreate:
		return automationSchemaObject(map[string]any{
			"title":       automationSchemaString(200),
			"description": automationSchemaString(4000),
			"owner":       automationSchemaString(80),
			"due_at":      timeSchema,
			"target_type": automationSchemaEnum(AutomationEntityCandidate, AutomationEntityLead),
			"target":      ref("target"),
		}, []string{"title", "due_at", "target_type", "target"}, nil), nil
	case AutomationToolNoteCreate:
		return automationSchemaObject(map[string]any{
			"target_type": automationSchemaEnum(string(NoteTargetCandidate), string(NoteTargetLead)),
			"target":      ref("target"),
			"author":      automationSchemaString(80),
			"body":        automationSchemaString(4000),
		}, []string{"target_type", "target", "body"}, nil), nil
	case AutomationToolReplyPrivateText:
		return automationSchemaObject(map[string]any{
			"intent": replyIntent,
			"text":   automationSchemaString(4000),
		}, []string{"intent", "text"}, nil), nil
	case AutomationToolJobUpdate:
		patch := automationSchemaObject(map[string]any{
			"title":                  automationSchemaString(200),
			"company":                automationSchemaString(200),
			"location":               automationSchemaString(160),
			"employment_type":        automationSchemaString(80),
			"description":            automationSchemaString(8000),
			"requirements":           stringList(64, 500),
			"demand_code":            automationSchemaString(120),
			"site_name":              automationSchemaString(200),
			"onsite_contact":         automationSchemaString(200),
			"headcount":              automationSchemaInteger(1, 1000000),
			"priority":               jobPriority,
			"start_at":               timeSchema,
			"deadline_at":            timeSchema,
			"clear_start_at":         booleanSchema,
			"clear_deadline_at":      booleanSchema,
			"shift":                  automationSchemaString(200),
			"work_schedule":          automationSchemaString(1000),
			"compensation_summary":   automationSchemaString(2000),
			"accommodation":          automationSchemaString(2000),
			"transport_requirements": automationSchemaString(2000),
			"required_documents":     stringList(64, 200),
			"owner":                  automationSchemaString(80),
			"status":                 automationSchemaEnum("draft", "open", "paused", "closed"),
		}, nil, map[string]any{"minProperties": 1})
		return automationSchemaObject(map[string]any{
			"job":   ref(AutomationEntityJob),
			"patch": patch,
		}, []string{"job", "patch"}, nil), nil
	case AutomationToolOnboardingCreate:
		return automationSchemaObject(map[string]any{
			"lead":                ref(AutomationEntityLead),
			"entry_at":            timeSchema,
			"entry_address":       automationSchemaString(500),
			"site_contact":        automationSchemaString(200),
			"required_documents":  stringList(64, 200),
			"completed_documents": stringList(64, 200),
			"missing_information": stringList(64, 500),
		}, []string{"lead"}, nil), nil
	case AutomationToolOnboardingUpdate:
		patch := automationSchemaObject(map[string]any{
			"entry_at":            timeSchema,
			"clear_entry_at":      booleanSchema,
			"entry_address":       automationSchemaString(500),
			"site_contact":        automationSchemaString(200),
			"required_documents":  stringList(64, 200),
			"completed_documents": stringList(64, 200),
			"missing_information": stringList(64, 500),
			"block_reason":        automationSchemaString(1000),
		}, nil, map[string]any{"minProperties": 1})
		return automationSchemaObject(map[string]any{
			"onboarding": ref(AutomationEntityOnboarding),
			"patch":      patch,
		}, []string{"onboarding", "patch"}, nil), nil
	case AutomationToolFleetUpdate:
		patch := automationSchemaObject(map[string]any{
			"required":            booleanSchema,
			"pickup_address":      automationSchemaString(500),
			"pickup_at":           timeSchema,
			"clear_pickup_at":     booleanSchema,
			"destination":         automationSchemaString(500),
			"luggage_note":        automationSchemaString(1000),
			"vehicle_requirement": automationSchemaString(1000),
			"fleet_contact":       automationSchemaString(200),
			"driver_name":         automationSchemaString(120),
			"driver_phone":        automationSchemaString(40),
			"vehicle_plate":       automationSchemaString(40),
		}, nil, map[string]any{"minProperties": 1})
		return automationSchemaObject(map[string]any{
			"onboarding": ref(AutomationEntityOnboarding),
			"patch":      patch,
		}, []string{"onboarding", "patch"}, nil), nil
	case AutomationToolServiceCreate:
		return automationSchemaObject(map[string]any{
			"candidate":      ref(AutomationEntityCandidate),
			"job":            ref(AutomationEntityJob),
			"onboarding":     ref(AutomationEntityOnboarding),
			"category":       automationSchemaEnum("general", "transport", "accommodation", "equipment", "schedule", "document", "safety", "dispute", "fund", "other"),
			"priority":       jobPriority,
			"description":    automationSchemaString(4000),
			"owner":          automationSchemaString(80),
			"requires_human": booleanSchema,
			"due_at":         timeSchema,
		}, []string{"candidate", "category", "description"}, nil), nil
	case AutomationToolServiceUpdate:
		patch := automationSchemaObject(map[string]any{
			"candidate":      ref(AutomationEntityCandidate),
			"job":            ref(AutomationEntityJob),
			"onboarding":     ref(AutomationEntityOnboarding),
			"category":       automationSchemaEnum("general", "transport", "accommodation", "equipment", "schedule", "document", "safety", "dispute", "fund", "other"),
			"priority":       jobPriority,
			"description":    automationSchemaString(4000),
			"owner":          automationSchemaString(80),
			"requires_human": booleanSchema,
			"due_at":         timeSchema,
			"clear_due_at":   booleanSchema,
			"resolution":     automationSchemaString(4000),
		}, nil, map[string]any{"minProperties": 1})
		return automationSchemaObject(map[string]any{
			"service_request": ref(AutomationEntityServiceRequest),
			"patch":           patch,
		}, []string{"service_request", "patch"}, nil), nil
	case AutomationToolFundCreate:
		return automationSchemaObject(map[string]any{
			"candidate":    ref(AutomationEntityCandidate),
			"job":          ref(AutomationEntityJob),
			"lead":         ref(AutomationEntityLead),
			"onboarding":   ref(AutomationEntityOnboarding),
			"amount_minor": automationSchemaInteger(1, 0),
			"currency":     automationSchemaString(3),
			"condition":    automationSchemaString(2000),
			"promised_by":  automationSchemaString(200),
			"due_at":       timeSchema,
			"evidence":     automationSchemaString(4000),
		}, []string{"candidate", "amount_minor", "currency", "condition", "promised_by"}, nil), nil
	case AutomationToolReplyAudienceText:
		return automationSchemaObject(map[string]any{
			"audience_id": automationSchemaString(80),
			"intent":      replyIntent,
			"text":        automationSchemaString(4000),
		}, []string{"audience_id", "intent", "text"}, nil), nil
	case AutomationToolForwardEvent:
		return automationSchemaObject(map[string]any{
			"event_id": automationSchemaString(80),
			"rule_id":  automationSchemaString(80),
			"reason":   automationSchemaString(500),
		}, []string{"event_id", "rule_id"}, nil), nil
	default:
		return nil, NewAutomationValidationError("tool", "unsupported automation tool")
	}
}

func automationSchemaObject(
	properties map[string]any,
	required []string,
	extra map[string]any,
) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
	for key, value := range extra {
		schema[key] = value
	}
	return schema
}

func automationSchemaString(maxLength int) map[string]any {
	return map[string]any{"type": "string", "maxLength": maxLength}
}

func automationWorkbenchArgumentsDescription(specs []workbenchOperationSpec) string {
	var description strings.Builder
	description.WriteString(
		"One JSON-encoded strict object for the selected operation. Required top-level fields: ",
	)
	for index, spec := range specs {
		if index != 0 {
			description.WriteString("; ")
		}
		description.WriteString(string(spec.catalog.Operation))
		description.WriteByte('(')
		description.WriteString(strings.Join(spec.requiredFields, ","))
		description.WriteByte(')')
	}
	description.WriteString(". Use {} when no fields are required.")
	return description.String()
}

func automationSchemaEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func automationSchemaInteger(minimum, maximum int64) map[string]any {
	schema := map[string]any{"type": "integer", "minimum": minimum}
	if maximum > 0 {
		schema["maximum"] = maximum
	}
	return schema
}

func automationSchemaNumber(minimum, maximum float64) map[string]any {
	return map[string]any{"type": "number", "minimum": minimum, "maximum": maximum}
}

func automationToolDescription(tool AutomationToolName) string {
	if entry, ok := WorkbenchToolCatalogEntryFor(tool); ok {
		return entry.Description
	}
	switch tool {
	case AutomationToolMessageClassify:
		return "Classify the source message using the closed recruitment label set."
	case AutomationToolCandidateCreate:
		return "Create one candidate from verified source-message facts."
	case AutomationToolCandidateUpdate:
		return "Update an existing candidate with an expected version."
	case AutomationToolCandidateLink:
		return "Link the source message to an existing candidate."
	case AutomationToolJobMatch:
		return "Rank allowed jobs for the candidate context."
	case AutomationToolJobCreate:
		return "Create or update one job from the current verified recruiting source-group message."
	case AutomationToolLeadCreate:
		return "Create a lead joining a candidate and job."
	case AutomationToolLeadTransition:
		return "Move a lead through an allowed stage transition."
	case AutomationToolTaskCreate:
		return "Create a follow-up task for a candidate or lead."
	case AutomationToolNoteCreate:
		return "Create a note for a candidate or lead."
	case AutomationToolReplyPrivateText:
		return "Propose one controlled private text reply."
	case AutomationToolJobUpdate:
		return "Update an existing job with an expected version."
	case AutomationToolOnboardingCreate:
		return "Create onboarding for an existing lead."
	case AutomationToolOnboardingUpdate:
		return "Update an existing onboarding case."
	case AutomationToolFleetUpdate:
		return "Update transport details for onboarding."
	case AutomationToolServiceCreate:
		return "Create a candidate service request."
	case AutomationToolServiceUpdate:
		return "Update an existing service request."
	case AutomationToolFundCreate:
		return "Record a fund callback commitment without initiating payment."
	case AutomationToolReplyAudienceText:
		return "Propose controlled text for a server-owned audience."
	case AutomationToolForwardEvent:
		return "Forward an existing event through an existing server-owned rule."
	default:
		return ""
	}
}
