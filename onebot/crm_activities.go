package main

import (
	"errors"
	"sort"
	"time"
)

type crmActivityNotFoundError struct {
	entity string
	id     string
}

func (e *crmActivityNotFoundError) Error() string {
	if e.id == "" {
		return e.entity + " not found"
	}
	return e.entity + " " + e.id + " not found"
}

type crmActivityService struct {
	store *CRMStore
	now   func() time.Time
}

type crmActivityNoteFilter struct {
	TargetType NoteTargetType
	TargetID   string
	Limit      int
	Offset     int
}

type crmActivityTaskFilter struct {
	Owner       *string
	Status      FollowUpStatus
	Overdue     *bool
	DueFrom     *time.Time
	DueTo       *time.Time
	CandidateID string
	LeadID      string
	Limit       int
	Offset      int
}

type crmActivityNoteListResponse struct {
	Total int    `json:"total"`
	Notes []Note `json:"notes"`
}

type crmActivityTaskListResponse struct {
	Total int            `json:"total"`
	Tasks []TaskListItem `json:"tasks"`
}

func crmActivityNewService(store *CRMStore) *crmActivityService {
	return &crmActivityService{
		store: store,
		now:   time.Now,
	}
}

func (s *crmActivityService) CreateNote(create NoteCreate) (Note, error) {
	create, err := NormalizeNoteCreate(create)
	if err != nil {
		return Note{}, err
	}
	if err := s.validateStore(); err != nil {
		return Note{}, err
	}

	var created Note
	err = s.store.Update(func(state *CRMState, allocator *IDAllocator) error {
		if err := crmActivityRequireNoteTarget(state, create.TargetType, create.TargetID); err != nil {
			return err
		}
		id, err := allocator.Next(IDKindNote)
		if err != nil {
			return err
		}
		created = Note{
			ID:         id,
			TargetType: create.TargetType,
			TargetID:   create.TargetID,
			Author:     create.Author,
			Body:       create.Body,
			CreatedAt:  s.currentTime(),
		}
		state.Notes = append(state.Notes, created)
		return nil
	})
	if err != nil {
		return Note{}, err
	}
	return created, nil
}

func (s *crmActivityService) ListNotes(filter crmActivityNoteFilter) (crmActivityNoteListResponse, error) {
	filter, err := crmActivityNormalizeNoteFilter(filter)
	if err != nil {
		return crmActivityNoteListResponse{}, err
	}
	if err := s.validateStore(); err != nil {
		return crmActivityNoteListResponse{}, err
	}

	state := s.store.Snapshot()
	if err := crmActivityRequireNoteTarget(&state, filter.TargetType, filter.TargetID); err != nil {
		return crmActivityNoteListResponse{}, err
	}
	notes := make([]Note, 0)
	for _, note := range state.Notes {
		if note.TargetType == filter.TargetType && note.TargetID == filter.TargetID {
			notes = append(notes, note)
		}
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].CreatedAt.Equal(notes[j].CreatedAt) {
			return notes[i].ID < notes[j].ID
		}
		return notes[i].CreatedAt.Before(notes[j].CreatedAt)
	})

	total := len(notes)
	notes = crmActivityPage(notes, filter.Offset, filter.Limit)
	return crmActivityNoteListResponse{Total: total, Notes: notes}, nil
}

