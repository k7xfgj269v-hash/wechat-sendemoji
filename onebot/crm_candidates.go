package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

type crmCandidateListFilter struct {
	Query  string
	Status CandidateStatus
	Owner  string
	Source string
	Limit  int
	Offset int
}

type crmCandidateLinkResult struct {
	Candidate Candidate    `json:"candidate"`
	Event     MessageEvent `json:"event"`
	Created   bool         `json:"-"`
}

type crmCandidateNotFoundError struct {
	resource string
}

func (e *crmCandidateNotFoundError) Error() string {
	return e.resource + " not found"
}

func crmCandidateCreate(store *CRMStore, draft CandidateDraft) (Candidate, error) {
	if strings.TrimSpace(draft.SourceEventID) != "" {
		return Candidate{}, NewCRMValidationError("source_event_id", "can only be set by message linkage")
	}
	normalized, err := NormalizeCandidateDraft(draft)
	if err != nil {
		return Candidate{}, err
	}

	var created Candidate
	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		candidateIndex, err := crmCandidateResolveIdentity(
			state.Candidates,
			crmCandidateIdentityFromDraft(normalized),
		)
		if err != nil {
			return err
		}
		if candidateIndex >= 0 {
			now := crmCandidateMutationTime(state.Candidates[candidateIndex].UpdatedAt)
			candidate, changed := crmCandidateEnrichIdentity(state.Candidates[candidateIndex], normalized, now)
			if changed {
				state.Candidates[candidateIndex] = candidate
			}
			created = candidate
			return nil
		}

		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = crmCandidateFromDraft(id, normalized, now)
		state.Candidates = append(state.Candidates, created)
		return nil
	})
	if err != nil {
		return Candidate{}, err
	}
	return created, nil
}

func crmCandidateList(store *CRMStore, filter crmCandidateListFilter) (int, []CandidateListItem, error) {
	normalized, err := crmCandidateNormalizeListFilter(filter)
	if err != nil {
		return 0, nil, err
	}

	state := store.Snapshot()
	activeLeadCounts := make(map[string]int, len(state.Candidates))
	leadCandidates := make(map[string]string, len(state.Leads))
	for _, lead := range state.Leads {
		leadCandidates[lead.ID] = lead.CandidateID
		if !IsTerminalLeadStage(lead.Stage) {
			activeLeadCounts[lead.CandidateID]++
		}
	}
	openTaskCounts := make(map[string]int, len(state.Candidates))
	for _, task := range state.Tasks {
		if task.Status != FollowUpStatusOpen {
			continue
		}
		candidateID := task.CandidateID
		if candidateID == "" {
			candidateID = leadCandidates[task.LeadID]
		}
		if candidateID != "" {
			openTaskCounts[candidateID]++
		}
	}

	items := make([]CandidateListItem, 0, len(state.Candidates))
	for _, candidate := range state.Candidates {
		if !crmCandidateMatchesFilter(candidate, normalized) {
			continue
		}
		items = append(items, CandidateListItem{
			Candidate:       candidate,
			ActiveLeadCount: activeLeadCounts[candidate.ID],
			OpenTaskCount:   openTaskCounts[candidate.ID],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Candidate.UpdatedAt.Equal(items[j].Candidate.UpdatedAt) {
			return items[i].Candidate.ID < items[j].Candidate.ID
		}
		return items[i].Candidate.UpdatedAt.After(items[j].Candidate.UpdatedAt)
	})

	total := len(items)
	if normalized.Offset >= total {
		return total, []CandidateListItem{}, nil
	}
	end := normalized.Offset + normalized.Limit
	if end > total {
		end = total
	}
	return total, items[normalized.Offset:end], nil
}

func crmCandidateGetDetail(store *CRMStore, id string) (CandidateDetail, error) {
	normalizedID, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return CandidateDetail{}, err
	}

	state := store.Snapshot()
	candidateIndex := crmCandidateIndexByID(state.Candidates, normalizedID)
	if candidateIndex < 0 {
		return CandidateDetail{}, &crmCandidateNotFoundError{resource: "candidate"}
	}

	detail := CandidateDetail{
		Candidate:       state.Candidates[candidateIndex],
		MessageEventIDs: []string{},
		LeadIDs:         []string{},
		OpenTaskIDs:     []string{},
	}
	leadCandidates := make(map[string]string, len(state.Leads))
	for _, lead := range state.Leads {
		leadCandidates[lead.ID] = lead.CandidateID
		if lead.CandidateID == normalizedID {
			detail.LeadIDs = append(detail.LeadIDs, lead.ID)
		}
	}
	for _, event := range state.MessageEvents {
		if event.CandidateID == normalizedID {
			detail.MessageEventIDs = append(detail.MessageEventIDs, event.ID)
		}
	}
	for _, task := range state.Tasks {
		if task.Status != FollowUpStatusOpen {
			continue
		}
		candidateID := task.CandidateID
		if candidateID == "" {
			candidateID = leadCandidates[task.LeadID]
		}
		if candidateID == normalizedID {
			detail.OpenTaskIDs = append(detail.OpenTaskIDs, task.ID)
		}
	}
	for _, note := range state.Notes {
		if note.TargetType == NoteTargetCandidate && note.TargetID == normalizedID {
			detail.NoteCount++
		}
	}
	sort.Strings(detail.MessageEventIDs)
	sort.Strings(detail.LeadIDs)
	sort.Strings(detail.OpenTaskIDs)
	return detail, nil
}

