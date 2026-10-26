package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	errForwardRuleNotFound = errors.New("forward rule was not found")
	errForwardRuleDisabled = errors.New("forward rule is disabled")
	errForwardRuleMismatch = errors.New("forward rule does not match the event")
	errForwardRuleCooldown = errors.New("forward rule cooldown is active")
)

type ForwardRuleStore struct {
	mu          sync.Mutex
	crm         *CRMStore
	rules       []ForwardRule
	nextRuleID  uint64
	lastMatched map[string]time.Time
}

func NewForwardRuleStore(stores ...*CRMStore) *ForwardRuleStore {
	var crm *CRMStore
	if len(stores) > 0 {
		crm = stores[0]
	}
	return &ForwardRuleStore{
		crm:         crm,
		rules:       []ForwardRule{},
		lastMatched: make(map[string]time.Time),
	}
}

func (s *ForwardRuleStore) List() []ForwardRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return forwardRuleCloneList(s.forwardRuleRulesLocked())
}

func (s *ForwardRuleStore) Create(groupID string, keywords []string, cooldownSeconds int, enabled bool) (ForwardRule, error) {
	rule := ForwardRule{
		GroupID:         strings.TrimSpace(groupID),
		Keywords:        normalizeKeywords(keywords),
		CooldownSeconds: cooldownSeconds,
		Enabled:         enabled,
	}
	if err := forwardRuleValidate(rule); err != nil {
		return ForwardRule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.crm == nil {
		if len(s.rules) >= crmMaxRuleCount {
			return ForwardRule{}, NewCRMValidationError("forwarding_rules", "entity limit exceeded")
		}
		s.nextRuleID++
		rule.ID = fmt.Sprintf("rule_%08d", s.nextRuleID)
		s.rules = append(s.rules, cloneForwardRule(rule))
		return cloneForwardRule(rule), nil
	}

	var created ForwardRule
	err := s.crm.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindRule)
		if err != nil {
			return err
		}
		rule.ID = id
		state.ForwardingRules = append(state.ForwardingRules, cloneForwardRule(rule))
		created = cloneForwardRule(rule)
		return nil
	})
	if err != nil {
		return ForwardRule{}, err
	}
	return cloneForwardRule(created), nil
}

func (s *ForwardRuleStore) Update(id string, req updateForwardRuleRequest) (ForwardRule, bool, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.crm == nil {
		for i := range s.rules {
			if s.rules[i].ID != id {
				continue
			}
			updated, err := forwardRuleApplyUpdate(s.rules[i], req)
			if err != nil {
				return ForwardRule{}, true, err
			}
			s.rules[i] = cloneForwardRule(updated)
			return cloneForwardRule(updated), true, nil
		}
		return ForwardRule{}, false, nil
	}

	if !forwardRuleContainsID(s.crm.Snapshot().ForwardingRules, id) {
		return ForwardRule{}, false, nil
	}

	found := false
	var updated ForwardRule
	err := s.crm.Update(func(state *CRMState, _ *IDAllocator) error {
		for i := range state.ForwardingRules {
			if state.ForwardingRules[i].ID != id {
				continue
			}
			found = true
			next, err := forwardRuleApplyUpdate(state.ForwardingRules[i], req)
			if err != nil {
				return err
			}
			state.ForwardingRules[i] = cloneForwardRule(next)
			updated = cloneForwardRule(next)
			return nil
		}
		return nil
	})
	if err != nil {
		return ForwardRule{}, found, err
	}
	return cloneForwardRule(updated), found, nil
}

func (s *ForwardRuleStore) Delete(id string) bool {
	deleted, _ := s.forwardRuleDelete(id)
	return deleted
}

