package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SourceJobIngestion struct {
	EventID string   `json:"event_id"`
	GroupID string   `json:"group_id"`
	Job     JobDraft `json:"job"`
}

type JobIngestionResult struct {
	Decision   JobIngestionDecision `json:"decision"`
	Job        Job                  `json:"job"`
	Provenance JobProvenance        `json:"provenance"`
	Idempotent bool                 `json:"idempotent"`
}

type sourceJobIdentity struct {
	groupID              string
	demandCode           string
	canonicalFingerprint string
}

func (s *CRMStore) IngestSourceJob(input SourceJobIngestion) (JobIngestionResult, error) {
	if s == nil {
		return JobIngestionResult{}, NewCRMValidationError("store", "is required")
	}
	eventID, err := normalizeCRMString("event_id", input.EventID, 80, true, false)
	if err != nil {
		return JobIngestionResult{}, err
	}
	groupID, err := normalizeCRMString("group_id", input.GroupID, 256, true, false)
	if err != nil {
		return JobIngestionResult{}, err
	}
	if !strings.HasSuffix(groupID, "@chatroom") {
		return JobIngestionResult{}, NewCRMValidationError(
			"group_id",
			"must be a WeChat group identifier",
		)
	}
	normalized, err := sourceJobNormalizeDraft(input.Job)
	if err != nil {
		return JobIngestionResult{}, err
	}
	snapshot := sourceJobSnapshot(normalized)
	fingerprint := sourceJobCanonicalFingerprint(snapshot)
	identity := sourceJobIdentity{
		groupID:              groupID,
		demandCode:           normalized.DemandCode,
		canonicalFingerprint: fingerprint,
	}

	var result JobIngestionResult
	err = s.UpdateWithMeta(CRMUpdateMeta{
		Actor:         "automation",
		Operation:     "job.source_ingest",
		EntityType:    "job",
		SourceEventID: eventID,
	}, func(state *CRMState, allocator *IDAllocator) error {
		eventIndex := sourceJobEventIndex(state.MessageEvents, eventID)
		if eventIndex < 0 {
			return NewCRMValidationError("event_id", "references a missing message event")
		}
		event := state.MessageEvents[eventIndex]
		if event.Source != crmMessageSource {
			return NewCRMValidationError("event_id", "must reference a WeChat message")
		}
		if event.ChatType != ChatTypeGroup {
			return NewCRMValidationError("event_id", "must reference a group message")
		}
		if event.GroupID != groupID {
			return NewCRMValidationError("group_id", "does not match the source message event")
		}
		if !sourceJobEnabledGroup(state.RecruitingProject.SourceGroups, groupID) {
			return NewCRMValidationError("group_id", "is not an enabled recruiting source group")
		}

		if provenance, ok := sourceJobProvenanceForMessage(
			state.JobProvenance,
			event.ID,
			event.MessageKey,
		); ok {
			if provenance.JobID == "" {
				return NewCRMConflictError("source message already has a non-job ingestion decision")
			}
			jobIndex := crmPipelineJobIndex(state.Jobs, provenance.JobID)
			if jobIndex < 0 {
				return NewCRMConflictError("source message provenance references a missing job")
			}
			result = JobIngestionResult{
				Decision:   provenance.Decision,
				Job:        state.Jobs[jobIndex],
				Provenance: provenance,
				Idempotent: true,
			}
			return nil
		}
		if len(state.JobProvenance) >= crmMaxEntityCount {
			return NewCRMValidationError("job_provenance", "entity limit exceeded")
		}

		contentSHA256 := sourceJobContentSHA256(event)
		jobID, err := sourceJobResolveIdentity(state.JobProvenance, identity)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		var job Job
		var decision JobIngestionDecision
		var jobRevision uint64

		if jobID == "" {
			if len(state.Jobs) >= crmMaxEntityCount {
				return NewCRMValidationError("jobs", "entity limit exceeded")
			}
			if len(state.DemandRevisions) >= crmMaxEntityCount {
				return NewCRMValidationError("demand_revisions", "entity limit exceeded")
			}
			id, err := allocator.Next(IDKindJob)
			if err != nil {
				return err
			}
			job = sourceJobFromDraft(id, normalized, now)
			jobRevision = 1
			revisionID, err := sourceJobExpectedDemandRevisionID(allocator)
			if err != nil {
				return err
			}
			state.Jobs = append(state.Jobs, job)
			result.Job = job
			result.Job.ActiveDemandRevisionID = revisionID
			result.Job.DemandRevisionNumber = jobRevision
			decision = JobIngestionCreate
		} else {
			jobIndex := crmPipelineJobIndex(state.Jobs, jobID)
			if jobIndex < 0 {
				return NewCRMConflictError("source identity provenance references a missing job")
			}
			current := state.Jobs[jobIndex]
			currentSnapshot := sourceJobCanonicalSnapshot(jobDemandSnapshot(current))
			switch {
			case reflect.DeepEqual(currentSnapshot, snapshot) &&
				sourceJobHasCurrentContent(
					state.JobProvenance,
					identity,
					current.ID,
					current.DemandRevisionNumber,
					contentSHA256,
				):
				decision = JobIngestionDuplicate
				jobRevision = current.DemandRevisionNumber
				result.Job = current
			case reflect.DeepEqual(currentSnapshot, snapshot):
				decision = JobIngestionNoop
				jobRevision = current.DemandRevisionNumber
				result.Job = current
			default:
				if len(state.DemandRevisions) >= crmMaxEntityCount {
					return NewCRMValidationError("demand_revisions", "entity limit exceeded")
				}
				if current.DemandRevisionNumber >= crmMaxIDCounter {
					return NewCRMConflictError("demand revision number exhausted for job " + current.ID)
				}
				revisionID, err := sourceJobExpectedDemandRevisionID(allocator)
				if err != nil {
					return err
				}
				crmPipelineApplyDemandSnapshot(&current, snapshot)
				current.Version++
				current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
				state.Jobs[jobIndex] = current
				jobRevision = current.DemandRevisionNumber + 1
				result.Job = current
				result.Job.ActiveDemandRevisionID = revisionID
				result.Job.DemandRevisionNumber = jobRevision
				decision = JobIngestionUpdate
			}
			job = state.Jobs[jobIndex]
		}

		provenanceID, err := allocator.Next(IDKindJobProvenance)
		if err != nil {
			return err
		}
		provenance := JobProvenance{
			ID:                   provenanceID,
			JobID:                job.ID,
			EventID:              event.ID,
			MessageKey:           event.MessageKey,
			GroupID:              event.GroupID,
			AuthorID:             event.AuthorID,
			SourceTimestamp:      time.Unix(event.Timestamp, 0).UTC(),
			ContentSHA256:        contentSHA256,
			CanonicalFingerprint: fingerprint,
			DemandCode:           normalized.DemandCode,
			Decision:             decision,
			JobRevision:          jobRevision,
			CreatedAt:            now,
		}
		state.JobProvenance = append(state.JobProvenance, provenance)
		if state.MessageEvents[eventIndex].Disposition != MessageDispositionSourceGroupJob {
			state.MessageEvents[eventIndex].Disposition = MessageDispositionSourceGroupJob
			state.MessageEvents[eventIndex].Version++
			state.MessageEvents[eventIndex].UpdatedAt = crmPipelineMutationTime(
				state.MessageEvents[eventIndex].UpdatedAt,
			)
		}
		result.Decision = decision
		result.Provenance = provenance
		return nil
	})
	if err != nil {
		return JobIngestionResult{}, err
	}
	return result, nil
}