func crmCandidateUpdate(store *CRMStore, id string, update CandidateUpdate) (Candidate, error) {
	normalizedID, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Candidate{}, err
	}
	normalized, err := crmCandidateNormalizeUpdate(update)
	if err != nil {
		return Candidate{}, err
	}

	var updated Candidate
	err = store.Update(func(state *CRMState, _ *IDAllocator) error {
		index := crmCandidateIndexByID(state.Candidates, normalizedID)
		if index < 0 {
			return &crmCandidateNotFoundError{resource: "candidate"}
		}
		current := state.Candidates[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("candidate version conflict")
		}

		if normalized.FullName != nil {
			current.FullName = *normalized.FullName
		}
		if normalized.WeChatID != nil {
			current.WeChatID = *normalized.WeChatID
		}
		if normalized.Phone != nil {
			current.Phone = *normalized.Phone
		}
		if normalized.Email != nil {
			current.Email = *normalized.Email
		}
		if normalized.Location != nil {
			current.Location = *normalized.Location
		}
		if normalized.Source != nil {
			current.Source = *normalized.Source
		}
		if normalized.Skills != nil {
			current.Skills = append([]string{}, (*normalized.Skills)...)
		}
		if normalized.Owner != nil {
			current.Owner = *normalized.Owner
		}
		if normalized.Status != nil && current.Status != *normalized.Status {
			if err := ValidateCandidateStatusTransition(current.Status, *normalized.Status); err != nil {
				return err
			}
			current.Status = *normalized.Status
		}
		if normalized.Summary != nil {
			current.Summary = *normalized.Summary
		}
		current.Version++
		current.UpdatedAt = crmCandidateMutationTime(current.UpdatedAt)
		state.Candidates[index] = current
		updated = current
		return nil
	})
	if err != nil {
		return Candidate{}, err
	}
	return updated, nil
}

