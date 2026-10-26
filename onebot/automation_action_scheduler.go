package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	AutomationAuditEventActionBlocked = "action.blocked"

	automationScheduleReady          = "ready"
	automationSchedulePending        = "pending"
	automationScheduleAwaitingReview = "awaiting_review"
	automationScheduleExecuting      = "executing"
	automationScheduleBlocked        = "blocked"
	automationScheduleCompleted      = "completed"
)

type automationDependencyProjection struct {
	ActionID           string                  `json:"action_id"`
	ProviderCallID     string                  `json:"provider_call_id,omitempty"`
	Tool               AutomationToolName      `json:"tool,omitempty"`
	Status             AutomationActionStatus  `json:"status,omitempty"`
	Satisfied          bool                    `json:"satisfied"`
	ExpectedEntityType string                  `json:"expected_entity_type,omitempty"`
	ResultEntity       *AutomationEntityResult `json:"result_entity,omitempty"`
	NotReadyCode       string                  `json:"not_ready_code,omitempty"`
	NotReadyReason     string                  `json:"not_ready_reason,omitempty"`
}

type automationActionScheduleProjection struct {
	State              string `json:"state"`
	Ready              bool   `json:"ready"`
	NotReadyCode       string `json:"not_ready_code,omitempty"`
	NotReadyReason     string `json:"not_ready_reason,omitempty"`
	BlockingActionID   string `json:"blocking_action_id,omitempty"`
	ExpectedEntityType string `json:"expected_entity_type,omitempty"`
	ActualEntityType   string `json:"actual_entity_type,omitempty"`
}

type automationActionProjection struct {
	AutomationAction
	Dependencies   []automationDependencyProjection   `json:"dependencies"`
	Scheduler      automationActionScheduleProjection `json:"scheduler"`
	ResultEntity   *AutomationEntityResult            `json:"result_entity,omitempty"`
	NotReadyReason string                             `json:"not_ready_reason,omitempty"`
}

type automationRunActionSummary struct {
	Total          int `json:"total"`
	Ready          int `json:"ready"`
	Pending        int `json:"pending"`
	AwaitingReview int `json:"awaiting_review"`
	Blocked        int `json:"blocked"`
	Executing      int `json:"executing"`
	Succeeded      int `json:"succeeded"`
	Failed         int `json:"failed"`
}

type automationRunProjection struct {
	AutomationRun
	ActionSummary automationRunActionSummary `json:"action_summary"`
}

type automationScheduledBlock struct {
	ActionID           string
	Code               string
	Reason             string
	DependencyActionID string
	ExpectedEntityType string
	ActualEntityType   string
}

type automationRunSchedulePlan struct {
	Ready      []string
	Blocked    []automationScheduledBlock
	Projection map[string]automationActionScheduleProjection
}

func (s *AutomationService) ExecuteReadyActions(ctx context.Context, runID string) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if s == nil || s.store == nil {
		return NewAutomationValidationError("automation_service", "is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return NewAutomationValidationError("run_id", "is required")
	}

	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var plan automationRunSchedulePlan
		err := s.store.Read(func(state *AutomationState) error {
			var innerErr error
			plan, innerErr = automationPlanRunSchedule(*state, runID)
			return innerErr
		})
		if err != nil {
			return err
		}
		if len(plan.Blocked) != 0 {
			changed, err := s.blockScheduledAction(plan.Blocked[0])
			if err != nil {
				return err
			}
			if changed {
				continue
			}
		}
		if len(plan.Ready) == 0 {
			return nil
		}
		if s.executor == nil {
			return NewAutomationValidationError("executor", "is unavailable")
		}
		actionID := plan.Ready[0]
		if err := s.executeSupervisedAction(ctx, actionID); err != nil {
			current, getErr := s.getAction(actionID)
			if getErr == nil && current.Status != AutomationActionApproved {
				continue
			}
			return err
		}
	}
}

