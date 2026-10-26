package main

import (
	"context"
	"encoding/json"
	"time"
)

const (
	workbenchPipelineDetailTextJSONBytes = 512
	workbenchPipelineListTextJSONBytes   = 12
	workbenchPipelineListExcerptItems    = 8
	workbenchPipelineStageEventItems     = 4
	workbenchPipelineChangeJSONBytes     = 512
)

type workbenchPipelineTextProjection struct {
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated"`
}

type workbenchPipelineStringListProjection struct {
	Total   int                               `json:"total"`
	Items   []workbenchPipelineTextProjection `json:"items"`
	HasMore bool                              `json:"has_more"`
}

type workbenchPipelineCandidateUpdateProjection struct {
	ID            string                                `json:"id"`
	FullName      workbenchPipelineTextProjection       `json:"full_name"`
	WeChatID      workbenchPipelineTextProjection       `json:"wechat_id"`
	Phone         workbenchPipelineTextProjection       `json:"phone"`
	Email         workbenchPipelineTextProjection       `json:"email"`
	Location      workbenchPipelineTextProjection       `json:"location"`
	Source        workbenchPipelineTextProjection       `json:"source"`
	SourceEventID string                                `json:"source_event_id"`
	Skills        workbenchPipelineStringListProjection `json:"skills"`
	Owner         workbenchPipelineTextProjection       `json:"owner"`
	Status        CandidateStatus                       `json:"status"`
	Summary       workbenchPipelineTextProjection       `json:"summary"`
	Version       uint64                                `json:"version"`
	CreatedAt     time.Time                             `json:"created_at"`
	UpdatedAt     time.Time                             `json:"updated_at"`
}

type workbenchPipelineDemandSnapshotProjection struct {
	Title                 workbenchPipelineTextProjection       `json:"title"`
	Company               workbenchPipelineTextProjection       `json:"company"`
	Location              workbenchPipelineTextProjection       `json:"location"`
	EmploymentType        workbenchPipelineTextProjection       `json:"employment_type"`
	Description           workbenchPipelineTextProjection       `json:"description"`
	Requirements          workbenchPipelineStringListProjection `json:"requirements"`
	DemandCode            workbenchPipelineTextProjection       `json:"demand_code"`
	SiteName              workbenchPipelineTextProjection       `json:"site_name"`
	OnsiteContact         workbenchPipelineTextProjection       `json:"onsite_contact"`
	Headcount             int                                   `json:"headcount"`
	Priority              JobPriority                           `json:"priority"`
	StartAt               *time.Time                            `json:"start_at,omitempty"`
	DeadlineAt            *time.Time                            `json:"deadline_at,omitempty"`
	Shift                 workbenchPipelineTextProjection       `json:"shift"`
	WorkSchedule          workbenchPipelineTextProjection       `json:"work_schedule"`
	CompensationSummary   workbenchPipelineTextProjection       `json:"compensation_summary"`
	Accommodation         workbenchPipelineTextProjection       `json:"accommodation"`
	TransportRequirements workbenchPipelineTextProjection       `json:"transport_requirements"`
	RequiredDocuments     workbenchPipelineStringListProjection `json:"required_documents"`
}

type workbenchPipelineJobUpdateProjection struct {
	ID                     string                                    `json:"id"`
	Version                uint64                                    `json:"version"`
	Status                 JobStatus                                 `json:"status"`
	ActiveDemandRevisionID string                                    `json:"active_demand_revision_id"`
	DemandRevisionNumber   uint64                                    `json:"demand_revision_number"`
	Owner                  workbenchPipelineTextProjection           `json:"owner"`
	Snapshot               workbenchPipelineDemandSnapshotProjection `json:"snapshot"`
	CreatedAt              time.Time                                 `json:"created_at"`
	UpdatedAt              time.Time                                 `json:"updated_at"`
}

type workbenchPipelineRevisionIdentityProjection struct {
	ID        string                          `json:"id"`
	JobID     string                          `json:"job_id"`
	Number    uint64                          `json:"number"`
	CreatedBy workbenchPipelineTextProjection `json:"created_by"`
	Reason    workbenchPipelineTextProjection `json:"reason"`
	CreatedAt time.Time                       `json:"created_at"`
}

