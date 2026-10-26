package main

import (
	"bytes"
	"context"
	"sort"
)

func init() {
	bindWorkbenchOperationHandlers("m4-sanitized-introspection",
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationSessionsList,
			Handler:   workbenchIntrospectionSessionsList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationSessionsGet,
			Handler:   workbenchIntrospectionSessionsGet,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationRunsList,
			Handler:   workbenchIntrospectionRunsList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationRunsGet,
			Handler:   workbenchIntrospectionRunsGet,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationActionsList,
			Handler:   workbenchIntrospectionActionsList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationActionsGet,
			Handler:   workbenchIntrospectionActionsGet,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationAutomationAuditList,
			Handler:   workbenchIntrospectionAuditList,
		},
	)
}

func workbenchIntrospectionSessionsList(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationSessionsList
	arguments, ok := raw.(*WorkbenchAutomationListArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation session arguments are invalid")
	}
	if arguments.Status != "" &&
		!IsValidAutomationSessionStatus(AutomationSessionStatus(arguments.Status)) {
		return workbenchIntrospectionInvalidArguments(operation, "automation session status is invalid")
	}
	state, failure := workbenchIntrospectionSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}

	sessionTools := workbenchIntrospectionSessionTools(state)
	sessions := make([]AutomationSession, 0, len(state.Sessions))
	for _, session := range state.Sessions {
		if arguments.Status != "" && string(session.Status) != arguments.Status {
			continue
		}
		if arguments.Tool != "" {
			tools := sessionTools[session.ID]
			if _, matches := tools[arguments.Tool]; !matches {
				continue
			}
		}
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	total := len(sessions)
	pageSessions := workbenchIntrospectionPage(sessions, arguments.Limit, arguments.Offset)
	counts := workbenchIntrospectionRelationshipCounts(state)
	projected := make([]workbenchAutomationSessionProjection, len(pageSessions))
	for i := range pageSessions {
		projected[i] = workbenchProjectAutomationSessionWithCounts(pageSessions[i], counts)
	}
	return workbenchIntrospectionBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(values []workbenchAutomationSessionProjection) any {
			return workbenchAutomationSessionListProjection{Sessions: values}
		},
	)
}