func (s *AutomationService) blockScheduledAction(block automationScheduledBlock) (bool, error) {
	var blocked AutomationAction
	var auditData json.RawMessage
	changed := false
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, block.ActionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		action := &state.Actions[index]
		if IsTerminalAutomationActionStatus(action.Status) || action.Status == AutomationActionExecuting {
			return nil
		}
		result, err := json.Marshal(map[string]any{
			"blocked": map[string]any{
				"code":                 block.Code,
				"reason":               block.Reason,
				"dependency_action_id": block.DependencyActionID,
				"expected_entity_type": block.ExpectedEntityType,
				"actual_entity_type":   block.ActualEntityType,
			},
		})
		if err != nil {
			return err
		}
		now := automationMutationTime(action.UpdatedAt)
		action.Status = AutomationActionFailed
		action.Result = result
		action.ErrorCode = block.Code
		action.ErrorMessage = block.Reason
		action.Retryable = false
		action.Version++
		action.UpdatedAt = now
		for i := range state.PermissionRequests {
			request := &state.PermissionRequests[i]
			if request.ActionID != action.ID || request.Status != AutomationPermissionStatusPending {
				continue
			}
			request.Status = AutomationPermissionStatusExpired
			request.Version++
			request.UpdatedAt = now
		}
		auditData, err = json.Marshal(map[string]any{
			"provider_call_id":     action.ProviderCallID,
			"tool":                 action.Tool,
			"risk":                 action.Risk,
			"status":               action.Status,
			"action_version":       action.Version,
			"scheduler_state":      automationScheduleBlocked,
			"not_ready_code":       block.Code,
			"not_ready_reason":     block.Reason,
			"dependency_action_id": block.DependencyActionID,
			"expected_entity_type": block.ExpectedEntityType,
			"actual_entity_type":   block.ActualEntityType,
		})
		if err != nil {
			return err
		}
		if _, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: AutomationAuditEventActionBlocked,
			RunID:     action.RunID,
			ActionID:  action.ID,
			ActorType: "system",
			Data:      sanitizeAutomationJSON(auditData),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		blocked = *action
		changed = true
		return nil
	})
	if err != nil || !changed {
		return changed, err
	}
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type:     AutomationAuditEventActionBlocked,
		RunID:    blocked.RunID,
		ActionID: blocked.ID,
		Data:     auditData,
	})
	s.reconcileChatTurnForRun(blocked.RunID)
	return true, nil
}

func automationPlanRunSchedule(state AutomationState, runID string) (automationRunSchedulePlan, error) {
	if automationRunIndex(&state, runID) < 0 {
		return automationRunSchedulePlan{}, errors.New("automation run not found")
	}
	actions := automationActionsForRun(state, runID)
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].ID < actions[j].ID
	})
	cyclic := automationCyclicActionIDs(actions)
	plan := automationRunSchedulePlan{
		Ready:      []string{},
		Blocked:    []automationScheduledBlock{},
		Projection: make(map[string]automationActionScheduleProjection, len(actions)),
	}
	for _, action := range actions {
		projection, block := automationActionScheduleState(state, action, cyclic)
		plan.Projection[action.ID] = projection
		if projection.Ready {
			plan.Ready = append(plan.Ready, action.ID)
		}
		if block != nil {
			plan.Blocked = append(plan.Blocked, *block)
		}
	}
	sort.Strings(plan.Ready)
	sort.Slice(plan.Blocked, func(i, j int) bool {
		if plan.Blocked[i].ActionID == plan.Blocked[j].ActionID {
			return plan.Blocked[i].Code < plan.Blocked[j].Code
		}
		return plan.Blocked[i].ActionID < plan.Blocked[j].ActionID
	})
	return plan, nil
}