type workbenchPipelineRevisionListItemProjection struct {
	ID                     string    `json:"id"`
	JobID                  string    `json:"job_id"`
	Number                 uint64    `json:"number"`
	CreatedByExcerpt       string    `json:"created_by_excerpt"`
	CreatedByTruncated     bool      `json:"created_by_truncated"`
	ReasonExcerpt          string    `json:"reason_excerpt"`
	ReasonTruncated        bool      `json:"reason_truncated"`
	CreatedAt              time.Time `json:"created_at"`
	TitleExcerpt           string    `json:"title_excerpt"`
	TitleTruncated         bool      `json:"title_truncated"`
	RequirementsCount      int       `json:"requirements_count"`
	RequiredDocumentsCount int       `json:"required_documents_count"`
}

type workbenchPipelineRevisionListProjection struct {
	Revisions []workbenchPipelineRevisionListItemProjection `json:"revisions"`
}

type workbenchPipelineRevisionDetailProjection struct {
	ID        string                                    `json:"id"`
	JobID     string                                    `json:"job_id"`
	Number    uint64                                    `json:"number"`
	Snapshot  workbenchPipelineDemandSnapshotProjection `json:"snapshot"`
	CreatedBy workbenchPipelineTextProjection           `json:"created_by"`
	Reason    workbenchPipelineTextProjection           `json:"reason"`
	CreatedAt time.Time                                 `json:"created_at"`
}

type workbenchPipelineRevisionChangeValueProjection struct {
	Type      string `json:"type"`
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated"`
	ItemCount int    `json:"item_count"`
}

type workbenchPipelineRevisionChangeProjection struct {
	Field  string                                         `json:"field"`
	Before workbenchPipelineRevisionChangeValueProjection `json:"before"`
	After  workbenchPipelineRevisionChangeValueProjection `json:"after"`
}

type workbenchPipelineRevisionComparisonProjection struct {
	JobID         string                                      `json:"job_id"`
	From          workbenchPipelineRevisionIdentityProjection `json:"from"`
	To            workbenchPipelineRevisionIdentityProjection `json:"to"`
	ChangedFields []string                                    `json:"changed_fields"`
	Changes       []workbenchPipelineRevisionChangeProjection `json:"changes"`
}

type workbenchPipelineLeadProjection struct {
	ID             string                          `json:"id"`
	CandidateID    string                          `json:"candidate_id"`
	JobID          string                          `json:"job_id"`
	Stage          LeadStage                       `json:"stage"`
	Owner          workbenchPipelineTextProjection `json:"owner"`
	Version        uint64                          `json:"version"`
	CreatedAt      time.Time                       `json:"created_at"`
	UpdatedAt      time.Time                       `json:"updated_at"`
	StageChangedAt time.Time                       `json:"stage_changed_at"`
}

type workbenchPipelineStageEventProjection struct {
	ID        string                          `json:"id"`
	LeadID    string                          `json:"lead_id"`
	FromStage LeadStage                       `json:"from_stage"`
	ToStage   LeadStage                       `json:"to_stage"`
	Reason    workbenchPipelineTextProjection `json:"reason"`
	Actor     workbenchPipelineTextProjection `json:"actor"`
	CreatedAt time.Time                       `json:"created_at"`
}

type workbenchPipelineStageEventListProjection struct {
	Total   int                                     `json:"total"`
	Items   []workbenchPipelineStageEventProjection `json:"items"`
	HasMore bool                                    `json:"has_more"`
}

type workbenchPipelineLeadListItemProjection struct {
	Lead          workbenchPipelineLeadProjection           `json:"lead"`
	CandidateName workbenchPipelineTextProjection           `json:"candidate_name"`
	JobTitle      workbenchPipelineTextProjection           `json:"job_title"`
	StageEvents   workbenchPipelineStageEventListProjection `json:"stage_events"`
}

type workbenchPipelineStageProjection struct {
	Stage LeadStage                                 `json:"stage"`
	Leads []workbenchPipelineLeadListItemProjection `json:"leads"`
}

type workbenchPipelineResponseProjection struct {
	Stages []workbenchPipelineStageProjection `json:"stages"`
	Total  int                                `json:"total"`
}

func init() {
	bindWorkbenchOperationHandlers("m2-pipeline-depth",
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesUpdate, Handler: workbenchPipelineCandidatesUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsUpdate, Handler: workbenchPipelineJobsUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsUpdate, Handler: workbenchPipelineLeadsUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationPipelineGet, Handler: workbenchPipelineGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationRevisionsList, Handler: workbenchPipelineRevisionsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationRevisionsGet, Handler: workbenchPipelineRevisionsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationRevisionsCompare, Handler: workbenchPipelineRevisionsCompare},
	)
}

func workbenchPipelineCandidatesUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationCandidatesUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchCandidateUpdateArguments)
		candidate, err := crmCandidateUpdate(dispatcher.crm, arguments.CandidateID, arguments.Update)
		if err != nil {
			return nil, err
		}
		return workbenchPipelineProjectCandidateUpdate(candidate), nil
	})
}

func workbenchPipelineJobsUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationJobsUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchJobUpdateArguments)
		job, err := crmPipelineUpdateJob(dispatcher.crm, arguments.JobID, arguments.Update)
		if err != nil {
			return nil, err
		}
		return workbenchPipelineProjectJobUpdate(job), nil
	})
}

func workbenchPipelineLeadsUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationLeadsUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchLeadUpdateArguments)
		return crmPipelineUpdateLead(dispatcher.crm, arguments.LeadID, arguments.Update)
	})
}

func workbenchPipelineGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationPipelineGet
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	board, err := crmPipelineBoard(dispatcher.crm, "", "")
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return workbenchPipelineProjectPage(board, arguments.Offset, arguments.Limit)
}

func workbenchPipelineRevisionsList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRevisionsList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchRevisionListArguments)
	revisions, err := crmPipelineListDemandRevisions(dispatcher.crm, arguments.JobID)
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	total := len(revisions)
	revisions = crmPipelinePage(revisions, arguments.Offset, arguments.Limit)
	if len(revisions) == 0 {
		return NewWorkbenchToolSuccess(
			operation,
			workbenchPipelineRevisionListProjection{
				Revisions: []workbenchPipelineRevisionListItemProjection{},
			},
			newWorkbenchPage(arguments.Limit, arguments.Offset, total, 0),
		)
	}
	projected := make([]workbenchPipelineRevisionListItemProjection, 0, len(revisions))
	var result WorkbenchToolResult
	for i := range revisions {
		candidate := append(projected, workbenchPipelineProjectRevisionListItem(revisions[i]))
		candidateResult := NewWorkbenchToolSuccess(
			operation,
			workbenchPipelineRevisionListProjection{Revisions: candidate},
			newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(candidate)),
		)
		if !candidateResult.OK {
			break
		}
		projected = candidate
		result = candidateResult
	}
	if len(projected) == 0 {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInternal,
			"revision summary could not be encoded",
			false,
		)
	}
	return result
}

func workbenchPipelineRevisionsGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationRevisionsGet, func() (any, error) {
		arguments := raw.(*WorkbenchRevisionGetArguments)
		revision, err := crmPipelineGetDemandRevision(
			dispatcher.crm,
			arguments.JobID,
			arguments.RevisionID,
		)
		if err != nil {
			return nil, err
		}
		return workbenchPipelineProjectRevisionDetail(revision), nil
	})
}

func workbenchPipelineRevisionsCompare(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(dispatcher, WorkbenchOperationRevisionsCompare, func() (any, error) {
		arguments := raw.(*WorkbenchRevisionCompareArguments)
		comparison, err := crmPipelineCompareDemandRevisions(
			dispatcher.crm,
			arguments.JobID,
			arguments.FromRevisionID,
			arguments.ToRevisionID,
		)
		if err != nil {
			return nil, err
		}
		return workbenchPipelineProjectRevisionComparison(comparison), nil
	})
}

func workbenchPipelineProjectJobUpdate(job Job) workbenchPipelineJobUpdateProjection {
	return workbenchPipelineJobUpdateProjection{
		ID:                     job.ID,
		Version:                job.Version,
		Status:                 job.Status,
		ActiveDemandRevisionID: job.ActiveDemandRevisionID,
		DemandRevisionNumber:   job.DemandRevisionNumber,
		Owner:                  workbenchPipelineProjectText(job.Owner, workbenchPipelineDetailTextJSONBytes),
		Snapshot:               workbenchPipelineProjectDemandSnapshot(jobDemandSnapshot(job)),
		CreatedAt:              job.CreatedAt,
		UpdatedAt:              job.UpdatedAt,
	}
}