func workbenchIntrospectionSessionsGet(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationSessionsGet
	arguments, ok := raw.(*WorkbenchIDArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation session arguments are invalid")
	}
	state, committedAudit, failure := workbenchIntrospectionAuditSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}
	var session *AutomationSession
	for i := range state.Sessions {
		if state.Sessions[i].ID == arguments.ID {
			selected := state.Sessions[i]
			session = &selected
			break
		}
	}
	if session == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorNotFound,
			"automation session not found",
			false,
		)
	}

	auditRecords, failure := workbenchIntrospectionAuditRecords(ctx, state, committedAudit, operation)
	if failure != nil {
		return *failure
	}
	turns := make([]AutomationTurn, 0)
	turnEvents := make([]AutomationTurnEvent, 0)
	runs := make([]AutomationRun, 0)
	actions := make([]AutomationAction, 0)
	runIDs := make(map[string]struct{})
	actionIDs := make(map[string]struct{})
	for _, turn := range state.Turns {
		if turn.SessionID == session.ID {
			turns = append(turns, turn)
		}
	}
	for _, event := range state.TurnEvents {
		if event.SessionID == session.ID {
			turnEvents = append(turnEvents, event)
		}
	}
	for _, run := range state.Runs {
		if run.SessionID == session.ID {
			runs = append(runs, run)
			runIDs[run.ID] = struct{}{}
		}
	}
	for _, action := range state.Actions {
		if _, matches := runIDs[action.RunID]; matches {
			actions = append(actions, action)
			actionIDs[action.ID] = struct{}{}
		}
	}
	filteredAudit := make([]AutomationAuditRecord, 0)
	for _, record := range auditRecords {
		_, runMatches := runIDs[record.RunID]
		_, actionMatches := actionIDs[record.ActionID]
		if runMatches || actionMatches {
			filteredAudit = append(filteredAudit, record)
		}
	}

	workbenchIntrospectionSortTurnsAscending(turns)
	workbenchIntrospectionSortTurnEventsAscending(turnEvents)
	workbenchIntrospectionSortRunsAscending(runs)
	workbenchIntrospectionSortActionsAscending(actions)
	workbenchIntrospectionSortAuditAscending(filteredAudit)

	selectedTurns := workbenchIntrospectionNewest(
		turns,
		workbenchIntrospectionCap(len(turns), workbenchIntrospectionSessionTurnsCap),
	)
	projectedTurns := make([]workbenchAutomationTurnProjection, len(selectedTurns))
	for i := range selectedTurns {
		projectedTurns[i] = workbenchProjectAutomationTurn(selectedTurns[i])
	}
	selectedTurnEvents := workbenchIntrospectionNewest(
		turnEvents,
		workbenchIntrospectionCap(len(turnEvents), workbenchIntrospectionSessionEventsCap),
	)
	projectedTurnEvents := make([]workbenchAutomationTurnEventProjection, len(selectedTurnEvents))
	for i := range selectedTurnEvents {
		projectedTurnEvents[i] = workbenchProjectAutomationTurnEvent(selectedTurnEvents[i])
	}
	selectedRuns := workbenchIntrospectionNewest(
		runs,
		workbenchIntrospectionCap(len(runs), workbenchIntrospectionSessionRunsCap),
	)
	counts := workbenchIntrospectionRelationshipCounts(state)
	projectedRuns := make([]workbenchAutomationRunProjection, len(selectedRuns))
	for i := range selectedRuns {
		projectedRuns[i] = workbenchProjectAutomationRunWithCounts(selectedRuns[i], counts)
	}
	selectedActions := workbenchIntrospectionNewest(
		actions,
		workbenchIntrospectionCap(len(actions), workbenchIntrospectionSessionActionsCap),
	)
	projectedActions := make([]workbenchAutomationActionProjection, len(selectedActions))
	for i := range selectedActions {
		projectedActions[i] = workbenchProjectAutomationAction(selectedActions[i])
	}
	selectedAudit := workbenchIntrospectionNewest(
		filteredAudit,
		workbenchIntrospectionCap(len(filteredAudit), workbenchIntrospectionAuditEventsCap),
	)
	projectedAudit := make([]workbenchAutomationAuditEventProjection, len(selectedAudit))
	for i := range selectedAudit {
		projectedAudit[i] = workbenchProjectAutomationAudit(selectedAudit[i])
	}

	root := workbenchProjectAutomationSessionWithCounts(*session, counts)
	budget := workbenchIntrospectionDetailBudget{
		Turns:       len(projectedTurns),
		TurnEvents:  len(projectedTurnEvents),
		Runs:        len(projectedRuns),
		Actions:     len(projectedActions),
		AuditEvents: len(projectedAudit),
	}
	return workbenchIntrospectionBoundedDetailSuccess(
		operation,
		budget,
		func(current workbenchIntrospectionDetailBudget) any {
			selectedTurns := workbenchIntrospectionNewest(projectedTurns, current.Turns)
			selectedEvents := workbenchIntrospectionNewest(projectedTurnEvents, current.TurnEvents)
			selectedRuns := workbenchIntrospectionNewest(projectedRuns, current.Runs)
			selectedActions := workbenchIntrospectionNewest(projectedActions, current.Actions)
			selectedAudit := workbenchIntrospectionNewest(projectedAudit, current.AuditEvents)
			return workbenchAutomationSessionDetailProjection{
				Session:          root,
				TurnsTotal:       len(turns),
				Turns:            selectedTurns,
				TurnEventsTotal:  len(turnEvents),
				TurnEvents:       selectedEvents,
				RunsTotal:        len(runs),
				Runs:             selectedRuns,
				ActionsTotal:     len(actions),
				Actions:          selectedActions,
				AuditEventsTotal: len(filteredAudit),
				AuditEvents:      selectedAudit,
				Truncated: len(selectedTurns) < len(turns) ||
					len(selectedEvents) < len(turnEvents) ||
					len(selectedRuns) < len(runs) ||
					len(selectedActions) < len(actions) ||
					len(selectedAudit) < len(filteredAudit),
			}
		},
	)
}