func automationActionScheduleState(
	state AutomationState,
	action AutomationAction,
	cyclic map[string]struct{},
) (automationActionScheduleProjection, *automationScheduledBlock) {
	if IsTerminalAutomationActionStatus(action.Status) {
		if strings.HasPrefix(action.ErrorCode, "dependency_") {
			return automationActionScheduleProjection{
				State:          automationScheduleBlocked,
				NotReadyCode:   action.ErrorCode,
				NotReadyReason: action.ErrorMessage,
			}, nil
		}
		return automationActionScheduleProjection{State: automationScheduleCompleted}, nil
	}
	if action.Status == AutomationActionExecuting {
		return automationActionScheduleProjection{
			State:          automationScheduleExecuting,
			NotReadyCode:   "action_executing",
			NotReadyReason: "action execution is already in progress",
		}, nil
	}
	if _, ok := cyclic[action.ID]; ok {
		return automationBlockedProjection(action.ID, "dependency_cycle", "action dependency graph contains a cycle", "", "", "")
	}

	dependencies := append([]string(nil), action.DependsOnActionIDs...)
	sort.Strings(dependencies)
	for _, dependencyID := range dependencies {
		index := automationActionIndex(&state, dependencyID)
		if index < 0 {
			return automationBlockedProjection(
				action.ID,
				"dependency_missing",
				fmt.Sprintf("dependency action %s was not found", dependencyID),
				dependencyID,
				"",
				"",
			)
		}
		dependency := state.Actions[index]
		if dependency.RunID != action.RunID {
			return automationBlockedProjection(
				action.ID,
				"dependency_cross_run",
				fmt.Sprintf("dependency action %s belongs to another run", dependencyID),
				dependencyID,
				"",
				"",
			)
		}
	}
	expectedTypes, referenceErr := automationExpectedDependencyEntityTypes(state, action)
	if referenceErr != nil {
		return automationBlockedProjection(action.ID, "dependency_reference_invalid", referenceErr.Error(), "", "", "")
	}
	for _, dependencyID := range dependencies {
		index := automationActionIndex(&state, dependencyID)
		dependency := state.Actions[index]
		switch dependency.Status {
		case AutomationActionSucceeded:
			expectedType := expectedTypes[dependencyID]
			if expectedType == "" {
				continue
			}
			entity, code, reason := automationDependencyResultEntity(dependency, expectedType)
			if code != "" {
				actualType := ""
				if entity != nil {
					actualType = entity.EntityType
				}
				return automationBlockedProjection(
					action.ID,
					code,
					reason,
					dependencyID,
					expectedType,
					actualType,
				)
			}
		case AutomationActionAwaitingReview:
			return automationActionScheduleProjection{
				State:            automationSchedulePending,
				NotReadyCode:     "dependency_awaiting_review",
				NotReadyReason:   fmt.Sprintf("dependency action %s is awaiting review", dependencyID),
				BlockingActionID: dependencyID,
			}, nil
		case AutomationActionApproved, AutomationActionProposed:
			return automationActionScheduleProjection{
				State:            automationSchedulePending,
				NotReadyCode:     "dependency_pending",
				NotReadyReason:   fmt.Sprintf("dependency action %s has not executed", dependencyID),
				BlockingActionID: dependencyID,
			}, nil
		case AutomationActionExecuting:
			return automationActionScheduleProjection{
				State:            automationSchedulePending,
				NotReadyCode:     "dependency_executing",
				NotReadyReason:   fmt.Sprintf("dependency action %s is executing", dependencyID),
				BlockingActionID: dependencyID,
			}, nil
		default:
			code := "dependency_" + string(dependency.Status)
			return automationBlockedProjection(
				action.ID,
				code,
				fmt.Sprintf("dependency action %s ended with status %s", dependencyID, dependency.Status),
				dependencyID,
				expectedTypes[dependencyID],
				"",
			)
		}
	}

	switch action.Status {
	case AutomationActionApproved:
		return automationActionScheduleProjection{State: automationScheduleReady, Ready: true}, nil
	case AutomationActionAwaitingReview:
		return automationActionScheduleProjection{
			State:          automationScheduleAwaitingReview,
			NotReadyCode:   "action_awaiting_review",
			NotReadyReason: "action is awaiting operator review",
		}, nil
	default:
		return automationActionScheduleProjection{
			State:          automationSchedulePending,
			NotReadyCode:   "action_not_approved",
			NotReadyReason: fmt.Sprintf("action status %s is not executable", action.Status),
		}, nil
	}
}

func automationBlockedProjection(
	actionID string,
	code string,
	reason string,
	dependencyID string,
	expectedType string,
	actualType string,
) (automationActionScheduleProjection, *automationScheduledBlock) {
	projection := automationActionScheduleProjection{
		State:              automationScheduleBlocked,
		NotReadyCode:       code,
		NotReadyReason:     reason,
		BlockingActionID:   dependencyID,
		ExpectedEntityType: expectedType,
		ActualEntityType:   actualType,
	}
	return projection, &automationScheduledBlock{
		ActionID:           actionID,
		Code:               code,
		Reason:             reason,
		DependencyActionID: dependencyID,
		ExpectedEntityType: expectedType,
		ActualEntityType:   actualType,
	}
}

