package main

import (
	"context"
	"strings"
	"time"
)

type CRMUpdateMeta struct {
	Actor         string `json:"actor"`
	Operation     string `json:"operation"`
	EntityType    string `json:"entity_type,omitempty"`
	EntityID      string `json:"entity_id,omitempty"`
	SourceEventID string `json:"source_event_id,omitempty"`
}

type CandidateDraft struct {
	FullName      string          `json:"full_name"`
	WeChatID      string          `json:"wechat_id,omitempty"`
	Phone         string          `json:"phone,omitempty"`
	Email         string          `json:"email,omitempty"`
	Location      string          `json:"location,omitempty"`
	Source        string          `json:"source,omitempty"`
	SourceEventID string          `json:"-"`
	Skills        []string        `json:"skills"`
	Owner         string          `json:"owner,omitempty"`
	Status        CandidateStatus `json:"status,omitempty"`
	Summary       string          `json:"summary,omitempty"`
}

type CandidateUpdate struct {
	ExpectedVersion uint64           `json:"expected_version"`
	FullName        *string          `json:"full_name,omitempty"`
	WeChatID        *string          `json:"wechat_id,omitempty"`
	Phone           *string          `json:"phone,omitempty"`
	Email           *string          `json:"email,omitempty"`
	Location        *string          `json:"location,omitempty"`
	Source          *string          `json:"source,omitempty"`
	Skills          *[]string        `json:"skills,omitempty"`
	Owner           *string          `json:"owner,omitempty"`
	Status          *CandidateStatus `json:"status,omitempty"`
	Summary         *string          `json:"summary,omitempty"`
}

type JobDraft struct {
	Title                 string      `json:"title"`
	Company               string      `json:"company"`
	Location              string      `json:"location,omitempty"`
	EmploymentType        string      `json:"employment_type,omitempty"`
	Description           string      `json:"description,omitempty"`
	Requirements          []string    `json:"requirements"`
	DemandCode            string      `json:"demand_code,omitempty"`
	SiteName              string      `json:"site_name,omitempty"`
	OnsiteContact         string      `json:"onsite_contact,omitempty"`
	Headcount             int         `json:"headcount"`
	Priority              JobPriority `json:"priority,omitempty"`
	StartAt               *time.Time  `json:"start_at,omitempty"`
	DeadlineAt            *time.Time  `json:"deadline_at,omitempty"`
	Shift                 string      `json:"shift,omitempty"`
	WorkSchedule          string      `json:"work_schedule,omitempty"`
	CompensationSummary   string      `json:"compensation_summary,omitempty"`
	Accommodation         string      `json:"accommodation,omitempty"`
	TransportRequirements string      `json:"transport_requirements,omitempty"`
	RequiredDocuments     []string    `json:"required_documents"`
	Owner                 string      `json:"owner,omitempty"`
	Status                JobStatus   `json:"status,omitempty"`
}

type JobUpdate struct {
	ExpectedVersion       uint64       `json:"expected_version"`
	Title                 *string      `json:"title,omitempty"`
	Company               *string      `json:"company,omitempty"`
	Location              *string      `json:"location,omitempty"`
	EmploymentType        *string      `json:"employment_type,omitempty"`
	Description           *string      `json:"description,omitempty"`
	Requirements          *[]string    `json:"requirements,omitempty"`
	DemandCode            *string      `json:"demand_code,omitempty"`
	SiteName              *string      `json:"site_name,omitempty"`
	OnsiteContact         *string      `json:"onsite_contact,omitempty"`
	Headcount             *int         `json:"headcount,omitempty"`
	Priority              *JobPriority `json:"priority,omitempty"`
	StartAt               *time.Time   `json:"start_at,omitempty"`
	DeadlineAt            *time.Time   `json:"deadline_at,omitempty"`
	ClearStartAt          bool         `json:"clear_start_at,omitempty"`
	ClearDeadlineAt       bool         `json:"clear_deadline_at,omitempty"`
	Shift                 *string      `json:"shift,omitempty"`
	WorkSchedule          *string      `json:"work_schedule,omitempty"`
	CompensationSummary   *string      `json:"compensation_summary,omitempty"`
	Accommodation         *string      `json:"accommodation,omitempty"`
	TransportRequirements *string      `json:"transport_requirements,omitempty"`
	RequiredDocuments     *[]string    `json:"required_documents,omitempty"`
	Owner                 *string      `json:"owner,omitempty"`
	Status                *JobStatus   `json:"status,omitempty"`
}