func workbenchIntrospectionRunsList(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationRunsList
	arguments, ok := raw.(*WorkbenchAutomationListArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation run arguments are invalid")
	}
	if arguments.Status != "" &&
		!IsValidAutomationRunStatus(AutomationRunStatus(arguments.Status)) {
		return workbenchIntrospectionInvalidArguments(operation, "automation run status is invalid")
	}
	state, failure := workbenchIntrospectionSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}

	runTools := workbenchIntrospectionRunTools(state)
	runs := make([]AutomationRun, 0, len(state.Runs))
	for _, run := range state.Runs {
		if arguments.Status != "" && string(run.Status) != arguments.Status {
			continue
		}
		if arguments.Tool != "" {
			tools := runTools[run.ID]
			if _, matches := tools[arguments.Tool]; !matches {
				continue
			}
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	total := len(runs)
	pageRuns := workbenchIntrospectionPage(runs, arguments.Limit, arguments.Offset)
	counts := workbenchIntrospectionRelationshipCounts(state)
	projected := make([]workbenchAutomationRunProjection, len(pageRuns))
	for i := range pageRuns {
		projected[i] = workbenchProjectAutomationRunWithCounts(pageRuns[i], counts)
	}
	return workbenchIntrospectionBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(values []workbenchAutomationRunProjection) any {
			return workbenchAutomationRunListProjection{Runs: values}
		},
	)
}

func workbenchIntrospectionRunsGet(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationRunsGet
	arguments, ok := raw.(*WorkbenchIDArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation run arguments are invalid")
	}
	state, committedAudit, failure := workbenchIntrospectionAuditSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}
	var run *AutomationRun
	for i := range state.Runs {
		if state.Runs[i].ID == arguments.ID {
			selected := state.Runs[i]
			run = &selected
			break
		}
	}
	if run == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorNotFound,
			"automation run not found",
			false,
		)
	}
	auditRecords, failure := workbenchIntrospectionAuditRecords(ctx, state, committedAudit, operation)
	if failure != nil {
		return *failure
	}
	actions := make([]AutomationAction, 0)
	actionIDs := make(map[string]struct{})
	for _, action := range state.Actions {
		if action.RunID == run.ID {
			actions = append(actions, action)
			actionIDs[action.ID] = struct{}{}
		}
	}
	filteredAudit := make([]AutomationAuditRecord, 0)
	for _, record := range auditRecords {
		_, actionMatches := actionIDs[record.ActionID]
		if record.RunID == run.ID || actionMatches {
			filteredAudit = append(filteredAudit, record)
		}
	}
	workbenchIntrospectionSortActionsAscending(actions)
	workbenchIntrospectionSortAuditAscending(filteredAudit)
	selectedActions := workbenchIntrospectionNewest(
		actions,
		workbenchIntrospectionCap(len(actions), workbenchIntrospectionRunActionsCap),
	)
	projectedActions := make([]workbenchAutomationActionProjection, len(selectedActions))
	for i := range selectedActions {
		projectedActions[i] = workbenchProjectAutomationAction(selectedActions[i])
	}
	selectedAudit := workbenchIntrospectionNewest(
		filteredAudit,
		workbenchIntrospectionCap(len(filteredAudit), workbenchIntrospectionAuditEventsCap),
	)
	projectedAudit := make([]workbenchAutomationAuditEventProjection, len(selectedAudit))
	for i := range selectedAudit {
		projectedAudit[i] = workbenchProjectAutomationAudit(selectedAudit[i])
	}

	counts := workbenchIntrospectionRelationshipCounts(state)
	root := workbenchProjectAutomationRunWithCounts(*run, counts)
	budget := workbenchIntrospectionDetailBudget{
		Actions:     len(projectedActions),
		AuditEvents: len(projectedAudit),
	}
	return workbenchIntrospectionBoundedDetailSuccess(
		operation,
		budget,
		func(current workbenchIntrospectionDetailBudget) any {
			selectedActions := workbenchIntrospectionNewest(projectedActions, current.Actions)
			selectedAudit := workbenchIntrospectionNewest(projectedAudit, current.AuditEvents)
			return workbenchAutomationRunDetailProjection{
				Run:              root,
				ActionsTotal:     len(actions),
				Actions:          selectedActions,
				AuditEventsTotal: len(filteredAudit),
				AuditEvents:      selectedAudit,
				Truncated: len(selectedActions) < len(actions) ||
					len(selectedAudit) < len(filteredAudit),
			}
		},
	)
}