func automationExpectedDependencyEntityTypes(
	state AutomationState,
	action AutomationAction,
) (map[string]string, error) {
	references, err := AutomationToolCallReferences(AutomationToolCall{
		Tool:      action.Tool,
		Arguments: action.Payload,
	})
	if err != nil {
		return nil, NewAutomationValidationError("action.payload", "does not contain valid dependency references")
	}
	callToAction := make(map[string]AutomationAction)
	for _, candidate := range state.Actions {
		if candidate.RunID == action.RunID {
			callToAction[candidate.ProviderCallID] = candidate
		}
	}
	expected := make(map[string]string)
	for _, reference := range references {
		if reference.Reference.FromCall == "" {
			continue
		}
		dependency, ok := callToAction[reference.Reference.FromCall]
		if !ok {
			return nil, NewAutomationValidationError(
				"action.payload",
				fmt.Sprintf("references missing provider call %s", reference.Reference.FromCall),
			)
		}
		if current := expected[dependency.ID]; current != "" && current != reference.EntityType {
			return nil, NewAutomationValidationError("action.payload", "expects conflicting dependency entity types")
		}
		expected[dependency.ID] = reference.EntityType
	}
	declared := make(map[string]struct{}, len(action.DependsOnActionIDs))
	for _, dependencyID := range action.DependsOnActionIDs {
		declared[dependencyID] = struct{}{}
	}
	if len(expected) != len(declared) {
		return nil, NewAutomationValidationError(
			"action.depends_on_action_ids",
			"does not match payload dependency references",
		)
	}
	for dependencyID := range declared {
		if _, ok := expected[dependencyID]; !ok {
			return nil, NewAutomationValidationError(
				"action.depends_on_action_ids",
				fmt.Sprintf("dependency %s is not referenced by the payload", dependencyID),
			)
		}
	}
	return expected, nil
}

func automationDependencyResultEntity(
	dependency AutomationAction,
	expectedType string,
) (*AutomationEntityResult, string, string) {
	var result AutomationActionResult
	if err := json.Unmarshal(dependency.Result, &result); err != nil || result.Entity == nil {
		return nil,
			"dependency_result_missing",
			fmt.Sprintf("dependency action %s did not produce a result entity", dependency.ID)
	}
	if result.Entity.EntityType != expectedType {
		return result.Entity,
			"dependency_entity_mismatch",
			fmt.Sprintf(
				"dependency action %s produced entity type %s, expected %s",
				dependency.ID,
				result.Entity.EntityType,
				expectedType,
			)
	}
	if strings.TrimSpace(result.Entity.EntityID) == "" {
		return result.Entity,
			"dependency_result_missing",
			fmt.Sprintf("dependency action %s produced an empty entity id", dependency.ID)
	}
	return result.Entity, "", ""
}

func automationCyclicActionIDs(actions []AutomationAction) map[string]struct{} {
	byID := make(map[string]AutomationAction, len(actions))
	for _, action := range actions {
		byID[action.ID] = action
	}
	indegree := make(map[string]int, len(actions))
	dependents := make(map[string][]string, len(actions))
	for _, action := range actions {
		for _, dependencyID := range action.DependsOnActionIDs {
			if _, ok := byID[dependencyID]; !ok {
				continue
			}
			indegree[action.ID]++
			dependents[dependencyID] = append(dependents[dependencyID], action.ID)
		}
		if _, ok := indegree[action.ID]; !ok {
			indegree[action.ID] = 0
		}
	}
	queue := make([]string, 0, len(actions))
	for actionID, count := range indegree {
		if count == 0 {
			queue = append(queue, actionID)
		}
	}
	sort.Strings(queue)
	for len(queue) != 0 {
		actionID := queue[0]
		queue = queue[1:]
		sort.Strings(dependents[actionID])
		for _, dependentID := range dependents[actionID] {
			indegree[dependentID]--
			if indegree[dependentID] == 0 {
				queue = append(queue, dependentID)
				sort.Strings(queue)
			}
		}
		delete(indegree, actionID)
	}
	cyclic := make(map[string]struct{}, len(indegree))
	for actionID, count := range indegree {
		if count > 0 {
			cyclic[actionID] = struct{}{}
		}
	}
	return cyclic
}