func (s *crmActivityService) CreateTask(create TaskCreate) (FollowUpTask, error) {
	create, err := NormalizeTaskCreate(create)
	if err != nil {
		return FollowUpTask{}, err
	}
	if err := s.validateStore(); err != nil {
		return FollowUpTask{}, err
	}

	var created FollowUpTask
	err = s.store.Update(func(state *CRMState, allocator *IDAllocator) error {
		if err := crmActivityValidateTaskReferences(state, create.CandidateID, create.LeadID); err != nil {
			return err
		}
		id, err := allocator.Next(IDKindTask)
		if err != nil {
			return err
		}
		now := s.currentTime()
		created = FollowUpTask{
			ID:          id,
			Title:       create.Title,
			Description: create.Description,
			Owner:       create.Owner,
			Status:      FollowUpStatusOpen,
			DueAt:       create.DueAt,
			CandidateID: create.CandidateID,
			LeadID:      create.LeadID,
			Version:     1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		state.Tasks = append(state.Tasks, created)
		return nil
	})
	if err != nil {
		return FollowUpTask{}, err
	}
	return created, nil
}

func (s *crmActivityService) ListTasks(filter crmActivityTaskFilter) (crmActivityTaskListResponse, error) {
	filter, err := crmActivityNormalizeTaskFilter(filter)
	if err != nil {
		return crmActivityTaskListResponse{}, err
	}
	if err := s.validateStore(); err != nil {
		return crmActivityTaskListResponse{}, err
	}

	state := s.store.Snapshot()
	now := s.currentTime()
	tasks := make([]TaskListItem, 0)
	for _, task := range state.Tasks {
		item := crmActivityProjectTask(state, task)
		if filter.Owner != nil && task.Owner != *filter.Owner {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.Overdue != nil {
			overdue := task.Status == FollowUpStatusOpen && task.DueAt.Before(now)
			if overdue != *filter.Overdue {
				continue
			}
		}
		if filter.DueFrom != nil && task.DueAt.Before(*filter.DueFrom) {
			continue
		}
		if filter.DueTo != nil && task.DueAt.After(*filter.DueTo) {
			continue
		}
		if filter.CandidateID != "" && crmActivityTaskCandidateID(state, task) != filter.CandidateID {
			continue
		}
		if filter.LeadID != "" && task.LeadID != filter.LeadID {
			continue
		}
		tasks = append(tasks, item)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Task.DueAt.Equal(tasks[j].Task.DueAt) {
			return tasks[i].Task.ID < tasks[j].Task.ID
		}
		return tasks[i].Task.DueAt.Before(tasks[j].Task.DueAt)
	})

	total := len(tasks)
	tasks = crmActivityPage(tasks, filter.Offset, filter.Limit)
	return crmActivityTaskListResponse{Total: total, Tasks: tasks}, nil
}

func (s *crmActivityService) GetTask(id string) (TaskListItem, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return TaskListItem{}, err
	}
	if err := s.validateStore(); err != nil {
		return TaskListItem{}, err
	}

	state := s.store.Snapshot()
	for _, task := range state.Tasks {
		if task.ID == id {
			return crmActivityProjectTask(state, task), nil
		}
	}
	return TaskListItem{}, &crmActivityNotFoundError{entity: "task", id: id}
}

func (s *crmActivityService) UpdateTask(id string, update TaskUpdate) (FollowUpTask, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FollowUpTask{}, err
	}
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return FollowUpTask{}, err
	}
	if err := s.validateStore(); err != nil {
		return FollowUpTask{}, err
	}

	var updated FollowUpTask
	err = s.store.Update(func(state *CRMState, _ *IDAllocator) error {
		index := crmActivityTaskIndex(state, id)
		if index < 0 {
			return &crmActivityNotFoundError{entity: "task", id: id}
		}
		task := state.Tasks[index]
		if task.Version != update.ExpectedVersion {
			return NewCRMConflictError("task version is stale")
		}

		merged := TaskCreate{
			Title:       task.Title,
			Description: task.Description,
			Owner:       task.Owner,
			DueAt:       task.DueAt,
			CandidateID: task.CandidateID,
			LeadID:      task.LeadID,
		}
		if update.Title != nil {
			merged.Title = *update.Title
		}
		if update.Description != nil {
			merged.Description = *update.Description
		}
		if update.Owner != nil {
			merged.Owner = *update.Owner
		}
		if update.DueAt != nil {
			merged.DueAt = *update.DueAt
		}
		if update.CandidateID != nil {
			merged.CandidateID = *update.CandidateID
		}
		if update.LeadID != nil {
			merged.LeadID = *update.LeadID
		}
		merged, err = NormalizeTaskCreate(merged)
		if err != nil {
			return err
		}
		if err := crmActivityValidateTaskReferences(state, merged.CandidateID, merged.LeadID); err != nil {
			return err
		}

		task.Title = merged.Title
		task.Description = merged.Description
		task.Owner = merged.Owner
		task.DueAt = merged.DueAt
		task.CandidateID = merged.CandidateID
		task.LeadID = merged.LeadID
		task.Version++
		task.UpdatedAt = s.mutationTime(task.UpdatedAt)
		state.Tasks[index] = task
		updated = task
		return nil
	})
	if err != nil {
		return FollowUpTask{}, err
	}
	return updated, nil
}