func crmCandidateLinkMessage(
	ctx context.Context,
	store *CRMStore,
	extractor CandidateExtractor,
	request MessageCandidateLink,
) (crmCandidateLinkResult, error) {
	normalized, err := crmCandidateNormalizeLinkRequest(request)
	if err != nil {
		return crmCandidateLinkResult{}, err
	}

	var extracted CandidateDraft
	if normalized.Candidate != nil {
		if extractor == nil {
			return crmCandidateLinkResult{}, NewCRMValidationError("candidate_extractor", "is required")
		}
		state := store.Snapshot()
		eventIndex := crmCandidateEventIndexByID(state.MessageEvents, normalized.MessageEventID)
		if eventIndex < 0 {
			return crmCandidateLinkResult{}, &crmCandidateNotFoundError{resource: "message event"}
		}
		event := state.MessageEvents[eventIndex]
		if event.CandidateID != "" {
			return crmCandidateLinkResult{}, NewCRMConflictError("message event is already linked")
		}
		if event.Version != normalized.ExpectedEventVersion {
			return crmCandidateLinkResult{}, NewCRMConflictError("message event version conflict")
		}
		boundDraft, err := crmCandidateBindPrivateEventIdentity(event, *normalized.Candidate)
		if err != nil {
			return crmCandidateLinkResult{}, err
		}
		extracted, err = extractor.Draft(ctx, event, boundDraft)
		if err != nil {
			return crmCandidateLinkResult{}, err
		}
		extracted, err = crmCandidateBindPrivateEventIdentity(event, extracted)
		if err != nil {
			return crmCandidateLinkResult{}, err
		}
		extracted, err = NormalizeCandidateDraft(extracted)
		if err != nil {
			return crmCandidateLinkResult{}, err
		}
		if extracted.SourceEventID != event.ID {
			return crmCandidateLinkResult{}, NewCRMValidationError("source_event_id", "must match the message event")
		}
	}

	var result crmCandidateLinkResult
	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		eventIndex := crmCandidateEventIndexByID(state.MessageEvents, normalized.MessageEventID)
		if eventIndex < 0 {
			return &crmCandidateNotFoundError{resource: "message event"}
		}
		event := state.MessageEvents[eventIndex]
		if event.CandidateID != "" {
			return NewCRMConflictError("message event is already linked")
		}
		if event.Version != normalized.ExpectedEventVersion {
			return NewCRMConflictError("message event version conflict")
		}

		now := crmCandidateMutationTime(event.UpdatedAt)
		var candidate Candidate
		if normalized.CandidateID != "" {
			candidateIndex := crmCandidateIndexByID(state.Candidates, normalized.CandidateID)
			if candidateIndex < 0 {
				return &crmCandidateNotFoundError{resource: "candidate"}
			}
			candidate = state.Candidates[candidateIndex]
		} else {
			candidateIndex, err := crmCandidateResolveIdentity(
				state.Candidates,
				crmCandidateIdentityFromDraft(extracted),
			)
			if err != nil {
				return err
			}
			if candidateIndex >= 0 {
				var changed bool
				candidate, changed = crmCandidateEnrichIdentity(state.Candidates[candidateIndex], extracted, now)
				if changed {
					state.Candidates[candidateIndex] = candidate
				}
			} else {
				id, err := allocator.Next(IDKindCandidate)
				if err != nil {
					return err
				}
				candidate = crmCandidateFromDraft(id, extracted, now)
				state.Candidates = append(state.Candidates, candidate)
				result.Created = true
			}
		}

		event.CandidateID = candidate.ID
		event.ReviewStatus = ReviewStatusReviewed
		event.Version++
		event.UpdatedAt = now
		state.MessageEvents[eventIndex] = event
		result.Candidate = candidate
		result.Event = event
		return nil
	})
	if err != nil {
		return crmCandidateLinkResult{}, err
	}
	return result, nil
}

