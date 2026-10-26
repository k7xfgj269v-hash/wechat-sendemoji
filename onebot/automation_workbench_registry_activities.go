package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

var errWorkbenchUnsafeCRMAuditProjection = errors.New("CRM audit event cannot be projected")

type workbenchNoteListProjection struct {
	Notes []Note `json:"notes"`
}

type workbenchTaskListProjection struct {
	Tasks []TaskListItem `json:"tasks"`
}

type workbenchCRMAuditListProjection struct {
	AuditEvents []workbenchCRMAuditEventProjection `json:"audit_events"`
}

type workbenchCRMAuditEventProjection struct {
	ID            string    `json:"id"`
	Actor         string    `json:"actor"`
	Operation     string    `json:"operation"`
	EntityType    string    `json:"entity_type,omitempty"`
	EntityID      string    `json:"entity_id,omitempty"`
	SourceEventID string    `json:"source_event_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func init() {
	bindWorkbenchOperationHandlers("m2-activities-audit",
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationNotesList,
			Handler:   workbenchActivitiesNotesList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationTasksList,
			Handler:   workbenchActivitiesTasksList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationTasksGet,
			Handler:   workbenchActivitiesTasksGet,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationTasksUpdate,
			Handler:   workbenchActivitiesTasksUpdate,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationMessagesReview,
			Handler:   workbenchActivitiesMessagesReview,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationCRMAuditList,
			Handler:   workbenchActivitiesCRMAuditList,
		},
	)
}

func workbenchActivitiesNotesList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationNotesList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchNoteListArguments)
	response, err := crmActivityNewService(dispatcher.crm).ListNotes(crmActivityNoteFilter{
		TargetType: arguments.TargetType,
		TargetID:   arguments.TargetID,
		Limit:      arguments.Limit,
		Offset:     arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return workbenchBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		response.Total,
		response.Notes,
		func(notes []Note) any {
			return workbenchNoteListProjection{Notes: notes}
		},
	)
}

func workbenchActivitiesTasksList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationTasksList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchTaskListArguments)
	response, err := crmActivityNewService(dispatcher.crm).ListTasks(crmActivityTaskFilter{
		Owner:       arguments.Owner,
		Status:      arguments.Status,
		Overdue:     arguments.Overdue,
		DueFrom:     arguments.DueFrom,
		DueTo:       arguments.DueTo,
		CandidateID: arguments.CandidateID,
		LeadID:      arguments.LeadID,
		Limit:       arguments.Limit,
		Offset:      arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return workbenchBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		response.Total,
		response.Tasks,
		func(tasks []TaskListItem) any {
			return workbenchTaskListProjection{Tasks: tasks}
		},
	)
}

func workbenchActivitiesTasksGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationTasksGet, func() (any, error) {
		return crmActivityNewService(dispatcher.crm).GetTask(raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchActivitiesTasksUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationTasksUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchTaskUpdateArguments)
		return crmActivityNewService(dispatcher.crm).UpdateTask(arguments.TaskID, arguments.Update)
	})
}

func workbenchActivitiesMessagesReview(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationMessagesReview
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchMessageReviewArguments)
	now := time.Now().UTC()
	var updated MessageEvent
	err := dispatcher.crm.Update(func(state *CRMState, _ *IDAllocator) error {
		for index := range state.MessageEvents {
			event := &state.MessageEvents[index]
			if event.ID != arguments.MessageID {
				continue
			}
			if event.Version != arguments.ExpectedVersion {
				return NewCRMConflictError("message event version conflict")
			}
			if event.CandidateID != "" && arguments.ReviewStatus != ReviewStatusReviewed {
				return NewCRMValidationError(
					"review_status",
					"linked messages must remain reviewed",
				)
			}
			event.ReviewStatus = arguments.ReviewStatus
			event.Version++
			event.UpdatedAt = now
			updated = *event
			updated.Kinds = append([]string{}, event.Kinds...)
			return nil
		}
		return crmMessageErrNotFound
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		crmMessageProjectEvent(dispatcher.crm.Snapshot(), updated),
		nil,
	)
}

func workbenchActivitiesCRMAuditList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationCRMAuditList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchAuditListArguments)
	if strings.TrimSpace(arguments.ActionID) != "" || strings.TrimSpace(arguments.RunID) != "" {
		return workbenchDomainFailure(
			operation,
			NewCRMValidationError("audit_filter", "action_id and run_id are not CRM audit fields"),
		)
	}
	total, events, err := crmAuditList(dispatcher.crm.Snapshot(), crmAuditFilter{
		Operation:     string(arguments.Operation),
		EntityType:    arguments.EntityType,
		EntityID:      arguments.EntityID,
		SourceEventID: arguments.SourceEventID,
		Limit:         arguments.Limit,
		Offset:        arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	projected, err := workbenchProjectCRMAuditEvents(events)
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return workbenchBoundedListSuccess(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(events []workbenchCRMAuditEventProjection) any {
			return workbenchCRMAuditListProjection{AuditEvents: events}
		},
	)
}

func workbenchBoundedListSuccess[T any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	total int,
	items []T,
	project func([]T) any,
) WorkbenchToolResult {
	for returned := len(items); returned >= 0; returned-- {
		result := NewWorkbenchToolSuccess(
			operation,
			project(items[:returned]),
			newWorkbenchPage(limit, offset, total, returned),
		)
		if result.OK {
			return result
		}
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorInternal,
		"result could not be bounded",
		false,
	)
}

func workbenchProjectCRMAuditEvents(
	events []CRMAuditEvent,
) ([]workbenchCRMAuditEventProjection, error) {
	projected := make([]workbenchCRMAuditEventProjection, len(events))
	for index, event := range events {
		if !workbenchValidCRMAuditID(event.ID, true) ||
			!workbenchValidCRMAuditActor(event.Actor) ||
			!workbenchValidCRMAuditOperation(event.Operation) ||
			!workbenchValidCRMAuditEntityType(event.EntityType) ||
			!workbenchValidCRMAuditID(event.EntityID, false) ||
			!workbenchValidCRMAuditID(event.SourceEventID, false) {
			return nil, errWorkbenchUnsafeCRMAuditProjection
		}
		projected[index] = workbenchCRMAuditEventProjection{
			ID:            event.ID,
			Actor:         event.Actor,
			Operation:     event.Operation,
			EntityType:    event.EntityType,
			EntityID:      event.EntityID,
			SourceEventID: event.SourceEventID,
			CreatedAt:     event.CreatedAt,
		}
	}
	return projected, nil
}

func workbenchValidCRMAuditID(value string, required bool) bool {
	return workbenchValidCRMAuditText(value, 80, required, "_-.")
}

func workbenchValidCRMAuditActor(value string) bool {
	return workbenchValidCRMAuditText(value, 80, true, "_-.@+")
}

func workbenchValidCRMAuditOperation(value string) bool {
	return workbenchValidCRMAuditText(value, 120, true, "_.-")
}

func workbenchValidCRMAuditEntityType(value string) bool {
	if value == "" {
		return true
	}
	return workbenchValidCRMAuditText(value, 80, true, "_") &&
		isValidCRMAuditEntityType(value)
}

func workbenchValidCRMAuditText(
	value string,
	maxLength int,
	required bool,
	allowedPunctuation string,
) bool {
	if value == "" {
		return !required
	}
	if value != strings.TrimSpace(value) ||
		len(value) > maxLength ||
		workbenchCRMAuditAbsolutePath(value) ||
		workbenchCRMAuditCredentialLike(value) {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune(allowedPunctuation, rune(character)) {
			continue
		}
		return false
	}
	return true
}

func workbenchCRMAuditAbsolutePath(value string) bool {
	return strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, `\\`) ||
		(len(value) >= 3 &&
			((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
			value[1] == ':' &&
			(value[2] == '\\' || value[2] == '/'))
}

func workbenchCRMAuditCredentialLike(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"sk-",
		"ghp_",
		"github_pat_",
		"glpat-",
		"hf_",
		"npm_",
		"rk_live_",
		"sk_live_",
		"xoxa-",
		"xoxb-",
		"xoxp-",
		"xoxs-",
		"akia",
		"asia",
		"aiza",
		"ya29.",
		"-----begin",
	} {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(lower, "eyj") && strings.Count(value, ".") == 2 {
		return true
	}

	var compact strings.Builder
	compact.Grow(len(lower))
	for index := 0; index < len(lower); index++ {
		character := lower[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') {
			compact.WriteByte(character)
		}
	}
	normalized := compact.String()
	for _, label := range []string{
		"accesskey",
		"accesstoken",
		"apikey",
		"authorization",
		"bearer",
		"clientkey",
		"clientsecret",
		"cookie",
		"credential",
		"password",
		"passwd",
		"privatekey",
		"providerpayload",
		"rawattachment",
		"secret",
		"token",
	} {
		if strings.Contains(normalized, label) {
			return true
		}
	}
	return false
}