func sourceJobNormalizeDraft(draft JobDraft) (JobDraft, error) {
	normalized, err := NormalizeJobDraft(draft)
	if err != nil {
		return JobDraft{}, err
	}
	if normalized.Status != JobStatusOpen {
		return JobDraft{}, NewCRMValidationError("status", "source jobs must be open")
	}
	sourceJobSortStrings(normalized.Requirements)
	sourceJobSortStrings(normalized.RequiredDocuments)
	return normalized, nil
}

func sourceJobSortStrings(values []string) {
	sort.Slice(values, func(i, j int) bool {
		left := strings.ToLower(values[i])
		right := strings.ToLower(values[j])
		if left == right {
			return values[i] < values[j]
		}
		return left < right
	})
}

func sourceJobSnapshot(draft JobDraft) JobDemandSnapshot {
	return sourceJobCanonicalSnapshot(JobDemandSnapshot{
		Title:                 draft.Title,
		Company:               draft.Company,
		Location:              draft.Location,
		EmploymentType:        draft.EmploymentType,
		Description:           draft.Description,
		Requirements:          append([]string{}, draft.Requirements...),
		DemandCode:            draft.DemandCode,
		SiteName:              draft.SiteName,
		OnsiteContact:         draft.OnsiteContact,
		Headcount:             draft.Headcount,
		Priority:              draft.Priority,
		StartAt:               cloneCRMTimePointer(draft.StartAt),
		DeadlineAt:            cloneCRMTimePointer(draft.DeadlineAt),
		Shift:                 draft.Shift,
		WorkSchedule:          draft.WorkSchedule,
		CompensationSummary:   draft.CompensationSummary,
		Accommodation:         draft.Accommodation,
		TransportRequirements: draft.TransportRequirements,
		RequiredDocuments:     append([]string{}, draft.RequiredDocuments...),
	})
}