func (s *crmActivityService) SetTaskStatus(id string, request TaskStatusRequest) (FollowUpTask, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FollowUpTask{}, err
	}
	if err := validateCRMVersion("expected_version", request.ExpectedVersion); err != nil {
		return FollowUpTask{}, err
	}
	if !IsValidFollowUpStatus(request.Status) {
		return FollowUpTask{}, NewCRMValidationError("status", "invalid follow-up status")
	}
	if err := s.validateStore(); err != nil {
		return FollowUpTask{}, err
	}

	var updated FollowUpTask
	err = s.store.Update(func(state *CRMState, _ *IDAllocator) error {
		index := crmActivityTaskIndex(state, id)
		if index < 0 {
			return &crmActivityNotFoundError{entity: "task", id: id}
		}
		task := state.Tasks[index]
		if task.Version != request.ExpectedVersion {
			return NewCRMConflictError("task version is stale")
		}
		if err := ValidateFollowUpStatusTransition(task.Status, request.Status); err != nil {
			return err
		}

		now := s.mutationTime(task.UpdatedAt)
		task.Status = request.Status
		task.Version++
		task.UpdatedAt = now
		if request.Status == FollowUpStatusCompleted {
			completedAt := now
			task.CompletedAt = &completedAt
		} else {
			task.CompletedAt = nil
		}
		state.Tasks[index] = task
		updated = task
		return nil
	})
	if err != nil {
		return FollowUpTask{}, err
	}
	return updated, nil
}

func (s *crmActivityService) validateStore() error {
	if s == nil || s.store == nil {
		return NewCRMValidationError("store", "is required")
	}
	return nil
}

func (s *crmActivityService) currentTime() time.Time {
	now := time.Now
	if s != nil && s.now != nil {
		now = s.now
	}
	return now().UTC()
}

func (s *crmActivityService) mutationTime(notBefore time.Time) time.Time {
	now := s.currentTime()
	if now.Before(notBefore) {
		return notBefore
	}
	return now
}

func crmActivityNormalizeNoteFilter(filter crmActivityNoteFilter) (crmActivityNoteFilter, error) {
	if !IsValidNoteTargetType(filter.TargetType) {
		return crmActivityNoteFilter{}, NewCRMValidationError("target_type", "invalid note target type")
	}
	targetID, err := normalizeCRMString("target_id", filter.TargetID, 80, true, false)
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	limit, offset, err := crmActivityNormalizePage(filter.Limit, filter.Offset)
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	filter.TargetID = targetID
	filter.Limit = limit
	filter.Offset = offset
	return filter, nil
}

func crmActivityNormalizeTaskFilter(filter crmActivityTaskFilter) (crmActivityTaskFilter, error) {
	var err error
	if filter.Owner != nil {
		owner, normalizeErr := normalizeCRMString("owner", *filter.Owner, 80, false, false)
		if normalizeErr != nil {
			return crmActivityTaskFilter{}, normalizeErr
		}
		filter.Owner = &owner
	}
	if filter.Status != "" && !IsValidFollowUpStatus(filter.Status) {
		return crmActivityTaskFilter{}, NewCRMValidationError("status", "invalid follow-up status")
	}
	if filter.DueFrom != nil {
		if filter.DueFrom.IsZero() {
			return crmActivityTaskFilter{}, NewCRMValidationError("due_from", "is required")
		}
		value := filter.DueFrom.UTC()
		filter.DueFrom = &value
	}
	if filter.DueTo != nil {
		if filter.DueTo.IsZero() {
			return crmActivityTaskFilter{}, NewCRMValidationError("due_to", "is required")
		}
		value := filter.DueTo.UTC()
		filter.DueTo = &value
	}
	if filter.DueFrom != nil && filter.DueTo != nil && filter.DueFrom.After(*filter.DueTo) {
		return crmActivityTaskFilter{}, NewCRMValidationError("due_range", "due_from must not be after due_to")
	}
	if filter.CandidateID, err = normalizeCRMString("candidate_id", filter.CandidateID, 80, false, false); err != nil {
		return crmActivityTaskFilter{}, err
	}
	if filter.LeadID, err = normalizeCRMString("lead_id", filter.LeadID, 80, false, false); err != nil {
		return crmActivityTaskFilter{}, err
	}
	filter.Limit, filter.Offset, err = crmActivityNormalizePage(filter.Limit, filter.Offset)
	if err != nil {
		return crmActivityTaskFilter{}, err
	}
	return filter, nil
}