func workbenchIntrospectionActionsList(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationActionsList
	arguments, ok := raw.(*WorkbenchAutomationListArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation action arguments are invalid")
	}
	if arguments.Status != "" &&
		!IsValidAutomationActionStatus(AutomationActionStatus(arguments.Status)) {
		return workbenchIntrospectionInvalidArguments(operation, "automation action status is invalid")
	}
	state, failure := workbenchIntrospectionSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}
	actions := make([]AutomationAction, 0, len(state.Actions))
	for _, action := range state.Actions {
		if arguments.Status != "" && string(action.Status) != arguments.Status {
			continue
		}
		if arguments.Tool != "" && action.Tool != arguments.Tool {
			continue
		}
		actions = append(actions, action)
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].UpdatedAt.Equal(actions[j].UpdatedAt) {
			return actions[i].ID > actions[j].ID
		}
		return actions[i].UpdatedAt.After(actions[j].UpdatedAt)
	})
	total := len(actions)
	pageActions := workbenchIntrospectionPage(actions, arguments.Limit, arguments.Offset)
	projected := make([]workbenchAutomationActionProjection, len(pageActions))
	for i := range pageActions {
		projected[i] = workbenchProjectAutomationAction(pageActions[i])
	}
	return workbenchIntrospectionBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(values []workbenchAutomationActionProjection) any {
			return workbenchAutomationActionListProjection{Actions: values}
		},
	)
}

