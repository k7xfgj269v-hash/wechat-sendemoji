package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func NormalizeCandidateDraft(draft CandidateDraft) (CandidateDraft, error) {
	var err error
	if draft.FullName, err = normalizeCRMString("full_name", draft.FullName, 120, true, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.WeChatID, err = normalizeCRMString("wechat_id", draft.WeChatID, 128, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Phone, err = normalizeCRMString("phone", draft.Phone, 40, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Email, err = normalizeCRMString("email", draft.Email, 254, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Location, err = normalizeCRMString("location", draft.Location, 160, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Source, err = normalizeCRMString("source", draft.Source, 120, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.SourceEventID, err = normalizeCRMString("source_event_id", draft.SourceEventID, 80, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Skills, err = normalizeCRMStringList("skills", draft.Skills, 32, 64); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Owner, err = normalizeCRMString("owner", draft.Owner, 80, false, false); err != nil {
		return CandidateDraft{}, err
	}
	if draft.Status == "" {
		draft.Status = CandidateStatusActive
	}
	if !IsValidCandidateStatus(draft.Status) {
		return CandidateDraft{}, NewCRMValidationError("status", "invalid candidate status")
	}
	if draft.Summary, err = normalizeCRMString("summary", draft.Summary, 4000, false, true); err != nil {
		return CandidateDraft{}, err
	}
	return draft, nil
}

func NormalizeJobDraft(draft JobDraft) (JobDraft, error) {
	var err error
	if draft.Title, err = normalizeCRMString("title", draft.Title, 160, true, false); err != nil {
		return JobDraft{}, err
	}
	if draft.Company, err = normalizeCRMString("company", draft.Company, 160, true, false); err != nil {
		return JobDraft{}, err
	}
	if draft.Location, err = normalizeCRMString("location", draft.Location, 160, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.EmploymentType, err = normalizeCRMString("employment_type", draft.EmploymentType, 80, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.Description, err = normalizeCRMString("description", draft.Description, 8000, false, true); err != nil {
		return JobDraft{}, err
	}
	if draft.Requirements, err = normalizeCRMStringList("requirements", draft.Requirements, 64, 1000); err != nil {
		return JobDraft{}, err
	}
	if draft.DemandCode, err = normalizeCRMString("demand_code", draft.DemandCode, 120, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.SiteName, err = normalizeCRMString("site_name", draft.SiteName, 160, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.OnsiteContact, err = normalizeCRMString("onsite_contact", draft.OnsiteContact, 160, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.Headcount < 0 || draft.Headcount > 1000000 {
		return JobDraft{}, NewCRMValidationError("headcount", "must be between 0 and 1000000")
	}
	if draft.Priority == "" {
		draft.Priority = JobPriorityNormal
	}
	if !IsValidJobPriority(draft.Priority) {
		return JobDraft{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if draft.StartAt, err = normalizeCRMOptionalTime("start_at", draft.StartAt); err != nil {
		return JobDraft{}, err
	}
	if draft.DeadlineAt, err = normalizeCRMOptionalTime("deadline_at", draft.DeadlineAt); err != nil {
		return JobDraft{}, err
	}
	if draft.StartAt != nil && draft.DeadlineAt != nil && draft.DeadlineAt.Before(*draft.StartAt) {
		return JobDraft{}, NewCRMValidationError("deadline_at", "must not precede start_at")
	}
	if draft.Shift, err = normalizeCRMString("shift", draft.Shift, 160, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.WorkSchedule, err = normalizeCRMString("work_schedule", draft.WorkSchedule, 2000, false, true); err != nil {
		return JobDraft{}, err
	}
	if draft.CompensationSummary, err = normalizeCRMString("compensation_summary", draft.CompensationSummary, 2000, false, true); err != nil {
		return JobDraft{}, err
	}
	if draft.Accommodation, err = normalizeCRMString("accommodation", draft.Accommodation, 2000, false, true); err != nil {
		return JobDraft{}, err
	}
	if draft.TransportRequirements, err = normalizeCRMString("transport_requirements", draft.TransportRequirements, 2000, false, true); err != nil {
		return JobDraft{}, err
	}
	if draft.RequiredDocuments, err = normalizeCRMStringList("required_documents", draft.RequiredDocuments, 64, 200); err != nil {
		return JobDraft{}, err
	}
	if draft.Owner, err = normalizeCRMString("owner", draft.Owner, 80, false, false); err != nil {
		return JobDraft{}, err
	}
	if draft.Status == "" {
		draft.Status = JobStatusOpen
	}
	if !IsValidJobStatus(draft.Status) {
		return JobDraft{}, NewCRMValidationError("status", "invalid job status")
	}
	return draft, nil
}

func NormalizeLeadCreate(create LeadCreate) (LeadCreate, error) {
	var err error
	if create.CandidateID, err = normalizeCRMString("candidate_id", create.CandidateID, 80, true, false); err != nil {
		return LeadCreate{}, err
	}
	if create.JobID, err = normalizeCRMString("job_id", create.JobID, 80, true, false); err != nil {
		return LeadCreate{}, err
	}
	if create.Owner, err = normalizeCRMString("owner", create.Owner, 80, false, false); err != nil {
		return LeadCreate{}, err
	}
	return create, nil
}

func NormalizeTaskCreate(create TaskCreate) (TaskCreate, error) {
	var err error
	if create.Title, err = normalizeCRMString("title", create.Title, 200, true, false); err != nil {
		return TaskCreate{}, err
	}
	if create.Description, err = normalizeCRMString("description", create.Description, 4000, false, true); err != nil {
		return TaskCreate{}, err
	}
	if create.Owner, err = normalizeCRMString("owner", create.Owner, 80, false, false); err != nil {
		return TaskCreate{}, err
	}
	if create.CandidateID, err = normalizeCRMString("candidate_id", create.CandidateID, 80, false, false); err != nil {
		return TaskCreate{}, err
	}
	if create.LeadID, err = normalizeCRMString("lead_id", create.LeadID, 80, false, false); err != nil {
		return TaskCreate{}, err
	}
	if create.CandidateID == "" && create.LeadID == "" {
		return TaskCreate{}, NewCRMValidationError("task", "candidate_id or lead_id is required")
	}
	if create.DueAt.IsZero() {
		return TaskCreate{}, NewCRMValidationError("due_at", "is required")
	}
	create.DueAt = create.DueAt.UTC()
	return create, nil
}

func NormalizeNoteCreate(create NoteCreate) (NoteCreate, error) {
	var err error
	if !IsValidNoteTargetType(create.TargetType) {
		return NoteCreate{}, NewCRMValidationError("target_type", "invalid note target type")
	}
	if create.TargetID, err = normalizeCRMString("target_id", create.TargetID, 80, true, false); err != nil {
		return NoteCreate{}, err
	}
	if create.Author, err = normalizeCRMString("author", create.Author, 80, false, false); err != nil {
		return NoteCreate{}, err
	}
	if create.Body, err = normalizeCRMString("body", create.Body, 4000, true, true); err != nil {
		return NoteCreate{}, err
	}
	return create, nil
}

func NormalizeCRMSearch(search string) (string, error) {
	return normalizeCRMString("q", search, CRMMaxSearchRunes, false, false)
}

func BuildMessageKey(chatType ChatType, peerID, mid, authorID, groupID string, timestamp int64, text string, kinds []string) string {
	if mid != "" {
		return "wechat:" + string(chatType) + ":" + peerID + ":" + mid
	}

	hasher := sha256.New()
	writeCanonicalString(hasher, string(chatType))
	writeCanonicalString(hasher, peerID)
	writeCanonicalString(hasher, authorID)
	writeCanonicalString(hasher, groupID)
	writeCanonicalString(hasher, strconv.FormatInt(timestamp, 10))
	writeCanonicalString(hasher, text)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(kinds)))
	_, _ = hasher.Write(count[:])
	for _, kind := range kinds {
		writeCanonicalString(hasher, kind)
	}
	return "wechat:sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func MessageKeyForEvent(event MessageEvent) string {
	return BuildMessageKey(
		event.ChatType,
		event.PeerID,
		event.Mid,
		event.AuthorID,
		event.GroupID,
		event.Timestamp,
		event.Text,
		event.Kinds,
	)
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeCanonicalString(writer byteWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func normalizeCRMString(field, value string, maxRunes int, required, multiline bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", NewCRMValidationError(field, "is required")
	}
	if !utf8.ValidString(value) {
		return "", NewCRMValidationError(field, "must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return "", NewCRMValidationError(field, fmt.Sprintf("must be at most %d characters", maxRunes))
	}
	for _, r := range value {
		if r >= 0 && r < 0x20 && (!multiline || (r != '\t' && r != '\n')) {
			return "", NewCRMValidationError(field, "contains a control character")
		}
	}
	return value, nil
}

func normalizeCRMStringList(field string, values []string, maxCount, maxRunes int) ([]string, error) {
	if len(values) > maxCount {
		return nil, NewCRMValidationError(field, fmt.Sprintf("must contain at most %d values", maxCount))
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized, err := normalizeCRMString(field, value, maxRunes, true, false)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			return nil, NewCRMValidationError(field, "contains duplicate values")
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func validateCRMVersion(field string, version uint64) error {
	if version == 0 {
		return NewCRMValidationError(field, "must be greater than zero")
	}
	return nil
}

func validateCRMTime(field string, value time.Time) error {
	if value.IsZero() {
		return NewCRMValidationError(field, "is required")
	}
	_, offset := value.Zone()
	if offset != 0 {
		return NewCRMValidationError(field, "must be UTC")
	}
	return nil
}

func normalizeCRMOptionalTime(field string, value *time.Time) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if value.IsZero() {
		return nil, NewCRMValidationError(field, "must not be zero")
	}
	normalized := value.UTC()
	return &normalized, nil
}
