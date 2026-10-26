package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

func (arguments WorkbenchLeadTransitionArguments) DomainTransition(actor string) LeadTransition {
	return LeadTransition{
		ExpectedVersion: arguments.ExpectedVersion,
		FromStage:       arguments.FromStage,
		ToStage:         arguments.ToStage,
		Reason:          arguments.Reason,
		Actor:           actor,
	}
}

func (arguments WorkbenchNoteCreateArguments) DomainNote(author string) NoteCreate {
	return NoteCreate{
		TargetType: arguments.TargetType,
		TargetID:   arguments.TargetID,
		Author:     author,
		Body:       arguments.Body,
	}
}

func (arguments WorkbenchJobCompensationArguments) DomainUpdate() JobUpdate {
	return JobUpdate{
		ExpectedVersion:       arguments.ExpectedVersion,
		CompensationSummary:   arguments.CompensationSummary,
		Accommodation:         arguments.Accommodation,
		TransportRequirements: arguments.TransportRequirements,
	}
}

func normalizeWorkbenchRequiredString(field string, value *string, maxRunes int) error {
	normalized, err := normalizeAutomationString(field, *value, maxRunes, true, false)
	if err != nil {
		return err
	}
	*value = normalized
	return nil
}

func validateWorkbenchOperationID(operation WorkbenchOperation, field, id string) error {
	entityType := ""
	prefix := ""
	switch operation {
	case WorkbenchOperationMessagesGet:
		prefix = "event"
	case WorkbenchOperationCandidatesGet:
		entityType = AutomationEntityCandidate
	case WorkbenchOperationJobsGet:
		entityType = AutomationEntityJob
	case WorkbenchOperationLeadsGet:
		entityType = AutomationEntityLead
	case WorkbenchOperationTasksGet:
		entityType = AutomationEntityTask
	case WorkbenchOperationOnboardingsGet:
		entityType = AutomationEntityOnboarding
	case WorkbenchOperationServiceRequestsGet:
		entityType = AutomationEntityServiceRequest
	case WorkbenchOperationServiceQueuesGet:
		prefix = "queue"
	case WorkbenchOperationOperationBatchesGet,
		WorkbenchOperationOperationBatchesPreview,
		WorkbenchOperationOperationBatchesExecute:
		prefix = "batch"
	case WorkbenchOperationFundsGet,
		WorkbenchOperationFundsLedger,
		WorkbenchOperationFundsConfirmCommitment:
		entityType = AutomationEntityFundCallback
	case WorkbenchOperationAutomationSessionsGet:
		prefix = "aisession"
	case WorkbenchOperationAutomationRunsGet:
		prefix = "airun"
	case WorkbenchOperationAutomationActionsGet:
		prefix = "aiact"
	case WorkbenchOperationCandidatesAnonymize:
		entityType = AutomationEntityCandidate
	case WorkbenchOperationForwardRulesDelete:
		prefix = "rule"
	default:
		return nil
	}
	valid := false
	if entityType != "" {
		valid = isAutomationEntityID(id, entityType)
	} else {
		valid = workbenchResourceID(id, prefix)
	}
	if !valid {
		return NewAutomationValidationError(field, "does not match the expected resource type")
	}
	return nil
}

func workbenchResourceID(value, prefix string) bool {
	if prefix == "aisession" || prefix == "airun" {
		return strings.HasPrefix(value, prefix+"_") && len(value) > len(prefix)+1
	}
	return automationForwardResourceID(value, prefix)
}

func isKnownWorkbenchOperation(operation WorkbenchOperation) bool {
	for _, tool := range WorkbenchProviderTools() {
		if IsValidWorkbenchOperation(tool, operation) {
			return true
		}
	}
	return false
}

func cloneWorkbenchPage(page *WorkbenchPage) *WorkbenchPage {
	if page == nil {
		return nil
	}
	cloned := *page
	return &cloned
}

func workbenchArgumentsSchema(arguments any) (json.RawMessage, error) {
	schema, err := workbenchSchemaForType(reflect.TypeOf(arguments))
	if err != nil {
		return nil, err
	}
	return json.Marshal(schema)
}

func workbenchSchemaForType(valueType reflect.Type) (map[string]any, error) {
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if valueType == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	switch valueType.Kind() {
	case reflect.Struct:
		properties := make(map[string]any)
		required := make([]string, 0)
		for i := 0; i < valueType.NumField(); i++ {
			field := valueType.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			parts := strings.Split(tag, ",")
			name := parts[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			property, err := workbenchSchemaForType(field.Type)
			if err != nil {
				return nil, err
			}
			properties[name] = property
			optional := false
			for _, option := range parts[1:] {
				if option == "omitempty" {
					optional = true
					break
				}
			}
			if !optional {
				required = append(required, name)
			}
		}
		sort.Strings(required)
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           properties,
			"required":             required,
		}, nil
	case reflect.Slice, reflect.Array:
		item, err := workbenchSchemaForType(valueType.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": item}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Interface:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unsupported workbench schema type %s", valueType)
	}
}

func workbenchCanonicalRequestMatches(
	decoded WorkbenchDecodedToolRequest,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) bool {
	if decoded.Tool != tool || decoded.Request.Operation != operation {
		return false
	}
	raw, err := json.Marshal(arguments)
	return err == nil && bytes.Equal(raw, decoded.CanonicalArguments)
}