func workbenchIntrospectionActionsGet(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationActionsGet
	arguments, ok := raw.(*WorkbenchIDArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation action arguments are invalid")
	}
	state, committedAudit, failure := workbenchIntrospectionAuditSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}
	var action *AutomationAction
	for i := range state.Actions {
		if state.Actions[i].ID == arguments.ID {
			selected := state.Actions[i]
			action = &selected
			break
		}
	}
	if action == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorNotFound,
			"automation action not found",
			false,
		)
	}
	auditRecords, failure := workbenchIntrospectionAuditRecords(ctx, state, committedAudit, operation)
	if failure != nil {
		return *failure
	}

	var result *workbenchAutomationResultProjection
	for _, item := range state.Results {
		if item.ActionID == action.ID {
			projected := workbenchProjectAutomationResult(item)
			result = &projected
			break
		}
	}
	approvals := make([]AutomationApprovalRecord, 0)
	for _, approval := range state.Approvals {
		if approval.ActionID == action.ID {
			approvals = append(approvals, approval)
		}
	}
	requests := make([]AutomationPermissionRequest, 0)
	for _, request := range state.PermissionRequests {
		if request.ActionID == action.ID {
			requests = append(requests, request)
		}
	}
	var outbox *workbenchAutomationOutboxProjection
	for _, entry := range state.Outbox {
		if entry.ActionID == action.ID {
			projected := workbenchProjectAutomationOutbox(entry)
			outbox = &projected
			break
		}
	}
	filteredAudit := make([]AutomationAuditRecord, 0)
	for _, record := range auditRecords {
		if record.ActionID == action.ID {
			filteredAudit = append(filteredAudit, record)
		}
	}
	sort.SliceStable(approvals, func(i, j int) bool {
		if approvals[i].CreatedAt.Equal(approvals[j].CreatedAt) {
			return approvals[i].ID < approvals[j].ID
		}
		return approvals[i].CreatedAt.Before(approvals[j].CreatedAt)
	})
	sort.SliceStable(requests, func(i, j int) bool {
		if requests[i].CreatedAt.Equal(requests[j].CreatedAt) {
			return requests[i].ID < requests[j].ID
		}
		return requests[i].CreatedAt.Before(requests[j].CreatedAt)
	})
	workbenchIntrospectionSortAuditAscending(filteredAudit)

	selectedApprovals := workbenchIntrospectionNewest(
		approvals,
		workbenchIntrospectionCap(len(approvals), workbenchIntrospectionActionApprovalsCap),
	)
	projectedApprovals := make([]workbenchAutomationApprovalProjection, len(selectedApprovals))
	for i := range selectedApprovals {
		projectedApprovals[i] = workbenchProjectAutomationApproval(selectedApprovals[i])
	}
	selectedRequests := workbenchIntrospectionNewest(
		requests,
		workbenchIntrospectionCap(len(requests), workbenchIntrospectionPermissionReqsCap),
	)
	projectedRequests := make([]workbenchAutomationPermissionRequestProjection, len(selectedRequests))
	for i := range selectedRequests {
		projectedRequests[i] = workbenchProjectAutomationPermissionRequest(selectedRequests[i])
	}
	selectedAudit := workbenchIntrospectionNewest(
		filteredAudit,
		workbenchIntrospectionCap(len(filteredAudit), workbenchIntrospectionAuditEventsCap),
	)
	projectedAudit := make([]workbenchAutomationAuditEventProjection, len(selectedAudit))
	for i := range selectedAudit {
		projectedAudit[i] = workbenchProjectAutomationAudit(selectedAudit[i])
	}

	root := workbenchProjectAutomationAction(*action)
	budget := workbenchIntrospectionDetailBudget{
		Approvals:          len(projectedApprovals),
		PermissionRequests: len(projectedRequests),
		AuditEvents:        len(projectedAudit),
	}
	return workbenchIntrospectionBoundedDetailSuccess(
		operation,
		budget,
		func(current workbenchIntrospectionDetailBudget) any {
			selectedApprovals := workbenchIntrospectionNewest(projectedApprovals, current.Approvals)
			selectedRequests := workbenchIntrospectionNewest(projectedRequests, current.PermissionRequests)
			selectedAudit := workbenchIntrospectionNewest(projectedAudit, current.AuditEvents)
			return workbenchAutomationActionDetailProjection{
				Action:                  root,
				Result:                  result,
				ApprovalsTotal:          len(approvals),
				Approvals:               selectedApprovals,
				PermissionRequestsTotal: len(requests),
				PermissionRequests:      selectedRequests,
				Outbox:                  outbox,
				AuditEventsTotal:        len(filteredAudit),
				AuditEvents:             selectedAudit,
				Truncated: len(selectedApprovals) < len(approvals) ||
					len(selectedRequests) < len(requests) ||
					len(selectedAudit) < len(filteredAudit),
			}
		},
	)
}