func workbenchPipelineProjectCandidateUpdate(
	candidate Candidate,
) workbenchPipelineCandidateUpdateProjection {
	return workbenchPipelineCandidateUpdateProjection{
		ID:            candidate.ID,
		FullName:      workbenchPipelineProjectText(candidate.FullName, workbenchPipelineDetailTextJSONBytes),
		WeChatID:      workbenchPipelineProjectText(candidate.WeChatID, workbenchPipelineDetailTextJSONBytes),
		Phone:         workbenchPipelineProjectText(candidate.Phone, workbenchPipelineDetailTextJSONBytes),
		Email:         workbenchPipelineProjectText(candidate.Email, workbenchPipelineDetailTextJSONBytes),
		Location:      workbenchPipelineProjectText(candidate.Location, workbenchPipelineDetailTextJSONBytes),
		Source:        workbenchPipelineProjectText(candidate.Source, workbenchPipelineDetailTextJSONBytes),
		SourceEventID: candidate.SourceEventID,
		Skills:        workbenchPipelineProjectStringList(candidate.Skills),
		Owner:         workbenchPipelineProjectText(candidate.Owner, workbenchPipelineDetailTextJSONBytes),
		Status:        candidate.Status,
		Summary:       workbenchPipelineProjectText(candidate.Summary, workbenchPipelineDetailTextJSONBytes),
		Version:       candidate.Version,
		CreatedAt:     candidate.CreatedAt,
		UpdatedAt:     candidate.UpdatedAt,
	}
}

func workbenchPipelineProjectDemandSnapshot(
	snapshot JobDemandSnapshot,
) workbenchPipelineDemandSnapshotProjection {
	return workbenchPipelineDemandSnapshotProjection{
		Title:                 workbenchPipelineProjectText(snapshot.Title, workbenchPipelineDetailTextJSONBytes),
		Company:               workbenchPipelineProjectText(snapshot.Company, workbenchPipelineDetailTextJSONBytes),
		Location:              workbenchPipelineProjectText(snapshot.Location, workbenchPipelineDetailTextJSONBytes),
		EmploymentType:        workbenchPipelineProjectText(snapshot.EmploymentType, workbenchPipelineDetailTextJSONBytes),
		Description:           workbenchPipelineProjectText(snapshot.Description, workbenchPipelineDetailTextJSONBytes),
		Requirements:          workbenchPipelineProjectStringList(snapshot.Requirements),
		DemandCode:            workbenchPipelineProjectText(snapshot.DemandCode, workbenchPipelineDetailTextJSONBytes),
		SiteName:              workbenchPipelineProjectText(snapshot.SiteName, workbenchPipelineDetailTextJSONBytes),
		OnsiteContact:         workbenchPipelineProjectText(snapshot.OnsiteContact, workbenchPipelineDetailTextJSONBytes),
		Headcount:             snapshot.Headcount,
		Priority:              snapshot.Priority,
		StartAt:               cloneCRMTimePointer(snapshot.StartAt),
		DeadlineAt:            cloneCRMTimePointer(snapshot.DeadlineAt),
		Shift:                 workbenchPipelineProjectText(snapshot.Shift, workbenchPipelineDetailTextJSONBytes),
		WorkSchedule:          workbenchPipelineProjectText(snapshot.WorkSchedule, workbenchPipelineDetailTextJSONBytes),
		CompensationSummary:   workbenchPipelineProjectText(snapshot.CompensationSummary, workbenchPipelineDetailTextJSONBytes),
		Accommodation:         workbenchPipelineProjectText(snapshot.Accommodation, workbenchPipelineDetailTextJSONBytes),
		TransportRequirements: workbenchPipelineProjectText(snapshot.TransportRequirements, workbenchPipelineDetailTextJSONBytes),
		RequiredDocuments:     workbenchPipelineProjectStringList(snapshot.RequiredDocuments),
	}
}

func workbenchPipelineProjectStringList(values []string) workbenchPipelineStringListProjection {
	returned := len(values)
	if returned > workbenchPipelineListExcerptItems {
		returned = workbenchPipelineListExcerptItems
	}
	items := make([]workbenchPipelineTextProjection, returned)
	for i := 0; i < returned; i++ {
		items[i] = workbenchPipelineProjectText(values[i], workbenchPipelineDetailTextJSONBytes)
	}
	return workbenchPipelineStringListProjection{
		Total:   len(values),
		Items:   items,
		HasMore: returned < len(values),
	}
}

