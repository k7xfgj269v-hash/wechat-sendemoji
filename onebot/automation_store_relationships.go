package main

import (
	"sort"
	"strconv"
)

func validateAutomationActionRelationships(
	state *AutomationState,
	runs map[string]*AutomationRun,
	actions map[string]*AutomationAction,
) error {
	providerCalls := make(map[string]*AutomationAction, len(actions))
	for _, action := range actions {
		providerCalls[action.RunID+"\x00"+action.ProviderCallID] = action
	}
	for i := range state.Actions {
		action := &state.Actions[i]
		field := automationIndexedField("actions", i)
		run, exists := runs[action.RunID]
		if !exists {
			return NewAutomationValidationError(field+".run_id", "references a missing run")
		}
		if action.PolicyVersion > state.Policy.Version {
			return NewAutomationValidationError(field+".policy_version", "is newer than the active policy")
		}
		if action.SourceMessage.EventID != run.SourceMessageEventID ||
			action.SourceMessage.MessageKey != run.SourceMessageKey ||
			action.SourceMessage.ExpectedVersion != run.SourceMessageVersion {
			return NewAutomationValidationError(field+".source_message", "does not match the owning run")
		}
		if err := validateAutomationActionPayloadReferences(field, action, providerCalls); err != nil {
			return err
		}
		for j, dependencyID := range action.DependsOnActionIDs {
			dependency, exists := actions[dependencyID]
			itemField := automationIndexedField(field+".depends_on_action_ids", j)
			if !exists {
				return NewAutomationValidationError(itemField, "references a missing action")
			}
			if dependencyID == action.ID {
				return NewAutomationValidationError(itemField, "must not reference the same action")
			}
			if dependency.RunID != action.RunID {
				return NewAutomationValidationError(itemField, "references an action from another run")
			}
		}
		for name, referenceID := range map[string]string{
			"retry_of_action_id":    action.RetryOfActionID,
			"replaces_action_id":    action.ReplacesActionID,
			"replaced_by_action_id": action.ReplacedByActionID,
		} {
			if referenceID == "" {
				continue
			}
			reference, exists := actions[referenceID]
			if !exists {
				return NewAutomationValidationError(field+"."+name, "references a missing action")
			}
			if referenceID == action.ID {
				return NewAutomationValidationError(field+"."+name, "must not reference the same action")
			}
			if reference.RunID != action.RunID {
				return NewAutomationValidationError(field+"."+name, "references an action from another run")
			}
		}
		if action.ReplacesActionID != "" && actions[action.ReplacesActionID].ReplacedByActionID != action.ID {
			return NewAutomationValidationError(field+".replaces_action_id", "does not have a matching replacement link")
		}
		if action.ReplacedByActionID != "" && actions[action.ReplacedByActionID].ReplacesActionID != action.ID {
			return NewAutomationValidationError(field+".replaced_by_action_id", "does not have a matching replacement link")
		}
	}
	return nil
}

