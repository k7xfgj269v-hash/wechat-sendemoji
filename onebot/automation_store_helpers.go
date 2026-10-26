package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func validateAutomationStoredID(
	field string,
	id string,
	prefix string,
	counter uint64,
	allIDs map[string]string,
) error {
	if err := validateStoredAutomationString(field, id, 80, true, false); err != nil {
		return err
	}
	expectedLength := len(prefix) + 1 + 8
	if len(id) != expectedLength || !strings.HasPrefix(id, prefix+"_") {
		return NewAutomationValidationError(field, "has an invalid format")
	}
	numberText := id[len(prefix)+1:]
	for _, character := range numberText {
		if character < '0' || character > '9' {
			return NewAutomationValidationError(field, "has an invalid format")
		}
	}
	number, err := strconv.ParseUint(numberText, 10, 64)
	if err != nil || number == 0 || number > automationMaxIDCounter {
		return NewAutomationValidationError(field, "has an invalid counter")
	}
	if number > counter {
		return NewAutomationValidationError(field, "is ahead of its counter")
	}
	if previous, exists := allIDs[id]; exists {
		return NewAutomationValidationError(field, "duplicates "+previous)
	}
	allIDs[id] = field
	return nil
}

func validateAutomationCRMEntityID(field, id, entityType string) error {
	prefix := ""
	switch entityType {
	case AutomationEntityCandidate:
		prefix = "cand"
	case AutomationEntityJob:
		prefix = "job"
	case AutomationEntityLead:
		prefix = "lead"
	case AutomationEntityTask:
		prefix = "task"
	case AutomationEntityNote:
		prefix = "note"
	default:
		return NewAutomationValidationError(field, "has an invalid entity type")
	}
	if err := validateStoredAutomationString(field, id, 80, true, false); err != nil {
		return err
	}
	expectedLength := len(prefix) + 1 + 8
	if len(id) != expectedLength || !strings.HasPrefix(id, prefix+"_") {
		return NewAutomationValidationError(field, "has an invalid format")
	}
	number, err := strconv.ParseUint(id[len(prefix)+1:], 10, 64)
	if err != nil || number == 0 || number > automationMaxIDCounter {
		return NewAutomationValidationError(field, "has an invalid counter")
	}
	return nil
}

func validateStoredAutomationString(
	field string,
	value string,
	maxRunes int,
	required bool,
	multiline bool,
) error {
	normalized, err := normalizeAutomationString(field, value, maxRunes, required, multiline)
	if err != nil {
		return err
	}
	if value != normalized {
		return NewAutomationValidationError(field, "must be trimmed")
	}
	return nil
}

func validateStoredAutomationStringList(field string, values []string, maxCount, maxRunes int) error {
	if values == nil {
		return NewAutomationValidationError(field, "must not be null")
	}
	normalized, err := normalizeAutomationStringList(field, values, maxCount, maxRunes)
	if err != nil {
		return err
	}
	for i := range values {
		if values[i] != normalized[i] {
			return NewAutomationValidationError(automationIndexedField(field, i), "must be trimmed")
		}
	}
	return nil
}

func validateAutomationConfidence(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return NewAutomationValidationError(field, "must be finite and between 0 and 1")
	}
	return nil
}

func validateAutomationStoreTime(field string, value time.Time) error {
	if value.IsZero() {
		return NewAutomationValidationError(field, "is required")
	}
	_, offset := value.Zone()
	if offset != 0 {
		return NewAutomationValidationError(field, "must be UTC")
	}
	return nil
}

func validateOptionalAutomationTime(field string, value *time.Time) error {
	if value == nil {
		return nil
	}
	return validateAutomationStoreTime(field, *value)
}

func validateAutomationRawJSON(field string, value json.RawMessage, maxBytes int, required bool) error {
	if len(value) == 0 {
		if required {
			return NewAutomationValidationError(field, "is required")
		}
		return nil
	}
	if len(value) > maxBytes {
		return NewAutomationValidationError(field, fmt.Sprintf("must be at most %d bytes", maxBytes))
	}
	if !utf8.Valid(value) || !json.Valid(value) {
		return NewAutomationValidationError(field, "must contain one valid JSON value")
	}
	trimmed := trimAutomationJSONWhitespace(value)
	if bytes.Equal(trimmed, []byte("null")) {
		return NewAutomationValidationError(field, "must not be null")
	}
	return nil
}

func validateAutomationExternalID(field, id string) error {
	if id == "" {
		return nil
	}
	if err := validateStoredAutomationString(field, id, 80, true, false); err != nil {
		return err
	}
	separator := strings.LastIndexByte(id, '_')
	if separator <= 0 || len(id)-separator-1 != 8 {
		return NewAutomationValidationError(field, "has an invalid format")
	}
	for _, character := range id[:separator] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return NewAutomationValidationError(field, "has an invalid format")
		}
	}
	number, err := strconv.ParseUint(id[separator+1:], 10, 64)
	if err != nil || number == 0 || number > automationMaxIDCounter {
		return NewAutomationValidationError(field, "has an invalid counter")
	}
	return nil
}

func automationSessionByID(state *AutomationState, id string) *AutomationSession {
	for i := range state.Sessions {
		if state.Sessions[i].ID == id {
			return &state.Sessions[i]
		}
	}
	return nil
}

func automationTurnByID(state *AutomationState, id string) *AutomationTurn {
	for i := range state.Turns {
		if state.Turns[i].ID == id {
			return &state.Turns[i]
		}
	}
	return nil
}

func isValidAutomationEntityType(entityType string) bool {
	switch entityType {
	case AutomationEntityCandidate,
		AutomationEntityJob,
		AutomationEntityLead,
		AutomationEntityTask,
		AutomationEntityNote:
		return true
	default:
		return false
	}
}

func isAutomationLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func automationIndexedField(field string, index int) string {
	return field + "[" + strconv.Itoa(index) + "]"
}

func totalAutomationEntities(state *AutomationState) int {
	return len(state.Sessions) +
		len(state.Turns) +
		len(state.Runs) +
		len(state.Actions) +
		len(state.Results) +
		len(state.Approvals) +
		len(state.Outbox) +
		len(state.PendingAudit) +
		len(state.PermissionRequests) +
		len(state.PermissionRules)
}