func workbenchPipelineProjectLead(
	item LeadListItem,
) workbenchPipelineLeadListItemProjection {
	returned := len(item.StageEvents)
	if returned > workbenchPipelineStageEventItems {
		returned = workbenchPipelineStageEventItems
	}
	events := make([]workbenchPipelineStageEventProjection, returned)
	for i := 0; i < returned; i++ {
		event := item.StageEvents[i]
		events[i] = workbenchPipelineStageEventProjection{
			ID:        event.ID,
			LeadID:    event.LeadID,
			FromStage: event.FromStage,
			ToStage:   event.ToStage,
			Reason:    workbenchPipelineProjectText(event.Reason, workbenchPipelineDetailTextJSONBytes),
			Actor:     workbenchPipelineProjectText(event.Actor, workbenchPipelineDetailTextJSONBytes),
			CreatedAt: event.CreatedAt,
		}
	}
	return workbenchPipelineLeadListItemProjection{
		Lead: workbenchPipelineLeadProjection{
			ID:             item.Lead.ID,
			CandidateID:    item.Lead.CandidateID,
			JobID:          item.Lead.JobID,
			Stage:          item.Lead.Stage,
			Owner:          workbenchPipelineProjectText(item.Lead.Owner, workbenchPipelineDetailTextJSONBytes),
			Version:        item.Lead.Version,
			CreatedAt:      item.Lead.CreatedAt,
			UpdatedAt:      item.Lead.UpdatedAt,
			StageChangedAt: item.Lead.StageChangedAt,
		},
		CandidateName: workbenchPipelineProjectText(item.CandidateName, workbenchPipelineDetailTextJSONBytes),
		JobTitle:      workbenchPipelineProjectText(item.JobTitle, workbenchPipelineDetailTextJSONBytes),
		StageEvents: workbenchPipelineStageEventListProjection{
			Total:   len(item.StageEvents),
			Items:   events,
			HasMore: returned < len(item.StageEvents),
		},
	}
}

func workbenchPipelineProjectPage(
	board PipelineResponse,
	offset int,
	limit int,
) WorkbenchToolResult {
	operation := WorkbenchOperationPipelineGet
	projected := workbenchPipelineEmptyResponse(board)
	skipped := 0
	returned := 0
	var result WorkbenchToolResult
	for stageIndex := range board.Stages {
		for leadIndex := range board.Stages[stageIndex].Leads {
			if skipped < offset {
				skipped++
				continue
			}
			if returned == limit {
				return result
			}
			stage := &projected.Stages[stageIndex]
			stage.Leads = append(
				stage.Leads,
				workbenchPipelineProjectLead(board.Stages[stageIndex].Leads[leadIndex]),
			)
			candidate := NewWorkbenchToolSuccess(
				operation,
				projected,
				newWorkbenchPage(limit, offset, board.Total, returned+1),
			)
			if !candidate.OK {
				stage.Leads = stage.Leads[:len(stage.Leads)-1]
				if returned == 0 {
					return NewWorkbenchToolFailure(
						operation,
						WorkbenchErrorInternal,
						"pipeline item could not be encoded",
						false,
					)
				}
				return result
			}
			returned++
			result = candidate
		}
	}
	if returned == 0 {
		return NewWorkbenchToolSuccess(
			operation,
			projected,
			newWorkbenchPage(limit, offset, board.Total, 0),
		)
	}
	return result
}

func workbenchPipelineEmptyResponse(
	board PipelineResponse,
) workbenchPipelineResponseProjection {
	projected := workbenchPipelineResponseProjection{
		Stages: make([]workbenchPipelineStageProjection, len(board.Stages)),
		Total:  board.Total,
	}
	for i := range board.Stages {
		projected.Stages[i] = workbenchPipelineStageProjection{
			Stage: board.Stages[i].Stage,
			Leads: []workbenchPipelineLeadListItemProjection{},
		}
	}
	return projected
}

func workbenchPipelineProjectRevisionIdentity(
	revision DemandRevision,
) workbenchPipelineRevisionIdentityProjection {
	return workbenchPipelineRevisionIdentityProjection{
		ID:        revision.ID,
		JobID:     revision.JobID,
		Number:    revision.Number,
		CreatedBy: workbenchPipelineProjectText(revision.CreatedBy, workbenchPipelineDetailTextJSONBytes),
		Reason:    workbenchPipelineProjectText(revision.Reason, workbenchPipelineDetailTextJSONBytes),
		CreatedAt: revision.CreatedAt,
	}
}