func automationProjectAction(state AutomationState, action AutomationAction) automationActionProjection {
	cyclic := automationCyclicActionIDs(automationActionsForRun(state, action.RunID))
	schedule, _ := automationActionScheduleState(state, action, cyclic)
	dependencies := automationProjectDependencies(state, action)
	var result AutomationActionResult
	var entity *AutomationEntityResult
	if len(action.Result) != 0 && json.Unmarshal(action.Result, &result) == nil {
		entity = result.Entity
	}
	return automationActionProjection{
		AutomationAction: action,
		Dependencies:     dependencies,
		Scheduler:        schedule,
		ResultEntity:     entity,
		NotReadyReason:   schedule.NotReadyReason,
	}
}

func automationProjectDependencies(
	state AutomationState,
	action AutomationAction,
) []automationDependencyProjection {
	expectedTypes, _ := automationExpectedDependencyEntityTypes(state, action)
	dependencyIDs := append([]string(nil), action.DependsOnActionIDs...)
	sort.Strings(dependencyIDs)
	projections := make([]automationDependencyProjection, 0, len(dependencyIDs))
	for _, dependencyID := range dependencyIDs {
		projection := automationDependencyProjection{ActionID: dependencyID}
		index := automationActionIndex(&state, dependencyID)
		if index < 0 {
			projection.NotReadyCode = "dependency_missing"
			projection.NotReadyReason = fmt.Sprintf("dependency action %s was not found", dependencyID)
			projections = append(projections, projection)
			continue
		}
		dependency := state.Actions[index]
		projection.ProviderCallID = dependency.ProviderCallID
		projection.Tool = dependency.Tool
		projection.Status = dependency.Status
		projection.ExpectedEntityType = expectedTypes[dependency.ID]
		if dependency.RunID != action.RunID {
			projection.NotReadyCode = "dependency_cross_run"
			projection.NotReadyReason = fmt.Sprintf("dependency action %s belongs to another run", dependencyID)
			projections = append(projections, projection)
			continue
		}
		if dependency.Status == AutomationActionSucceeded {
			entity, code, reason := automationDependencyResultEntity(dependency, projection.ExpectedEntityType)
			projection.ResultEntity = entity
			if projection.ExpectedEntityType == "" {
				projection.Satisfied = true
			} else if code == "" {
				projection.Satisfied = true
			} else {
				projection.NotReadyCode = code
				projection.NotReadyReason = reason
			}
		} else {
			projection.NotReadyCode = "dependency_" + string(dependency.Status)
			projection.NotReadyReason = fmt.Sprintf(
				"dependency action %s has status %s",
				dependencyID,
				dependency.Status,
			)
		}
		projections = append(projections, projection)
	}
	return projections
}

func automationActionsForRun(state AutomationState, runID string) []AutomationAction {
	actions := make([]AutomationAction, 0)
	for _, action := range state.Actions {
		if action.RunID == runID {
			actions = append(actions, action)
		}
	}
	return actions
}

func automationRunSummary(state AutomationState, runID string) automationRunActionSummary {
	actions := automationActionsForRun(state, runID)
	cyclic := automationCyclicActionIDs(actions)
	summary := automationRunActionSummary{Total: len(actions)}
	for _, action := range actions {
		schedule, _ := automationActionScheduleState(state, action, cyclic)
		switch {
		case action.Status == AutomationActionSucceeded:
			summary.Succeeded++
		case schedule.State == automationScheduleBlocked || strings.HasPrefix(action.ErrorCode, "dependency_"):
			summary.Blocked++
		case schedule.State == automationScheduleReady:
			summary.Ready++
		case schedule.State == automationScheduleAwaitingReview:
			summary.AwaitingReview++
		case schedule.State == automationScheduleExecuting:
			summary.Executing++
		case schedule.State == automationSchedulePending:
			summary.Pending++
		case IsTerminalAutomationActionStatus(action.Status):
			summary.Failed++
		default:
			summary.Pending++
		}
	}
	return summary
}