func validateAutomationActionPayloadReferences(
	field string,
	action *AutomationAction,
	providerCalls map[string]*AutomationAction,
) error {
	references, err := AutomationToolCallReferences(AutomationToolCall{
		Tool:      action.Tool,
		Arguments: action.Payload,
	})
	if err != nil {
		return NewAutomationValidationError(field+".payload", "does not match the persisted tool schema")
	}

	expectedPreconditions := make(map[string]uint64)
	expectedDependencies := make(map[string]struct{})
	for _, reference := range references {
		switch {
		case reference.Reference.ID != "":
			key := reference.EntityType + "\x00" + reference.Reference.ID
			if version, exists := expectedPreconditions[key]; exists && version != reference.Reference.ExpectedVersion {
				return NewAutomationValidationError(field+".payload", "contains conflicting existing-entity versions")
			}
			expectedPreconditions[key] = reference.Reference.ExpectedVersion
		case reference.Reference.FromCall != "":
			dependency, exists := providerCalls[action.RunID+"\x00"+reference.Reference.FromCall]
			if !exists {
				return NewAutomationValidationError(field+".payload", "references a missing provider call")
			}
			if dependency.ID == action.ID {
				return NewAutomationValidationError(field+".payload", "must not reference the same action")
			}
			if AutomationToolOutputEntityType(dependency.Tool) != reference.EntityType {
				return NewAutomationValidationError(field+".payload", "references a provider call with the wrong entity type")
			}
			expectedDependencies[dependency.ID] = struct{}{}
		}
	}

	if len(expectedPreconditions) != len(action.Preconditions) {
		return NewAutomationValidationError(field+".preconditions", "does not exactly match payload entity references")
	}
	for i := range action.Preconditions {
		precondition := &action.Preconditions[i]
		key := precondition.EntityType + "\x00" + precondition.EntityID
		version, exists := expectedPreconditions[key]
		if !exists || version != precondition.ExpectedVersion {
			return NewAutomationValidationError(
				automationIndexedField(field+".preconditions", i),
				"does not exactly match a payload entity reference",
			)
		}
	}

	if len(expectedDependencies) != len(action.DependsOnActionIDs) {
		return NewAutomationValidationError(field+".depends_on_action_ids", "does not exactly match payload prior-call references")
	}
	for i, dependencyID := range action.DependsOnActionIDs {
		if _, exists := expectedDependencies[dependencyID]; !exists {
			return NewAutomationValidationError(
				automationIndexedField(field+".depends_on_action_ids", i),
				"does not exactly match a payload prior-call reference",
			)
		}
	}
	return nil
}

func validateAutomationApprovalRelationships(
	state *AutomationState,
	actions map[string]*AutomationAction,
) error {
	for i := range state.Approvals {
		approval := &state.Approvals[i]
		field := automationIndexedField("approvals", i)
		action, exists := actions[approval.ActionID]
		if !exists {
			return NewAutomationValidationError(field+".action_id", "references a missing action")
		}
		if approval.ActionVersion > action.Version {
			return NewAutomationValidationError(field+".action_version", "is newer than the referenced action")
		}
		if approval.ReplacementActionID != "" {
			replacement, exists := actions[approval.ReplacementActionID]
			if !exists {
				return NewAutomationValidationError(field+".replacement_action_id", "references a missing action")
			}
			if replacement.RunID != action.RunID {
				return NewAutomationValidationError(field+".replacement_action_id", "references an action from another run")
			}
		}
	}
	return nil
}

func validateAutomationOutboxRelationships(
	state *AutomationState,
	actions map[string]*AutomationAction,
) error {
	actionEntries := make(map[string]string, len(state.Outbox))
	for i := range state.Outbox {
		entry := &state.Outbox[i]
		field := automationIndexedField("outbox", i)
		action, exists := actions[entry.ActionID]
		if !exists {
			return NewAutomationValidationError(field+".action_id", "references a missing action")
		}
		if previous, exists := actionEntries[entry.ActionID]; exists {
			return NewAutomationValidationError(field+".action_id", "duplicates outbox entry "+previous)
		}
		actionEntries[entry.ActionID] = entry.ID
		if !automationActionSupportsTextOutbox(*action) {
			return NewAutomationValidationError(field+".action_id", "must reference a text reply action")
		}
		if action.Recipient == nil ||
			action.Recipient.ChatType != automationOutboxChatType(*entry) ||
			action.Recipient.PeerID != entry.PeerID {
			return NewAutomationValidationError(field+".peer_id", "does not match the action recipient")
		}
		expectedKey := "send:" + action.ID
		if entry.IdempotencyKey != expectedKey {
			return NewAutomationValidationError(field+".idempotency_key", "does not match the action send key")
		}
		record, exists := state.Idempotency[entry.IdempotencyKey]
		if !exists || record.Kind != "outbox" || record.ResourceID != entry.ID {
			return NewAutomationValidationError(field+".idempotency_key", "does not map to the outbox entry")
		}
	}
	return nil
}