func workbenchIntrospectionAuditList(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationAutomationAuditList
	arguments, ok := raw.(*WorkbenchAuditListArguments)
	if !ok || arguments == nil {
		return workbenchIntrospectionInvalidArguments(operation, "automation audit arguments are invalid")
	}
	switch {
	case arguments.Operation != "":
		return workbenchIntrospectionInvalidArguments(operation, "automation audit operation filter is unsupported")
	case arguments.EntityType != "":
		return workbenchIntrospectionInvalidArguments(operation, "automation audit entity_type filter is unsupported")
	case arguments.EntityID != "":
		return workbenchIntrospectionInvalidArguments(operation, "automation audit entity_id filter is unsupported")
	case arguments.SourceEventID != "":
		return workbenchIntrospectionInvalidArguments(operation, "automation audit source_event_id filter is unsupported")
	case arguments.RunID != "" && !automationForwardResourceID(arguments.RunID, "airun"):
		return workbenchIntrospectionInvalidArguments(operation, "automation audit run_id filter is invalid")
	case arguments.ActionID != "" && !automationForwardResourceID(arguments.ActionID, "aiact"):
		return workbenchIntrospectionInvalidArguments(operation, "automation audit action_id filter is invalid")
	}
	state, committedAudit, failure := workbenchIntrospectionAuditSnapshot(ctx, dispatcher, operation)
	if failure != nil {
		return *failure
	}
	records, failure := workbenchIntrospectionAuditRecords(ctx, state, committedAudit, operation)
	if failure != nil {
		return *failure
	}
	filtered := make([]AutomationAuditRecord, 0, len(records))
	for _, record := range records {
		if arguments.RunID != "" && record.RunID != arguments.RunID {
			continue
		}
		if arguments.ActionID != "" && record.ActionID != arguments.ActionID {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	total := len(filtered)
	pageRecords := workbenchIntrospectionPage(filtered, arguments.Limit, arguments.Offset)
	projected := make([]workbenchAutomationAuditEventProjection, len(pageRecords))
	for i := range pageRecords {
		projected[i] = workbenchProjectAutomationAudit(pageRecords[i])
	}
	return workbenchIntrospectionBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(values []workbenchAutomationAuditEventProjection) any {
			return workbenchAutomationAuditListProjection{AuditEvents: values}
		},
	)
}

func workbenchIntrospectionSnapshot(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) (AutomationState, *WorkbenchToolResult) {
	if err := ctx.Err(); err != nil {
		result := workbenchContextFailure(operation, err)
		return AutomationState{}, &result
	}
	if dispatcher == nil || dispatcher.automation == nil {
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"automation store is unavailable",
			true,
		)
		return AutomationState{}, &result
	}
	return dispatcher.automation.Snapshot(), nil
}

func workbenchIntrospectionAuditSnapshot(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) (AutomationState, []AutomationAuditRecord, *WorkbenchToolResult) {
	if err := ctx.Err(); err != nil {
		result := workbenchContextFailure(operation, err)
		return AutomationState{}, nil, &result
	}
	if dispatcher == nil || dispatcher.automation == nil {
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"automation store is unavailable",
			true,
		)
		return AutomationState{}, nil, &result
	}
	if dispatcher.audit == nil {
		return dispatcher.automation.Snapshot(), nil, nil
	}

	dispatcher.audit.mu.Lock()
	defer dispatcher.audit.mu.Unlock()
	if dispatcher.audit.committed == nil {
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"automation audit is unavailable",
			true,
		)
		return AutomationState{}, nil, &result
	}
	state := dispatcher.automation.Snapshot()
	committed := make([]AutomationAuditRecord, 0, len(dispatcher.audit.committed))
	for _, item := range dispatcher.audit.committed {
		committed = append(committed, cloneAutomationAuditRecord(item.record))
	}
	if err := ctx.Err(); err != nil {
		result := workbenchContextFailure(operation, err)
		return AutomationState{}, nil, &result
	}
	return state, committed, nil
}

