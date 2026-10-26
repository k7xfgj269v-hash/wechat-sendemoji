package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const AutomationPromptMaxRevisions = 1000

type AutomationPromptRevision struct {
	ID        string    `json:"id"`
	Number    uint64    `json:"number"`
	Template  string    `json:"template"`
	SHA256    string    `json:"sha256"`
	CreatedBy string    `json:"created_by"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type AutomationPromptComparison struct {
	From    AutomationPromptRevision `json:"from"`
	To      AutomationPromptRevision `json:"to"`
	Changed bool                     `json:"changed"`
}

type AutomationPromptSaveRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Template        string `json:"template"`
	CreatedBy       string `json:"created_by"`
	Reason          string `json:"reason"`
}

type AutomationPromptActivationRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	CreatedBy       string `json:"created_by"`
	Reason          string `json:"reason"`
}

type AutomationPromptEvaluationRequest struct {
	CreatedBy string `json:"created_by"`
}

func cloneAutomationPromptRevisions(revisions []AutomationPromptRevision) []AutomationPromptRevision {
	if revisions == nil {
		return []AutomationPromptRevision{}
	}
	return append([]AutomationPromptRevision(nil), revisions...)
}

func ensureAutomationPromptHistory(config AutomationProviderConfig, fallback time.Time) AutomationProviderConfig {
	if len(config.PromptRevisions) != 0 {
		return config
	}
	template := strings.TrimSpace(config.PromptTemplate)
	if template == "" {
		return config
	}
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	revision := AutomationPromptRevision{
		ID:        automationPromptRevisionID(1),
		Number:    1,
		Template:  template,
		SHA256:    automationPromptSHA256(template),
		CreatedBy: "system",
		Reason:    "initial prompt",
		CreatedAt: fallback.UTC(),
	}
	config.PromptRevisions = []AutomationPromptRevision{revision}
	config.ActivePromptRevisionID = revision.ID
	return config
}

func validateAutomationPromptHistory(config AutomationProviderConfig) error {
	if len(config.PromptRevisions) == 0 {
		return NewAutomationValidationError("prompt_revisions", "must contain the active prompt")
	}
	if len(config.PromptRevisions) > AutomationPromptMaxRevisions {
		return NewAutomationValidationError("prompt_revisions", "contains too many revisions")
	}
	seenIDs := make(map[string]struct{}, len(config.PromptRevisions))
	seenNumbers := make(map[uint64]struct{}, len(config.PromptRevisions))
	var previousNumber uint64
	for i, revision := range config.PromptRevisions {
		field := fmt.Sprintf("prompt_revisions[%d]", i)
		if revision.ID == "" || len(revision.ID) > 80 {
			return NewAutomationValidationError(field+".id", "is required and must not exceed 80 bytes")
		}
		if revision.Number == 0 || revision.Number <= previousNumber {
			return NewAutomationValidationError(field+".number", "must be strictly increasing")
		}
		if _, exists := seenIDs[revision.ID]; exists {
			return NewAutomationValidationError(field+".id", "duplicates another revision")
		}
		if _, exists := seenNumbers[revision.Number]; exists {
			return NewAutomationValidationError(field+".number", "duplicates another revision")
		}
		if strings.TrimSpace(revision.Template) != revision.Template ||
			revision.Template == "" || len(revision.Template) > 64<<10 {
			return NewAutomationValidationError(field+".template", "is invalid")
		}
		if revision.SHA256 != automationPromptSHA256(revision.Template) {
			return NewAutomationValidationError(field+".sha256", "does not match the template")
		}
		if revision.CreatedAt.IsZero() {
			return NewAutomationValidationError(field+".created_at", "is required")
		}
		if err := validateAutomationPromptMetadata(field+".created_by", revision.CreatedBy, true); err != nil {
			return err
		}
		if err := validateAutomationPromptMetadata(field+".reason", revision.Reason, true); err != nil {
			return err
		}
		seenIDs[revision.ID] = struct{}{}
		seenNumbers[revision.Number] = struct{}{}
		previousNumber = revision.Number
	}
	active, ok := automationPromptRevisionByID(config.PromptRevisions, config.ActivePromptRevisionID)
	if !ok {
		return NewAutomationValidationError("active_prompt_revision_id", "does not reference a prompt revision")
	}
	if config.PromptTemplate != active.Template {
		return NewAutomationValidationError("prompt_template", "does not match the active prompt revision")
	}
	return nil
}

func (s *AutomationProviderConfigStore) PromptRevisions() []AutomationPromptRevision {
	if s == nil {
		return []AutomationPromptRevision{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAutomationPromptRevisions(s.config.PromptRevisions)
}

func (s *AutomationProviderConfigStore) PromptRevision(id string) (AutomationPromptRevision, bool) {
	if s == nil {
		return AutomationPromptRevision{}, false
	}
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return automationPromptRevisionByID(s.config.PromptRevisions, id)
}

func (s *AutomationProviderConfigStore) ComparePromptRevisions(fromID, toID string) (AutomationPromptComparison, error) {
	if s == nil {
		return AutomationPromptComparison{}, errors.New("automation prompt store is unavailable")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	from, ok := automationPromptRevisionByID(s.config.PromptRevisions, strings.TrimSpace(fromID))
	if !ok {
		return AutomationPromptComparison{}, errors.New("automation prompt revision not found")
	}
	to, ok := automationPromptRevisionByID(s.config.PromptRevisions, strings.TrimSpace(toID))
	if !ok {
		return AutomationPromptComparison{}, errors.New("automation prompt revision not found")
	}
	return AutomationPromptComparison{From: from, To: to, Changed: from.SHA256 != to.SHA256}, nil
}

func (s *AutomationProviderConfigStore) SavePromptRevision(
	expectedVersion uint64,
	template string,
	createdBy string,
	reason string,
) (AutomationPromptRevision, AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedVersion != s.config.Version {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	template = strings.TrimSpace(template)
	if template == "" || len(template) > 64<<10 {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{},
			NewAutomationValidationError("template", "is required and must not exceed 65536 bytes")
	}
	if err := validateAutomationPromptMetadata("created_by", createdBy, false); err != nil {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{}, err
	}
	if err := validateAutomationPromptMetadata("reason", reason, false); err != nil {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{}, err
	}
	if active, ok := automationPromptRevisionByID(s.config.PromptRevisions, s.config.ActivePromptRevisionID); ok &&
		active.Template == template {
		return active, s.publicLocked(s.config, s.apiKey), nil
	}
	config := cloneAutomationProviderConfig(s.config)
	revision, err := appendAutomationPromptRevision(&config, template, createdBy, reason, time.Now().UTC())
	if err != nil {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{}, err
	}
	config.Version++
	config.UpdatedAt = revision.CreatedAt
	if err := s.persistLocked(config, s.apiKey); err != nil {
		return AutomationPromptRevision{}, AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.persisted = true
	return revision, s.publicLocked(config, s.apiKey), nil
}

func (s *AutomationProviderConfigStore) ActivatePromptRevision(
	expectedVersion uint64,
	revisionID string,
	createdBy string,
	reason string,
) (AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedVersion != s.config.Version {
		return AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	if err := validateAutomationPromptMetadata("created_by", createdBy, false); err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	if err := validateAutomationPromptMetadata("reason", reason, false); err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	revision, ok := automationPromptRevisionByID(s.config.PromptRevisions, strings.TrimSpace(revisionID))
	if !ok {
		return AutomationProviderConfigPublic{}, errors.New("automation prompt revision not found")
	}
	if revision.ID == s.config.ActivePromptRevisionID {
		return s.publicLocked(s.config, s.apiKey), nil
	}
	config := cloneAutomationProviderConfig(s.config)
	config.Version++
	config.PromptTemplate = revision.Template
	config.ActivePromptRevisionID = revision.ID
	config.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(config, s.apiKey); err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.persisted = true
	return s.publicLocked(config, s.apiKey), nil
}

func appendAutomationPromptRevision(
	config *AutomationProviderConfig,
	template string,
	createdBy string,
	reason string,
	now time.Time,
) (AutomationPromptRevision, error) {
	if len(config.PromptRevisions) >= AutomationPromptMaxRevisions {
		return AutomationPromptRevision{}, NewAutomationValidationError("prompt_revisions", "contains too many revisions")
	}
	if err := validateAutomationPromptMetadata("created_by", createdBy, false); err != nil {
		return AutomationPromptRevision{}, err
	}
	if err := validateAutomationPromptMetadata("reason", reason, false); err != nil {
		return AutomationPromptRevision{}, err
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		createdBy = "operator"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "prompt updated"
	}
	number := uint64(1)
	if len(config.PromptRevisions) != 0 {
		number = config.PromptRevisions[len(config.PromptRevisions)-1].Number + 1
	}
	revision := AutomationPromptRevision{
		ID:        automationPromptRevisionID(number),
		Number:    number,
		Template:  template,
		SHA256:    automationPromptSHA256(template),
		CreatedBy: createdBy,
		Reason:    reason,
		CreatedAt: now.UTC(),
	}
	config.PromptTemplate = template
	config.ActivePromptRevisionID = revision.ID
	config.PromptRevisions = append(config.PromptRevisions, revision)
	return revision, nil
}

func automationPromptRevisionByID(
	revisions []AutomationPromptRevision,
	id string,
) (AutomationPromptRevision, bool) {
	for _, revision := range revisions {
		if revision.ID == id {
			return revision, true
		}
	}
	return AutomationPromptRevision{}, false
}

func automationPromptRevisionID(number uint64) string {
	return fmt.Sprintf("prompt_%08d", number)
}

func automationPromptSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validateAutomationPromptMetadata(field, value string, allowDefault bool) error {
	value = strings.TrimSpace(value)
	if value == "" && allowDefault {
		return NewAutomationValidationError(field, "is required")
	}
	if len(value) > 240 {
		return NewAutomationValidationError(field, "must not exceed 240 bytes")
	}
	return nil
}

func normalizeAutomationAudienceContent(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
