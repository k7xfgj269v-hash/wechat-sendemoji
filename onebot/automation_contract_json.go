package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

func normalizeAutomationStringPointer(field string, value *string, maxRunes int, required, multiline bool) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeAutomationString(field, *value, maxRunes, required, multiline)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func decodeAutomationJSONObject(field string, data []byte, destination any, requiredFields ...string) error {
	if !utf8.Valid(data) {
		return NewAutomationValidationError(field, "must contain valid UTF-8 JSON")
	}
	trimmed := trimAutomationJSONWhitespace(data)
	if len(trimmed) == 0 {
		return NewAutomationValidationError(field, "must contain one JSON object")
	}
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return NewAutomationValidationError(field, "must contain one JSON object")
	}
	if err := rejectAutomationDuplicateJSONKeys(field, trimmed); err != nil {
		return err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return NewAutomationValidationError(field, "contains invalid JSON")
	}
	if err := validateAutomationExactJSONFields(field, trimmed, reflect.TypeOf(destination)); err != nil {
		return err
	}
	for _, required := range requiredFields {
		value, exists := object[required]
		if !exists {
			return NewAutomationValidationError(field+"."+required, "is required")
		}
		if bytes.Equal(trimAutomationJSONWhitespace(value), []byte("null")) {
			return NewAutomationValidationError(field+"."+required, "must not be null")
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewAutomationValidationError(field, "contains an unknown or invalid field")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return NewAutomationValidationError(field, "contains trailing JSON")
	}
	return nil
}

func validateAutomationExactJSONFields(field string, data []byte, destinationType reflect.Type) error {
	for destinationType.Kind() == reflect.Pointer {
		destinationType = destinationType.Elem()
	}
	if destinationType == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}

	trimmed := trimAutomationJSONWhitespace(data)
	switch destinationType.Kind() {
	case reflect.Struct:
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return NewAutomationValidationError(field, "contains invalid JSON")
		}
		allowedFields := make(map[string]reflect.Type, destinationType.NumField())
		for i := 0; i < destinationType.NumField(); i++ {
			structField := destinationType.Field(i)
			if structField.PkgPath != "" {
				continue
			}
			name := strings.Split(structField.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = structField.Name
			}
			allowedFields[name] = structField.Type
		}
		for name, value := range object {
			nestedType, allowed := allowedFields[name]
			if !allowed {
				return NewAutomationValidationError(field+"."+name, "contains an unknown field")
			}
			if err := validateAutomationExactJSONFields(field+"."+name, value, nestedType); err != nil {
				return err
			}
		}

	case reflect.Slice, reflect.Array:
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return nil
		}
		var elements []json.RawMessage
		if err := json.Unmarshal(trimmed, &elements); err != nil {
			return NewAutomationValidationError(field, "contains invalid JSON")
		}
		for i, element := range elements {
			if err := validateAutomationExactJSONFields(indexedField(field, i), element, destinationType.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func trimAutomationJSONWhitespace(data []byte) []byte {
	start := 0
	for start < len(data) && isAutomationJSONWhitespace(data[start]) {
		start++
	}
	end := len(data)
	for end > start && isAutomationJSONWhitespace(data[end-1]) {
		end--
	}
	return data[start:end]
}

func isAutomationJSONWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func rejectAutomationNullField(field string, data []byte, path ...string) error {
	current := json.RawMessage(data)
	for i, name := range path {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil
		}
		value, exists := object[name]
		if !exists {
			return nil
		}
		if bytes.Equal(trimAutomationJSONWhitespace(value), []byte("null")) {
			return NewAutomationValidationError(field+"."+strings.Join(path[:i+1], "."), "must not be null")
		}
		current = value
	}
	return nil
}

func validateAutomationJobMatchFields(field string, data []byte) error {
	var object struct {
		Matches []json.RawMessage `json:"matches"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return nil
	}
	for i, rawMatch := range object.Matches {
		var ignored AutomationJobMatch
		if err := decodeAutomationJSONObject(
			indexedField(field+".matches", i),
			rawMatch,
			&ignored,
			"job",
			"score",
			"rationale",
		); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomationJobCreateFields(field string, data []byte) error {
	var object struct {
		Job json.RawMessage `json:"job"`
	}
	if err := json.Unmarshal(data, &object); err != nil || len(object.Job) == 0 {
		return nil
	}
	var ignored JobDraft
	return decodeAutomationJSONObject(
		field+".job",
		object.Job,
		&ignored,
		"title",
		"company",
		"requirements",
		"headcount",
		"required_documents",
	)
}

func rejectAutomationDuplicateJSONKeys(field string, data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkAutomationJSONValue(decoder, field); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return NewAutomationValidationError(field, "contains trailing JSON")
		}
		return NewAutomationValidationError(field, "contains invalid trailing JSON")
	}
	return nil
}

func walkAutomationJSONValue(decoder *json.Decoder, field string) error {
	token, err := decoder.Token()
	if err != nil {
		return NewAutomationValidationError(field, "contains invalid JSON")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}

	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return NewAutomationValidationError(field, "contains invalid JSON")
			}
			key, ok := keyToken.(string)
			if !ok {
				return NewAutomationValidationError(field, "contains an invalid object key")
			}
			if _, exists := keys[key]; exists {
				return NewAutomationValidationError(field+"."+key, "contains a duplicate object key")
			}
			keys[key] = struct{}{}
			if err := walkAutomationJSONValue(decoder, field+"."+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return NewAutomationValidationError(field, "contains an invalid JSON object")
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := walkAutomationJSONValue(decoder, indexedField(field, index)); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return NewAutomationValidationError(field, "contains an invalid JSON array")
		}
	default:
		return NewAutomationValidationError(field, "contains an invalid JSON delimiter")
	}
	return nil
}

func normalizeAutomationString(field, value string, maxRunes int, required, multiline bool) (string, error) {
	normalized, err := normalizeCRMString(field, value, maxRunes, required, multiline)
	if err != nil {
		return "", asAutomationValidationError(err)
	}
	return normalized, nil
}

func normalizeAutomationStringList(field string, values []string, maxCount, maxRunes int) ([]string, error) {
	normalized, err := normalizeCRMStringList(field, values, maxCount, maxRunes)
	if err != nil {
		return nil, asAutomationValidationError(err)
	}
	return normalized, nil
}

func validateAutomationTime(field string, value time.Time) error {
	return asAutomationValidationError(validateCRMTime(field, value))
}

func asAutomationValidationError(err error) error {
	if err == nil {
		return nil
	}
	var validationError *CRMValidationError
	if errors.As(err, &validationError) {
		return NewAutomationValidationError(validationError.Field, validationError.Message)
	}
	return err
}