func workbenchIntrospectionAuditRecords(
	ctx context.Context,
	state AutomationState,
	committed []AutomationAuditRecord,
	operation WorkbenchOperation,
) ([]AutomationAuditRecord, *WorkbenchToolResult) {
	records := make([]AutomationAuditRecord, 0, len(state.PendingAudit)+len(committed))
	for _, record := range state.PendingAudit {
		records = append(records, cloneAutomationAuditRecord(record))
	}
	for _, record := range committed {
		records = append(records, cloneAutomationAuditRecord(record))
	}
	if err := ctx.Err(); err != nil {
		result := workbenchContextFailure(operation, err)
		return nil, &result
	}

	canonicalByID := make(map[string][]byte, len(records))
	merged := make([]AutomationAuditRecord, 0, len(records))
	for _, record := range records {
		canonical, err := canonicalAutomationAudit(record)
		if err != nil {
			result := NewWorkbenchToolFailure(
				operation,
				WorkbenchErrorUnavailable,
				"automation audit is unavailable",
				true,
			)
			return nil, &result
		}
		if existing, duplicate := canonicalByID[record.ID]; duplicate {
			if !bytes.Equal(existing, canonical) {
				result := NewWorkbenchToolFailure(
					operation,
					WorkbenchErrorUnavailable,
					"automation audit is unavailable",
					true,
				)
				return nil, &result
			}
			continue
		}
		canonicalByID[record.ID] = append([]byte(nil), canonical...)
		merged = append(merged, cloneAutomationAuditRecord(record))
	}
	return merged, nil
}

func workbenchIntrospectionSessionTools(
	state AutomationState,
) map[string]map[AutomationToolName]struct{} {
	runSessions := make(map[string]string, len(state.Runs))
	for _, run := range state.Runs {
		runSessions[run.ID] = run.SessionID
	}
	tools := make(map[string]map[AutomationToolName]struct{})
	for _, action := range state.Actions {
		sessionID := runSessions[action.RunID]
		if sessionID == "" {
			continue
		}
		if tools[sessionID] == nil {
			tools[sessionID] = make(map[AutomationToolName]struct{})
		}
		tools[sessionID][action.Tool] = struct{}{}
	}
	return tools
}

func workbenchIntrospectionRunTools(
	state AutomationState,
) map[string]map[AutomationToolName]struct{} {
	tools := make(map[string]map[AutomationToolName]struct{})
	for _, action := range state.Actions {
		if tools[action.RunID] == nil {
			tools[action.RunID] = make(map[AutomationToolName]struct{})
		}
		tools[action.RunID][action.Tool] = struct{}{}
	}
	return tools
}

func workbenchIntrospectionSortTurnsAscending(turns []AutomationTurn) {
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].CreatedAt.Equal(turns[j].CreatedAt) {
			return turns[i].ID < turns[j].ID
		}
		return turns[i].CreatedAt.Before(turns[j].CreatedAt)
	})
}

func workbenchIntrospectionSortTurnEventsAscending(events []AutomationTurnEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Sequence != events[j].Sequence {
			return events[i].Sequence < events[j].Sequence
		}
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		}
		if events[i].TurnID != events[j].TurnID {
			return events[i].TurnID < events[j].TurnID
		}
		return events[i].SessionID < events[j].SessionID
	})
}

func workbenchIntrospectionSortRunsAscending(runs []AutomationRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func workbenchIntrospectionSortActionsAscending(actions []AutomationAction) {
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].CreatedAt.Equal(actions[j].CreatedAt) {
			return actions[i].ID < actions[j].ID
		}
		return actions[i].CreatedAt.Before(actions[j].CreatedAt)
	})
}

func workbenchIntrospectionSortAuditAscending(records []AutomationAuditRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
}

func workbenchIntrospectionInvalidArguments(
	operation WorkbenchOperation,
	message string,
) WorkbenchToolResult {
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorInvalidArguments,
		message,
		false,
	)
}

func workbenchIntrospectionCap(total, maximum int) int {
	if total < maximum {
		return total
	}
	return maximum
}