func crmActivityNormalizePage(limit, offset int) (int, int, error) {
	if limit == 0 {
		limit = CRMDefaultListLimit
	}
	if limit < 1 || limit > CRMMaxListLimit {
		return 0, 0, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if offset < 0 {
		return 0, 0, NewCRMValidationError("offset", "must not be negative")
	}
	return limit, offset, nil
}

func crmActivityRequireNoteTarget(state *CRMState, targetType NoteTargetType, targetID string) error {
	switch targetType {
	case NoteTargetCandidate:
		if crmActivityCandidateIndex(state, targetID) < 0 {
			return &crmActivityNotFoundError{entity: "candidate", id: targetID}
		}
	case NoteTargetLead:
		if crmActivityLeadIndex(state, targetID) < 0 {
			return &crmActivityNotFoundError{entity: "lead", id: targetID}
		}
	default:
		return NewCRMValidationError("target_type", "invalid note target type")
	}
	return nil
}

func crmActivityValidateTaskReferences(state *CRMState, candidateID, leadID string) error {
	if candidateID != "" && crmActivityCandidateIndex(state, candidateID) < 0 {
		return &crmActivityNotFoundError{entity: "candidate", id: candidateID}
	}
	if leadID == "" {
		return nil
	}
	leadIndex := crmActivityLeadIndex(state, leadID)
	if leadIndex < 0 {
		return &crmActivityNotFoundError{entity: "lead", id: leadID}
	}
	if candidateID != "" && state.Leads[leadIndex].CandidateID != candidateID {
		return NewCRMValidationError("candidate_id", "does not match the lead candidate")
	}
	return nil
}

func crmActivityProjectTask(state CRMState, task FollowUpTask) TaskListItem {
	item := TaskListItem{Task: task}
	candidateID := crmActivityTaskCandidateID(state, task)
	if candidateIndex := crmActivityCandidateIndex(&state, candidateID); candidateIndex >= 0 {
		item.CandidateName = state.Candidates[candidateIndex].FullName
	}
	if task.LeadID == "" {
		return item
	}
	leadIndex := crmActivityLeadIndex(&state, task.LeadID)
	if leadIndex < 0 {
		return item
	}
	lead := state.Leads[leadIndex]
	item.LeadStage = lead.Stage
	item.JobID = lead.JobID
	if jobIndex := crmActivityJobIndex(&state, lead.JobID); jobIndex >= 0 {
		item.JobTitle = state.Jobs[jobIndex].Title
	}
	return item
}

func crmActivityTaskCandidateID(state CRMState, task FollowUpTask) string {
	if task.CandidateID != "" {
		return task.CandidateID
	}
	if task.LeadID != "" {
		if leadIndex := crmActivityLeadIndex(&state, task.LeadID); leadIndex >= 0 {
			return state.Leads[leadIndex].CandidateID
		}
	}
	return ""
}

func crmActivityCandidateIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Candidates {
		if state.Candidates[index].ID == id {
			return index
		}
	}
	return -1
}

func crmActivityJobIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Jobs {
		if state.Jobs[index].ID == id {
			return index
		}
	}
	return -1
}

func crmActivityLeadIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Leads {
		if state.Leads[index].ID == id {
			return index
		}
	}
	return -1
}

func crmActivityTaskIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Tasks {
		if state.Tasks[index].ID == id {
			return index
		}
	}
	return -1
}

func crmActivityPage[T any](values []T, offset, limit int) []T {
	if offset >= len(values) {
		return []T{}
	}
	end := offset + limit
	if end > len(values) {
		end = len(values)
	}
	result := make([]T, end-offset)
	copy(result, values[offset:end])
	return result
}

func crmActivityIsNotFound(err error) bool {
	var target *crmActivityNotFoundError
	return errors.As(err, &target)
}