type LeadCreate struct {
	CandidateID string `json:"candidate_id"`
	JobID       string `json:"job_id"`
	Owner       string `json:"owner,omitempty"`
}

type LeadUpdate struct {
	ExpectedVersion uint64  `json:"expected_version"`
	Owner           *string `json:"owner,omitempty"`
}

type LeadTransition struct {
	ExpectedVersion uint64    `json:"expected_version"`
	FromStage       LeadStage `json:"from_stage"`
	ToStage         LeadStage `json:"to_stage"`
	Reason          string    `json:"reason,omitempty"`
	Actor           string    `json:"actor,omitempty"`
}

type TaskCreate struct {
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	DueAt       time.Time `json:"due_at"`
	CandidateID string    `json:"candidate_id,omitempty"`
	LeadID      string    `json:"lead_id,omitempty"`
}

type TaskUpdate struct {
	ExpectedVersion uint64     `json:"expected_version"`
	Title           *string    `json:"title,omitempty"`
	Description     *string    `json:"description,omitempty"`
	Owner           *string    `json:"owner,omitempty"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	CandidateID     *string    `json:"candidate_id,omitempty"`
	LeadID          *string    `json:"lead_id,omitempty"`
}

type TaskStatusRequest struct {
	ExpectedVersion uint64         `json:"expected_version"`
	Status          FollowUpStatus `json:"status"`
}

type NoteCreate struct {
	TargetType NoteTargetType `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Author     string         `json:"author,omitempty"`
	Body       string         `json:"body"`
}

type ReviewUpdate struct {
	ExpectedVersion uint64       `json:"expected_version"`
	ReviewStatus    ReviewStatus `json:"review_status"`
}

type MessageCandidateLink struct {
	MessageEventID       string          `json:"message_event_id"`
	ExpectedEventVersion uint64          `json:"expected_event_version"`
	CandidateID          string          `json:"candidate_id,omitempty"`
	Candidate            *CandidateDraft `json:"candidate,omitempty"`
}

type CandidateListItem struct {
	Candidate       Candidate `json:"candidate"`
	ActiveLeadCount int       `json:"active_lead_count"`
	OpenTaskCount   int       `json:"open_task_count"`
}

type CandidateDetail struct {
	Candidate       Candidate `json:"candidate"`
	MessageEventIDs []string  `json:"message_event_ids"`
	LeadIDs         []string  `json:"lead_ids"`
	OpenTaskIDs     []string  `json:"open_task_ids"`
	NoteCount       int       `json:"note_count"`
}

type JobListItem struct {
	Job                     Job                  `json:"job"`
	LeadCount               int                  `json:"lead_count"`
	LatestIngestionDecision JobIngestionDecision `json:"latest_ingestion_decision,omitempty"`
	ProvenanceCount         int                  `json:"provenance_count"`
}

type JobDetail struct {
	Job        Job              `json:"job"`
	LeadIDs    []string         `json:"lead_ids"`
	LeadCount  int              `json:"lead_count"`
	Provenance []JobProvenance  `json:"provenance"`
	Revisions  []DemandRevision `json:"revisions"`
}

type LeadListItem struct {
	Lead          Lead             `json:"lead"`
	CandidateName string           `json:"candidate_name"`
	JobTitle      string           `json:"job_title"`
	StageEvents   []LeadStageEvent `json:"stage_events"`
}

type TaskListItem struct {
	Task          FollowUpTask `json:"task"`
	CandidateName string       `json:"candidate_name"`
	LeadStage     LeadStage    `json:"lead_stage,omitempty"`
	JobID         string       `json:"job_id,omitempty"`
	JobTitle      string       `json:"job_title,omitempty"`
}

type PipelineStage struct {
	Stage LeadStage      `json:"stage"`
	Leads []LeadListItem `json:"leads"`
}

type PipelineResponse struct {
	Stages []PipelineStage `json:"stages"`
	Total  int             `json:"total"`
}

type CandidateExtractor interface {
	Draft(context.Context, MessageEvent, CandidateDraft) (CandidateDraft, error)
}

type ManualCandidateExtractor struct{}

func (ManualCandidateExtractor) Draft(_ context.Context, event MessageEvent, draft CandidateDraft) (CandidateDraft, error) {
	if strings.TrimSpace(draft.WeChatID) == "" {
		draft.WeChatID = event.AuthorID
	}
	if strings.TrimSpace(draft.Source) == "" {
		draft.Source = "wechat_message"
	}
	draft.SourceEventID = event.ID
	return NormalizeCandidateDraft(draft)
}
