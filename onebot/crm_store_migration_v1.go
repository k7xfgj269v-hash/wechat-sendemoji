package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type crmIDCountersV1 struct {
	Candidate uint64 `json:"candidate"`
	Job       uint64 `json:"job"`
	Lead      uint64 `json:"lead"`
	Task      uint64 `json:"task"`
	Note      uint64 `json:"note"`
	Event     uint64 `json:"event"`
	Rule      uint64 `json:"rule"`
}

type crmJobV1 struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Company        string    `json:"company"`
	Location       string    `json:"location,omitempty"`
	EmploymentType string    `json:"employment_type,omitempty"`
	Description    string    `json:"description,omitempty"`
	Requirements   []string  `json:"requirements"`
	Owner          string    `json:"owner,omitempty"`
	Status         JobStatus `json:"status"`
	Version        uint64    `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type crmStateV1 struct {
	SchemaVersion   int             `json:"schema_version"`
	Counters        crmIDCountersV1 `json:"counters"`
	Candidates      []Candidate     `json:"candidates"`
	Jobs            []crmJobV1      `json:"jobs"`
	Leads           []Lead          `json:"leads"`
	Tasks           []FollowUpTask  `json:"tasks"`
	Notes           []Note          `json:"notes"`
	MessageEvents   []MessageEvent  `json:"message_events"`
	ForwardingRules []ForwardRule   `json:"forwarding_rules"`
}

type crmIDCountersV2 struct {
	Candidate      uint64 `json:"candidate"`
	Job            uint64 `json:"job"`
	Lead           uint64 `json:"lead"`
	Task           uint64 `json:"task"`
	Note           uint64 `json:"note"`
	Event          uint64 `json:"event"`
	Rule           uint64 `json:"rule"`
	StageEvent     uint64 `json:"stage_event"`
	Onboarding     uint64 `json:"onboarding"`
	ServiceRequest uint64 `json:"service_request"`
	FundCallback   uint64 `json:"fund_callback"`
	Audit          uint64 `json:"audit"`
}

type crmJobV2 struct {
	ID                    string      `json:"id"`
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
	Priority              JobPriority `json:"priority"`
	StartAt               *time.Time  `json:"start_at,omitempty"`
	DeadlineAt            *time.Time  `json:"deadline_at,omitempty"`
	Shift                 string      `json:"shift,omitempty"`
	WorkSchedule          string      `json:"work_schedule,omitempty"`
	CompensationSummary   string      `json:"compensation_summary,omitempty"`
	Accommodation         string      `json:"accommodation,omitempty"`
	TransportRequirements string      `json:"transport_requirements,omitempty"`
	RequiredDocuments     []string    `json:"required_documents"`
	Owner                 string      `json:"owner,omitempty"`
	Status                JobStatus   `json:"status"`
	Version               uint64      `json:"version"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type crmServiceRequestV2 struct {
	ID               string                 `json:"id"`
	CandidateID      string                 `json:"candidate_id"`
	JobID            string                 `json:"job_id,omitempty"`
	OnboardingCaseID string                 `json:"onboarding_case_id,omitempty"`
	SourceEventID    string                 `json:"source_event_id,omitempty"`
	Category         ServiceRequestCategory `json:"category"`
	Priority         JobPriority            `json:"priority"`
	Description      string                 `json:"description"`
	Owner            string                 `json:"owner,omitempty"`
	Status           ServiceRequestStatus   `json:"status"`
	RequiresHuman    bool                   `json:"requires_human"`
	DueAt            *time.Time             `json:"due_at,omitempty"`
	Resolution       string                 `json:"resolution,omitempty"`
	Version          uint64                 `json:"version"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	ResolvedAt       *time.Time             `json:"resolved_at,omitempty"`
}

type crmStateV2 struct {
	SchemaVersion   int                   `json:"schema_version"`
	Counters        crmIDCountersV2       `json:"counters"`
	Candidates      []Candidate           `json:"candidates"`
	Jobs            []crmJobV2            `json:"jobs"`
	Leads           []Lead                `json:"leads"`
	LeadStageEvents []LeadStageEvent      `json:"lead_stage_events"`
	OnboardingCases []OnboardingCase      `json:"onboarding_cases"`
	ServiceRequests []crmServiceRequestV2 `json:"service_requests"`
	FundCallbacks   []FundCallback        `json:"fund_callbacks"`
	AuditEvents     []CRMAuditEvent       `json:"audit_events"`
	Tasks           []FollowUpTask        `json:"tasks"`
	Notes           []Note                `json:"notes"`
	MessageEvents   []MessageEvent        `json:"message_events"`
	ForwardingRules []ForwardRule         `json:"forwarding_rules"`
}

type crmIDCountersV3 struct {
	Candidate      uint64 `json:"candidate"`
	Job            uint64 `json:"job"`
	Lead           uint64 `json:"lead"`
	Task           uint64 `json:"task"`
	Note           uint64 `json:"note"`
	Event          uint64 `json:"event"`
	Rule           uint64 `json:"rule"`
	StageEvent     uint64 `json:"stage_event"`
	Onboarding     uint64 `json:"onboarding"`
	ServiceRequest uint64 `json:"service_request"`
	FundCallback   uint64 `json:"fund_callback"`
	DemandRevision uint64 `json:"demand_revision"`
	OperationBatch uint64 `json:"operation_batch"`
	ServiceQueue   uint64 `json:"service_queue"`
	FundLedger     uint64 `json:"fund_ledger"`
	Audit          uint64 `json:"audit"`
}

type crmMessageEventV3 struct {
	ID           string       `json:"id"`
	MessageKey   string       `json:"message_key"`
	Source       string       `json:"source"`
	Mid          string       `json:"mid,omitempty"`
	ChatType     ChatType     `json:"chat_type"`
	PeerID       string       `json:"peer_id"`
	AuthorID     string       `json:"author_id"`
	GroupID      string       `json:"group_id,omitempty"`
	Text         string       `json:"text"`
	Kinds        []string     `json:"kinds"`
	Timestamp    int64        `json:"timestamp"`
	Suspicious   bool         `json:"suspicious"`
	ReviewStatus ReviewStatus `json:"review_status"`
	CandidateID  string       `json:"candidate_id,omitempty"`
	Version      uint64       `json:"version"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type crmStateV3 struct {
	SchemaVersion      int                       `json:"schema_version"`
	Counters           crmIDCountersV3           `json:"counters"`
	Candidates         []Candidate               `json:"candidates"`
	Jobs               []Job                     `json:"jobs"`
	Leads              []Lead                    `json:"leads"`
	LeadStageEvents    []LeadStageEvent          `json:"lead_stage_events"`
	OnboardingCases    []OnboardingCase          `json:"onboarding_cases"`
	ServiceRequests    []ServiceRequest          `json:"service_requests"`
	FundCallbacks      []FundCallback            `json:"fund_callbacks"`
	DemandRevisions    []DemandRevision          `json:"demand_revisions"`
	OperationBatches   []OperationBatch          `json:"operation_batches"`
	ServiceQueues      []ServiceQueue            `json:"service_queues"`
	FundCallbackLedger []FundCallbackLedgerEntry `json:"fund_callback_ledger"`
	AuditEvents        []CRMAuditEvent           `json:"audit_events"`
	Tasks              []FollowUpTask            `json:"tasks"`
	Notes              []Note                    `json:"notes"`
	MessageEvents      []crmMessageEventV3       `json:"message_events"`
	ForwardingRules    []ForwardRule             `json:"forwarding_rules"`
}

func decodeCRMState(data []byte) (CRMState, error) {
	state, _, err := decodeCRMStateWithMigration(data)
	return state, err
}

func decodeCRMStateWithMigration(data []byte) (CRMState, bool, error) {
	var envelope map[string]json.RawMessage
	if err := decodeCRMJSON(data, &envelope); err != nil {
		return CRMState{}, false, err
	}
	rawSchema, ok := envelope["schema_version"]
	if !ok {
		return CRMState{}, false, NewCRMValidationError("schema_version", "is required")
	}
	var schemaVersion int
	if err := json.Unmarshal(rawSchema, &schemaVersion); err != nil {
		return CRMState{}, false, NewCRMValidationError("schema_version", "must be an integer")
	}

	switch schemaVersion {
	case 1:
		var legacy crmStateV1
		if err := decodeCRMJSON(data, &legacy); err != nil {
			return CRMState{}, false, err
		}
		legacyV2, err := migrateCRMStateV1ToV2(legacy)
		if err != nil {
			return CRMState{}, false, err
		}
		state, err := migrateCRMStateV2(legacyV2)
		if err != nil {
			return CRMState{}, false, err
		}
		return state, true, nil
	case 2:
		var legacy crmStateV2
		if err := decodeCRMJSON(data, &legacy); err != nil {
			return CRMState{}, false, err
		}
		state, err := migrateCRMStateV2(legacy)
		if err != nil {
			return CRMState{}, false, err
		}
		return state, true, nil
	case 3:
		var legacy crmStateV3
		if err := decodeCRMJSON(data, &legacy); err != nil {
			return CRMState{}, false, err
		}
		state, err := migrateCRMStateV3(legacy)
		if err != nil {
			return CRMState{}, false, err
		}
		return state, true, nil
	case CRMSchemaVersion:
		var state CRMState
		if err := decodeCRMJSON(data, &state); err != nil {
			return CRMState{}, false, err
		}
		if err := validateCRMDecodedV4Shape(&state); err != nil {
			return CRMState{}, false, err
		}
		if err := validateCRMStateStrict(&state); err != nil {
			return CRMState{}, false, err
		}
		return state, false, nil
	default:
		return CRMState{}, false, NewCRMValidationError(
			"schema_version",
			fmt.Sprintf("unsupported schema version %d", schemaVersion),
		)
	}
}

func crmStateFromV1(legacy crmStateV1) CRMState {
	state := CRMState{
		SchemaVersion: legacy.SchemaVersion,
		Counters: IDCounters{
			Candidate: legacy.Counters.Candidate,
			Job:       legacy.Counters.Job,
			Lead:      legacy.Counters.Lead,
			Task:      legacy.Counters.Task,
			Note:      legacy.Counters.Note,
			Event:     legacy.Counters.Event,
			Rule:      legacy.Counters.Rule,
		},
		Candidates:      legacy.Candidates,
		Leads:           legacy.Leads,
		Tasks:           legacy.Tasks,
		Notes:           legacy.Notes,
		MessageEvents:   legacy.MessageEvents,
		ForwardingRules: legacy.ForwardingRules,
	}
	if legacy.Jobs != nil {
		state.Jobs = make([]Job, len(legacy.Jobs))
		for i := range legacy.Jobs {
			job := legacy.Jobs[i]
			state.Jobs[i] = Job{
				ID:             job.ID,
				Title:          job.Title,
				Company:        job.Company,
				Location:       job.Location,
				EmploymentType: job.EmploymentType,
				Description:    job.Description,
				Requirements:   job.Requirements,
				Owner:          job.Owner,
				Status:         job.Status,
				Version:        job.Version,
				CreatedAt:      job.CreatedAt,
				UpdatedAt:      job.UpdatedAt,
			}
		}
	}
	return state
}

func migrateCRMStateV1(state *CRMState) error {
	if state == nil {
		return NewCRMValidationError("state", "is required")
	}
	if state.SchemaVersion == CRMSchemaVersion {
		return validateCRMStateStrict(state)
	}
	if state.SchemaVersion != 1 {
		return NewCRMValidationError(
			"schema_version",
			fmt.Sprintf("unsupported schema version %d", state.SchemaVersion),
		)
	}
	if err := validateCRMStateV1Slices(state); err != nil {
		return err
	}
	if state.Counters.StageEvent != 0 ||
		state.Counters.Onboarding != 0 ||
		state.Counters.ServiceRequest != 0 ||
		state.Counters.FundCallback != 0 ||
		state.Counters.DemandRevision != 0 ||
		state.Counters.OperationBatch != 0 ||
		state.Counters.ServiceQueue != 0 ||
		state.Counters.FundLedger != 0 ||
		state.Counters.Audit != 0 ||
		state.Counters.SourceGroup != 0 ||
		state.Counters.JobProvenance != 0 ||
		state.RecruitingProject.OrganizationName != "" ||
		state.RecruitingProject.Version != 0 ||
		state.RecruitingProject.RequireAuthoritativeJob ||
		state.RecruitingProject.SourceGroups != nil ||
		len(state.LeadStageEvents) != 0 ||
		len(state.OnboardingCases) != 0 ||
		len(state.ServiceRequests) != 0 ||
		len(state.FundCallbacks) != 0 ||
		len(state.DemandRevisions) != 0 ||
		len(state.OperationBatches) != 0 ||
		len(state.ServiceQueues) != 0 ||
		len(state.FundCallbackLedger) != 0 ||
		len(state.AuditEvents) != 0 ||
		len(state.JobProvenance) != 0 {
		return NewCRMValidationError("snapshot", "schema 1 contains partially migrated v2 state")
	}
	for i := range state.Jobs {
		if crmJobHasV2Data(state.Jobs[i]) {
			return NewCRMValidationError(indexedField("jobs", i), "schema 1 contains v2 job fields")
		}
		state.Jobs[i].Priority = JobPriorityNormal
		state.Jobs[i].RequiredDocuments = []string{}
	}
	state.LeadStageEvents = []LeadStageEvent{}
	state.OnboardingCases = []OnboardingCase{}
	state.ServiceRequests = []ServiceRequest{}
	state.FundCallbacks = []FundCallback{}
	state.AuditEvents = []CRMAuditEvent{}
	state.SchemaVersion = 2
	legacyV2 := crmStateV2FromCRMStateWithoutV3(*state)
	migrated, err := migrateCRMStateV2(legacyV2)
	if err != nil {
		return err
	}
	*state = migrated
	return nil
}

func migrateCRMStateV1ToV2(legacy crmStateV1) (crmStateV2, error) {
	state := crmStateFromV1(legacy)
	if err := validateCRMStateV1Slices(&state); err != nil {
		return crmStateV2{}, err
	}
	for i := range state.Jobs {
		if crmJobHasV2Data(state.Jobs[i]) {
			return crmStateV2{}, NewCRMValidationError(indexedField("jobs", i), "schema 1 contains v2 job fields")
		}
		state.Jobs[i].Priority = JobPriorityNormal
		state.Jobs[i].RequiredDocuments = []string{}
	}
	state.LeadStageEvents = []LeadStageEvent{}
	state.OnboardingCases = []OnboardingCase{}
	state.ServiceRequests = []ServiceRequest{}
	state.FundCallbacks = []FundCallback{}
	state.AuditEvents = []CRMAuditEvent{}
	state.SchemaVersion = 2
	legacyV2 := crmStateV2FromCRMStateWithoutV3(state)
	if err := validateCRMDecodedV2Shape(&legacyV2); err != nil {
		return crmStateV2{}, err
	}
	compatibilityState := crmStateFromV2(legacyV2)
	if err := ValidateCRMState(&compatibilityState); err != nil {
		return crmStateV2{}, err
	}
	return legacyV2, nil
}

func migrateCRMStateV2(legacy crmStateV2) (CRMState, error) {
	if legacy.SchemaVersion != 2 {
		return CRMState{}, NewCRMValidationError("schema_version", "schema 2 snapshot is required")
	}
	if err := validateCRMDecodedV2Shape(&legacy); err != nil {
		return CRMState{}, err
	}
	state := crmStateFromV2(legacy)
	normalizeCRMStateV2Defaults(&state)
	if err := ValidateCRMState(&state); err != nil {
		return CRMState{}, err
	}
	if len(state.Jobs) > crmMaxIDCounter {
		return CRMState{}, NewCRMConflictError("id counter exhausted for demand_revision")
	}
	indexes := make([]int, len(state.Jobs))
	for i := range state.Jobs {
		indexes[i] = i
	}
	sort.Slice(indexes, func(i, j int) bool {
		return state.Jobs[indexes[i]].ID < state.Jobs[indexes[j]].ID
	})
	for _, index := range indexes {
		state.Counters.DemandRevision++
		job := &state.Jobs[index]
		revision := DemandRevision{
			ID:        fmt.Sprintf("demand_%08d", state.Counters.DemandRevision),
			JobID:     job.ID,
			Number:    1,
			Snapshot:  jobDemandSnapshot(*job),
			CreatedBy: "system",
			Reason:    "schema_v2_migration",
			CreatedAt: job.UpdatedAt,
		}
		state.DemandRevisions = append(state.DemandRevisions, revision)
		job.ActiveDemandRevisionID = revision.ID
		job.DemandRevisionNumber = revision.Number
	}
	if err := validateCRMStateStrict(&state); err != nil {
		return CRMState{}, err
	}
	return state, nil
}