func sourceJobCanonicalSnapshot(snapshot JobDemandSnapshot) JobDemandSnapshot {
	canonical := cloneJobDemandSnapshot(snapshot)
	sourceJobSortStrings(canonical.Requirements)
	sourceJobSortStrings(canonical.RequiredDocuments)
	return canonical
}

func sourceJobFromDraft(id string, draft JobDraft, now time.Time) Job {
	return Job{
		ID:                    id,
		Title:                 draft.Title,
		Company:               draft.Company,
		Location:              draft.Location,
		EmploymentType:        draft.EmploymentType,
		Description:           draft.Description,
		Requirements:          append([]string{}, draft.Requirements...),
		DemandCode:            draft.DemandCode,
		SiteName:              draft.SiteName,
		OnsiteContact:         draft.OnsiteContact,
		Headcount:             draft.Headcount,
		Priority:              draft.Priority,
		StartAt:               cloneCRMTimePointer(draft.StartAt),
		DeadlineAt:            cloneCRMTimePointer(draft.DeadlineAt),
		Shift:                 draft.Shift,
		WorkSchedule:          draft.WorkSchedule,
		CompensationSummary:   draft.CompensationSummary,
		Accommodation:         draft.Accommodation,
		TransportRequirements: draft.TransportRequirements,
		RequiredDocuments:     append([]string{}, draft.RequiredDocuments...),
		Owner:                 draft.Owner,
		Status:                JobStatusOpen,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func sourceJobResolveIdentity(
	provenance []JobProvenance,
	identity sourceJobIdentity,
) (string, error) {
	jobID := ""
	for _, candidate := range provenance {
		if candidate.JobID == "" || !sourceJobProvenanceMatchesIdentity(candidate, identity) {
			continue
		}
		if jobID == "" {
			jobID = candidate.JobID
			continue
		}
		if candidate.JobID != jobID {
			return "", NewCRMConflictError("source identity is represented by multiple jobs")
		}
	}
	return jobID, nil
}

func sourceJobProvenanceMatchesIdentity(
	provenance JobProvenance,
	identity sourceJobIdentity,
) bool {
	if provenance.GroupID != identity.groupID {
		return false
	}
	if identity.demandCode != "" {
		return provenance.DemandCode == identity.demandCode
	}
	return provenance.DemandCode == "" &&
		provenance.CanonicalFingerprint == identity.canonicalFingerprint
}

func sourceJobHasCurrentContent(
	provenance []JobProvenance,
	identity sourceJobIdentity,
	jobID string,
	jobRevision uint64,
	contentSHA256 string,
) bool {
	for _, candidate := range provenance {
		if candidate.JobID == jobID &&
			candidate.JobRevision == jobRevision &&
			candidate.ContentSHA256 == contentSHA256 &&
			sourceJobProvenanceMatchesIdentity(candidate, identity) {
			return true
		}
	}
	return false
}

func sourceJobProvenanceForMessage(
	provenance []JobProvenance,
	eventID, messageKey string,
) (JobProvenance, bool) {
	for _, candidate := range provenance {
		if candidate.EventID == eventID || candidate.MessageKey == messageKey {
			return candidate, true
		}
	}
	return JobProvenance{}, false
}

func sourceJobEnabledGroup(groups []RecruitingSourceGroup, groupID string) bool {
	for _, group := range groups {
		if group.GroupID == groupID {
			return group.Enabled
		}
	}
	return false
}

func sourceJobEventIndex(events []MessageEvent, eventID string) int {
	for index := range events {
		if events[index].ID == eventID {
			return index
		}
	}
	return -1
}

func sourceJobExpectedDemandRevisionID(allocator *IDAllocator) (string, error) {
	if allocator == nil || !allocator.active {
		return "", NewCRMValidationError("allocator", "is inactive")
	}
	next := allocator.counters.DemandRevision + 1
	if next == 0 || next > crmMaxIDCounter {
		return "", NewCRMConflictError("demand revision ID range exhausted")
	}
	return fmt.Sprintf("demand_%08d", next), nil
}

func sourceJobContentSHA256(event MessageEvent) string {
	hasher := sha256.New()
	writeCanonicalString(hasher, "source-job-content-v1")
	writeCanonicalString(hasher, event.Text)
	writeCanonicalString(hasher, strconv.Itoa(len(event.Kinds)))
	for _, kind := range event.Kinds {
		writeCanonicalString(hasher, kind)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sourceJobCanonicalFingerprint(snapshot JobDemandSnapshot) string {
	snapshot = sourceJobCanonicalSnapshot(snapshot)
	hasher := sha256.New()
	writeCanonicalString(hasher, "source-job-fingerprint-v1")
	writeCanonicalString(hasher, snapshot.Title)
	writeCanonicalString(hasher, snapshot.Company)
	writeCanonicalString(hasher, snapshot.Location)
	writeCanonicalString(hasher, snapshot.EmploymentType)
	writeCanonicalString(hasher, snapshot.Description)
	sourceJobWriteStrings(hasher, snapshot.Requirements)
	writeCanonicalString(hasher, snapshot.DemandCode)
	writeCanonicalString(hasher, snapshot.SiteName)
	writeCanonicalString(hasher, snapshot.OnsiteContact)
	writeCanonicalString(hasher, strconv.Itoa(snapshot.Headcount))
	writeCanonicalString(hasher, string(snapshot.Priority))
	sourceJobWriteTime(hasher, snapshot.StartAt)
	sourceJobWriteTime(hasher, snapshot.DeadlineAt)
	writeCanonicalString(hasher, snapshot.Shift)
	writeCanonicalString(hasher, snapshot.WorkSchedule)
	writeCanonicalString(hasher, snapshot.CompensationSummary)
	writeCanonicalString(hasher, snapshot.Accommodation)
	writeCanonicalString(hasher, snapshot.TransportRequirements)
	sourceJobWriteStrings(hasher, snapshot.RequiredDocuments)
	return hex.EncodeToString(hasher.Sum(nil))
}

func sourceJobWriteStrings(hasher byteWriter, values []string) {
	writeCanonicalString(hasher, strconv.Itoa(len(values)))
	for _, value := range values {
		writeCanonicalString(hasher, value)
	}
}

func sourceJobWriteTime(hasher byteWriter, value *time.Time) {
	if value == nil {
		writeCanonicalString(hasher, "")
		return
	}
	writeCanonicalString(hasher, value.UTC().Format(time.RFC3339Nano))
}