func (s *ForwardRuleStore) forwardRuleDelete(id string) (bool, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.crm == nil {
		for i := range s.rules {
			if s.rules[i].ID != id {
				continue
			}
			copy(s.rules[i:], s.rules[i+1:])
			s.rules = s.rules[:len(s.rules)-1]
			delete(s.lastMatched, id)
			return true, nil
		}
		return false, nil
	}

	if !forwardRuleContainsID(s.crm.Snapshot().ForwardingRules, id) {
		return false, nil
	}

	found := false
	err := s.crm.Update(func(state *CRMState, _ *IDAllocator) error {
		for i := range state.ForwardingRules {
			if state.ForwardingRules[i].ID != id {
				continue
			}
			found = true
			copy(state.ForwardingRules[i:], state.ForwardingRules[i+1:])
			state.ForwardingRules = state.ForwardingRules[:len(state.ForwardingRules)-1]
			return nil
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if found {
		delete(s.lastMatched, id)
	}
	return found, nil
}

func (s *ForwardRuleStore) MatchAndReserve(rec *ForwardRecord, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	rules := s.forwardRuleRulesLocked()
	if len(rules) == 0 {
		return true
	}
	if rec == nil || rec.GroupID == "" {
		return false
	}

	matched := false
	for i := range rules {
		rule := &rules[i]
		if !forwardRuleMatches(*rule, rec) {
			continue
		}
		lastMatched := s.lastMatched[rule.ID]
		if rule.CooldownSeconds > 0 && !lastMatched.IsZero() {
			cooldown := time.Duration(rule.CooldownSeconds) * time.Second
			if now.Sub(lastMatched) < cooldown {
				continue
			}
		}
		s.lastMatched[rule.ID] = now
		matched = true
	}
	return matched
}

func (s *ForwardRuleStore) MatchRuleAndReserve(ruleID string, rec *ForwardRecord, now time.Time) (ForwardRule, error) {
	return s.MatchRule(ruleID, rec, now, true)
}

func (s *ForwardRuleStore) MatchRule(ruleID string, rec *ForwardRecord, now time.Time, reserve bool) (ForwardRule, error) {
	if s == nil {
		return ForwardRule{}, errForwardRuleNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ruleID = strings.TrimSpace(ruleID)
	for _, rule := range s.forwardRuleRulesLocked() {
		if rule.ID != ruleID {
			continue
		}
		if !rule.Enabled {
			return ForwardRule{}, errForwardRuleDisabled
		}
		if !forwardRuleMatches(rule, rec) {
			return ForwardRule{}, errForwardRuleMismatch
		}
		lastMatched := s.lastMatched[rule.ID]
		if rule.CooldownSeconds > 0 && !lastMatched.IsZero() {
			cooldown := time.Duration(rule.CooldownSeconds) * time.Second
			if now.Sub(lastMatched) < cooldown {
				return ForwardRule{}, errForwardRuleCooldown
			}
		}
		if reserve {
			s.lastMatched[rule.ID] = now
		}
		return cloneForwardRule(rule), nil
	}
	return ForwardRule{}, errForwardRuleNotFound
}

func forwardRuleMatches(rule ForwardRule, rec *ForwardRecord) bool {
	return rec != nil &&
		rec.GroupID != "" &&
		rule.Enabled &&
		(rule.GroupID == "" || rule.GroupID == rec.GroupID) &&
		matchesAnyKeyword(rec.Text, rule.Keywords)
}

func (s *ForwardRuleStore) forwardRuleRulesLocked() []ForwardRule {
	if s.crm != nil {
		return s.crm.Snapshot().ForwardingRules
	}
	return s.rules
}

func forwardRuleApplyUpdate(rule ForwardRule, req updateForwardRuleRequest) (ForwardRule, error) {
	changed := false
	if req.Keywords != nil {
		rule.Keywords = normalizeKeywords(*req.Keywords)
		changed = true
	}
	if req.CooldownSeconds != nil {
		rule.CooldownSeconds = *req.CooldownSeconds
		changed = true
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
		changed = true
	}
	if !changed {
		return ForwardRule{}, fmt.Errorf("没有可更新的字段")
	}
	if err := forwardRuleValidate(rule); err != nil {
		return ForwardRule{}, err
	}
	return cloneForwardRule(rule), nil
}

func forwardRuleValidate(rule ForwardRule) error {
	if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 86400 {
		return fmt.Errorf("cooldown_seconds 非法")
	}
	return validateForwardRule("forwarding_rule", &rule)
}

func forwardRuleContainsID(rules []ForwardRule, id string) bool {
	for i := range rules {
		if rules[i].ID == id {
			return true
		}
	}
	return false
}

func forwardRuleCloneList(rules []ForwardRule) []ForwardRule {
	cloned := make([]ForwardRule, len(rules))
	for i := range rules {
		cloned[i] = cloneForwardRule(rules[i])
	}
	return cloned
}

func matchesAnyKeyword(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func normalizeKeywords(keywords []string) []string {
	result := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if _, ok := seen[keyword]; ok {
			continue
		}
		seen[keyword] = struct{}{}
		result = append(result, keyword)
	}
	return result
}

func cloneForwardRule(rule ForwardRule) ForwardRule {
	rule.Keywords = append([]string{}, rule.Keywords...)
	return rule
}