func crmCandidateNormalizeListFilter(filter crmCandidateListFilter) (crmCandidateListFilter, error) {
	var err error
	if filter.Query, err = NormalizeCRMSearch(filter.Query); err != nil {
		return crmCandidateListFilter{}, err
	}
	if filter.Owner, err = normalizeCRMString("owner", filter.Owner, 80, false, false); err != nil {
		return crmCandidateListFilter{}, err
	}
	if filter.Source, err = normalizeCRMString("source", filter.Source, 120, false, false); err != nil {
		return crmCandidateListFilter{}, err
	}
	if filter.Status != "" && !IsValidCandidateStatus(filter.Status) {
		return crmCandidateListFilter{}, NewCRMValidationError("status", "invalid candidate status")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmCandidateListFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmCandidateListFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmCandidateNormalizeUpdate(update CandidateUpdate) (CandidateUpdate, error) {
	if update.ExpectedVersion == 0 {
		return CandidateUpdate{}, NewCRMValidationError("expected_version", "must be greater than zero")
	}
	if !crmCandidateUpdateHasFields(update) {
		return CandidateUpdate{}, NewCRMValidationError("candidate", "at least one update field is required")
	}

	if update.FullName != nil {
		value, normalizeErr := normalizeCRMString("full_name", *update.FullName, 120, true, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.FullName = &value
	}
	if update.WeChatID != nil {
		value, normalizeErr := normalizeCRMString("wechat_id", *update.WeChatID, 128, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.WeChatID = &value
	}
	if update.Phone != nil {
		value, normalizeErr := normalizeCRMString("phone", *update.Phone, 40, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Phone = &value
	}
	if update.Email != nil {
		value, normalizeErr := normalizeCRMString("email", *update.Email, 254, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Email = &value
	}
	if update.Location != nil {
		value, normalizeErr := normalizeCRMString("location", *update.Location, 160, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Location = &value
	}
	if update.Source != nil {
		value, normalizeErr := normalizeCRMString("source", *update.Source, 120, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Source = &value
	}
	if update.Skills != nil {
		values, normalizeErr := normalizeCRMStringList("skills", *update.Skills, 32, 64)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Skills = &values
	}
	if update.Owner != nil {
		value, normalizeErr := normalizeCRMString("owner", *update.Owner, 80, false, false)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Owner = &value
	}
	if update.Status != nil && !IsValidCandidateStatus(*update.Status) {
		return CandidateUpdate{}, NewCRMValidationError("status", "invalid candidate status")
	}
	if update.Summary != nil {
		value, normalizeErr := normalizeCRMString("summary", *update.Summary, 4000, false, true)
		if normalizeErr != nil {
			return CandidateUpdate{}, normalizeErr
		}
		update.Summary = &value
	}
	return update, nil
}

func crmCandidateNormalizeLinkRequest(request MessageCandidateLink) (MessageCandidateLink, error) {
	var err error
	if request.MessageEventID, err = normalizeCRMString("message_event_id", request.MessageEventID, 80, true, false); err != nil {
		return MessageCandidateLink{}, err
	}
	if request.ExpectedEventVersion == 0 {
		return MessageCandidateLink{}, NewCRMValidationError("expected_event_version", "must be greater than zero")
	}
	request.CandidateID = strings.TrimSpace(request.CandidateID)
	if (request.CandidateID == "") == (request.Candidate == nil) {
		return MessageCandidateLink{}, NewCRMValidationError("candidate_link", "exactly one of candidate_id or candidate is required")
	}
	if request.CandidateID != "" {
		if request.CandidateID, err = normalizeCRMString("candidate_id", request.CandidateID, 80, true, false); err != nil {
			return MessageCandidateLink{}, err
		}
	}
	if request.Candidate != nil {
		draft := *request.Candidate
		if strings.TrimSpace(draft.SourceEventID) != "" {
			return MessageCandidateLink{}, NewCRMValidationError("source_event_id", "is managed by message linkage")
		}
		draft.SourceEventID = ""
		draft.Skills = append([]string{}, draft.Skills...)
		request.Candidate = &draft
	}
	return request, nil
}

func crmCandidateMatchesFilter(candidate Candidate, filter crmCandidateListFilter) bool {
	if filter.Status != "" && candidate.Status != filter.Status {
		return false
	}
	if filter.Owner != "" && candidate.Owner != filter.Owner {
		return false
	}
	if filter.Source != "" && candidate.Source != filter.Source {
		return false
	}
	if filter.Query == "" {
		return true
	}
	query := strings.ToLower(filter.Query)
	values := []string{
		candidate.ID,
		candidate.FullName,
		candidate.WeChatID,
		candidate.Phone,
		candidate.Email,
		candidate.Location,
		candidate.Source,
		candidate.Owner,
		candidate.Summary,
		string(candidate.Status),
	}
	values = append(values, candidate.Skills...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func crmCandidateUpdateHasFields(update CandidateUpdate) bool {
	return update.FullName != nil ||
		update.WeChatID != nil ||
		update.Phone != nil ||
		update.Email != nil ||
		update.Location != nil ||
		update.Source != nil ||
		update.Skills != nil ||
		update.Owner != nil ||
		update.Status != nil ||
		update.Summary != nil
}

func crmCandidateFromDraft(id string, draft CandidateDraft, now time.Time) Candidate {
	return Candidate{
		ID:            id,
		FullName:      draft.FullName,
		WeChatID:      draft.WeChatID,
		Phone:         draft.Phone,
		Email:         draft.Email,
		Location:      draft.Location,
		Source:        draft.Source,
		SourceEventID: draft.SourceEventID,
		Skills:        append([]string{}, draft.Skills...),
		Owner:         draft.Owner,
		Status:        draft.Status,
		Summary:       draft.Summary,
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func crmCandidateMutationTime(previous time.Time) time.Time {
	now := time.Now().UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}

func crmCandidateIndexByID(candidates []Candidate, id string) int {
	for i := range candidates {
		if candidates[i].ID == id {
			return i
		}
	}
	return -1
}

func crmCandidateEventIndexByID(events []MessageEvent, id string) int {
	for i := range events {
		if events[i].ID == id {
			return i
		}
	}
	return -1
}