func validateAutomationAuditRelationships(
	state *AutomationState,
	runs map[string]*AutomationRun,
	actions map[string]*AutomationAction,
	outbox map[string]*AutomationOutboxEntry,
) error {
	for i := range state.PendingAudit {
		record := &state.PendingAudit[i]
		field := automationIndexedField("pending_audit", i)
		if record.RunID != "" {
			if _, exists := runs[record.RunID]; !exists {
				return NewAutomationValidationError(field+".run_id", "references a missing run")
			}
		}
		if record.ActionID != "" {
			if _, exists := actions[record.ActionID]; !exists {
				return NewAutomationValidationError(field+".action_id", "references a missing action")
			}
		}
		if record.OutboxID != "" {
			if _, exists := outbox[record.OutboxID]; !exists {
				return NewAutomationValidationError(field+".outbox_id", "references a missing outbox entry")
			}
		}
	}
	return nil
}

func validateAutomationIdempotency(state *AutomationState) error {
	expected := make(map[string]AutomationIdempotencyRecord, len(state.Runs)+len(state.Outbox))
	for i := range state.Runs {
		run := &state.Runs[i]
		if _, exists := expected[run.IdempotencyKey]; exists {
			return NewAutomationValidationError(
				automationIndexedField("runs", i)+".idempotency_key",
				"duplicates another canonical idempotency key",
			)
		}
		expected[run.IdempotencyKey] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: run.ID,
		}
	}
	for i := range state.Outbox {
		entry := &state.Outbox[i]
		key := "send:" + entry.ActionID
		if entry.IdempotencyKey != key {
			return NewAutomationValidationError(
				automationIndexedField("outbox", i)+".idempotency_key",
				"does not match the canonical outbox key",
			)
		}
		if _, exists := expected[key]; exists {
			return NewAutomationValidationError(
				automationIndexedField("outbox", i)+".idempotency_key",
				"duplicates another canonical idempotency key",
			)
		}
		expected[key] = AutomationIdempotencyRecord{
			Kind:       "outbox",
			ResourceID: entry.ID,
		}
	}

	if len(state.Idempotency) != len(expected) {
		return NewAutomationValidationError("idempotency", "contains an extra, orphan, or missing mapping")
	}
	keys := make([]string, 0, len(state.Idempotency))
	for key := range state.Idempotency {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record := state.Idempotency[key]
		field := "idempotency[" + strconv.Quote(key) + "]"
		if err := validateStoredAutomationString(field+".key", key, 512, true, false); err != nil {
			return err
		}
		if err := validateStoredAutomationString(field+".kind", record.Kind, 120, true, false); err != nil {
			return err
		}
		if err := validateStoredAutomationString(field+".resource_id", record.ResourceID, 80, true, false); err != nil {
			return err
		}
		expectedRecord, exists := expected[key]
		if !exists {
			return NewAutomationValidationError(field, "is not a canonical run or outbox mapping")
		}
		if record.Kind != expectedRecord.Kind || record.ResourceID != expectedRecord.ResourceID {
			return NewAutomationValidationError(field, "does not exactly match its canonical resource")
		}
		if err := validateAutomationStoreTime(field+".created_at", record.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomationDependencyGraph(actions []AutomationAction) error {
	indegree := make(map[string]int, len(actions))
	dependents := make(map[string][]string, len(actions))
	fields := make(map[string]string, len(actions))
	for i := range actions {
		action := &actions[i]
		indegree[action.ID] = len(action.DependsOnActionIDs)
		fields[action.ID] = automationIndexedField("actions", i) + ".depends_on_action_ids"
		for _, dependencyID := range action.DependsOnActionIDs {
			dependents[dependencyID] = append(dependents[dependencyID], action.ID)
		}
	}
	queue := make([]string, 0, len(actions))
	for actionID, count := range indegree {
		if count == 0 {
			queue = append(queue, actionID)
		}
	}
	visited := 0
	for len(queue) > 0 {
		actionID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		for _, dependentID := range dependents[actionID] {
			indegree[dependentID]--
			if indegree[dependentID] == 0 {
				queue = append(queue, dependentID)
			}
		}
	}
	if visited == len(actions) {
		return nil
	}
	cyclicIDs := make([]string, 0)
	for actionID, count := range indegree {
		if count > 0 {
			cyclicIDs = append(cyclicIDs, actionID)
		}
	}
	sort.Strings(cyclicIDs)
	return NewAutomationValidationError(fields[cyclicIDs[0]], "contains a dependency cycle")
}