func workbenchPipelineProjectRevisionListItem(
	revision DemandRevision,
) workbenchPipelineRevisionListItemProjection {
	createdBy := workbenchPipelineProjectText(
		revision.CreatedBy,
		workbenchPipelineListTextJSONBytes,
	)
	reason := workbenchPipelineProjectText(revision.Reason, workbenchPipelineListTextJSONBytes)
	title := workbenchPipelineProjectText(
		revision.Snapshot.Title,
		workbenchPipelineListTextJSONBytes,
	)
	return workbenchPipelineRevisionListItemProjection{
		ID:                     revision.ID,
		JobID:                  revision.JobID,
		Number:                 revision.Number,
		CreatedByExcerpt:       createdBy.Excerpt,
		CreatedByTruncated:     createdBy.Truncated,
		ReasonExcerpt:          reason.Excerpt,
		ReasonTruncated:        reason.Truncated,
		CreatedAt:              revision.CreatedAt,
		TitleExcerpt:           title.Excerpt,
		TitleTruncated:         title.Truncated,
		RequirementsCount:      len(revision.Snapshot.Requirements),
		RequiredDocumentsCount: len(revision.Snapshot.RequiredDocuments),
	}
}

func workbenchPipelineProjectRevisionDetail(
	revision DemandRevision,
) workbenchPipelineRevisionDetailProjection {
	return workbenchPipelineRevisionDetailProjection{
		ID:        revision.ID,
		JobID:     revision.JobID,
		Number:    revision.Number,
		Snapshot:  workbenchPipelineProjectDemandSnapshot(revision.Snapshot),
		CreatedBy: workbenchPipelineProjectText(revision.CreatedBy, workbenchPipelineDetailTextJSONBytes),
		Reason:    workbenchPipelineProjectText(revision.Reason, workbenchPipelineDetailTextJSONBytes),
		CreatedAt: revision.CreatedAt,
	}
}

func workbenchPipelineProjectRevisionComparison(
	comparison crmPipelineDemandRevisionComparison,
) workbenchPipelineRevisionComparisonProjection {
	changes := make([]workbenchPipelineRevisionChangeProjection, len(comparison.Changes))
	for i := range comparison.Changes {
		changes[i] = workbenchPipelineRevisionChangeProjection{
			Field:  comparison.Changes[i].Field,
			Before: workbenchPipelineProjectRevisionChangeValue(comparison.Changes[i].Before),
			After:  workbenchPipelineProjectRevisionChangeValue(comparison.Changes[i].After),
		}
	}
	return workbenchPipelineRevisionComparisonProjection{
		JobID:         comparison.JobID,
		From:          workbenchPipelineProjectRevisionIdentity(comparison.From),
		To:            workbenchPipelineProjectRevisionIdentity(comparison.To),
		ChangedFields: append([]string{}, comparison.ChangedFields...),
		Changes:       changes,
	}
}

func workbenchPipelineProjectRevisionChangeValue(
	value any,
) workbenchPipelineRevisionChangeValueProjection {
	valueType := "json"
	itemCount := 0
	switch typed := value.(type) {
	case nil:
		valueType = "null"
	case string, JobPriority:
		valueType = "string"
	case int:
		valueType = "integer"
	case []string:
		valueType = "string_list"
		itemCount = len(typed)
	case *time.Time:
		if typed == nil {
			valueType = "null"
		} else {
			valueType = "timestamp"
		}
	case time.Time:
		valueType = "timestamp"
	}
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte("null")
		valueType = "unavailable"
	}
	projected := workbenchPipelineProjectText(string(raw), workbenchPipelineChangeJSONBytes)
	return workbenchPipelineRevisionChangeValueProjection{
		Type:      valueType,
		Excerpt:   projected.Excerpt,
		Truncated: projected.Truncated,
		ItemCount: itemCount,
	}
}

func workbenchPipelineProjectText(value string, maxJSONBytes int) workbenchPipelineTextProjection {
	encoded, _ := json.Marshal(value)
	if len(encoded) <= maxJSONBytes {
		return workbenchPipelineTextProjection{Excerpt: value}
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		candidate, _ := json.Marshal(string(runes[:middle]))
		if len(candidate) <= maxJSONBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return workbenchPipelineTextProjection{
		Excerpt:   string(runes[:low]),
		Truncated: true,
	}
}
