package main

import (
	"encoding/json"
	"os"
	"strings"
)

const automationPersistentHighRiskSendEnv = "ONEBOT_AUTOMATION_ALLOW_PERSISTENT_HIGH_RISK_SEND"

func persistentHighRiskSendEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(automationPersistentHighRiskSendEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isPersistentHighRiskSendOperation(operation WorkbenchOperation) bool {
	switch operation {
	case WorkbenchOperationMessagesSendPrivate,
		WorkbenchOperationMessagesSendGroup,
		WorkbenchOperationMessagesSendAudience,
		WorkbenchOperationForwardingSendEvent:
		return true
	default:
		return false
	}
}

func workbenchPermissionDecisions(
	tool AutomationToolName,
	operation WorkbenchOperation,
	decisions []AutomationPermissionDecision,
) []AutomationPermissionDecision {
	result := append([]AutomationPermissionDecision(nil), decisions...)
	if tool == AutomationToolWorkbenchAllowOnce &&
		persistentHighRiskSendEnabled() &&
		isPersistentHighRiskSendOperation(operation) &&
		!hasAutomationPermissionDecision(result, AutomationPermissionAllowPersistent) {
		result = append(result, AutomationPermissionAllowPersistent)
	}
	return result
}

func hasAutomationPermissionDecision(
	decisions []AutomationPermissionDecision,
	want AutomationPermissionDecision,
) bool {
	for _, decision := range decisions {
		if decision == want {
			return true
		}
	}
	return false
}

const (
	WorkbenchOperationCatalogList               WorkbenchOperation = "catalog.list"
	WorkbenchOperationSystemHealth              WorkbenchOperation = "system.health"
	WorkbenchOperationAgentHealth               WorkbenchOperation = "agent.health"
	WorkbenchOperationMetricsGet                WorkbenchOperation = "metrics.get"
	WorkbenchOperationFridaStatus               WorkbenchOperation = "frida.status"
	WorkbenchOperationContactsSearch            WorkbenchOperation = "contacts.search"
	WorkbenchOperationContactsGet               WorkbenchOperation = "contacts.get"
	WorkbenchOperationGroupsList                WorkbenchOperation = "groups.list"
	WorkbenchOperationGroupsMembers             WorkbenchOperation = "groups.members"
	WorkbenchOperationMessagesList              WorkbenchOperation = "messages.list"
	WorkbenchOperationMessagesGet               WorkbenchOperation = "messages.get"
	WorkbenchOperationCandidatesList            WorkbenchOperation = "candidates.list"
	WorkbenchOperationCandidatesGet             WorkbenchOperation = "candidates.get"
	WorkbenchOperationJobsList                  WorkbenchOperation = "jobs.list"
	WorkbenchOperationJobsGet                   WorkbenchOperation = "jobs.get"
	WorkbenchOperationRevisionsList             WorkbenchOperation = "revisions.list"
	WorkbenchOperationRevisionsGet              WorkbenchOperation = "revisions.get"
	WorkbenchOperationRevisionsCompare          WorkbenchOperation = "revisions.compare"
	WorkbenchOperationLeadsList                 WorkbenchOperation = "leads.list"
	WorkbenchOperationLeadsGet                  WorkbenchOperation = "leads.get"
	WorkbenchOperationPipelineGet               WorkbenchOperation = "pipeline.get"
	WorkbenchOperationNotesList                 WorkbenchOperation = "notes.list"
	WorkbenchOperationTasksList                 WorkbenchOperation = "tasks.list"
	WorkbenchOperationTasksGet                  WorkbenchOperation = "tasks.get"
	WorkbenchOperationOnboardingsList           WorkbenchOperation = "onboardings.list"
	WorkbenchOperationOnboardingsGet            WorkbenchOperation = "onboardings.get"
	WorkbenchOperationServiceRequestsList       WorkbenchOperation = "service_requests.list"
	WorkbenchOperationServiceRequestsGet        WorkbenchOperation = "service_requests.get"
	WorkbenchOperationServiceQueuesList         WorkbenchOperation = "service_queues.list"
	WorkbenchOperationServiceQueuesGet          WorkbenchOperation = "service_queues.get"
	WorkbenchOperationOperationBatchesList      WorkbenchOperation = "operation_batches.list"
	WorkbenchOperationOperationBatchesGet       WorkbenchOperation = "operation_batches.get"
	WorkbenchOperationOperationBatchesPreview   WorkbenchOperation = "operation_batches.preview"
	WorkbenchOperationFundsList                 WorkbenchOperation = "funds.list"
	WorkbenchOperationFundsGet                  WorkbenchOperation = "funds.get"
	WorkbenchOperationFundsLedger               WorkbenchOperation = "funds.ledger"
	WorkbenchOperationRecruitingProjectGet      WorkbenchOperation = "recruiting_project.get"
	WorkbenchOperationRecruitingSourcesList     WorkbenchOperation = "recruiting_sources.list"
	WorkbenchOperationForwardRulesList          WorkbenchOperation = "forward_rules.list"
	WorkbenchOperationAutomationSessionsList    WorkbenchOperation = "automation_sessions.list"
	WorkbenchOperationAutomationSessionsGet     WorkbenchOperation = "automation_sessions.get"
	WorkbenchOperationAutomationRunsList        WorkbenchOperation = "automation_runs.list"
	WorkbenchOperationAutomationRunsGet         WorkbenchOperation = "automation_runs.get"
	WorkbenchOperationAutomationActionsList     WorkbenchOperation = "automation_actions.list"
	WorkbenchOperationAutomationActionsGet      WorkbenchOperation = "automation_actions.get"
	WorkbenchOperationCRMAuditList              WorkbenchOperation = "crm_audit.list"
	WorkbenchOperationAutomationAuditList       WorkbenchOperation = "automation_audit.list"
	WorkbenchOperationContactsRefresh           WorkbenchOperation = "contacts.refresh"
	WorkbenchOperationMessagesReview            WorkbenchOperation = "messages.review"
	WorkbenchOperationCandidatesCreate          WorkbenchOperation = "candidates.create"
	WorkbenchOperationCandidatesUpdate          WorkbenchOperation = "candidates.update"
	WorkbenchOperationCandidatesLinkMessage     WorkbenchOperation = "candidates.link_message"
	WorkbenchOperationJobsCreate                WorkbenchOperation = "jobs.create"
	WorkbenchOperationJobsUpdate                WorkbenchOperation = "jobs.update"
	WorkbenchOperationLeadsCreate               WorkbenchOperation = "leads.create"
	WorkbenchOperationLeadsUpdate               WorkbenchOperation = "leads.update"
	WorkbenchOperationLeadsTransition           WorkbenchOperation = "leads.transition"
	WorkbenchOperationNotesCreate               WorkbenchOperation = "notes.create"
	WorkbenchOperationTasksCreate               WorkbenchOperation = "tasks.create"
	WorkbenchOperationTasksUpdate               WorkbenchOperation = "tasks.update"
	WorkbenchOperationTasksSetStatus            WorkbenchOperation = "tasks.set_status"
	WorkbenchOperationOnboardingsUpdate         WorkbenchOperation = "onboardings.update"
	WorkbenchOperationFleetUpdate               WorkbenchOperation = "fleet.update"
	WorkbenchOperationServiceRequestsCreate     WorkbenchOperation = "service_requests.create"
	WorkbenchOperationServiceRequestsUpdate     WorkbenchOperation = "service_requests.update"
	WorkbenchOperationServiceQueuesCreate       WorkbenchOperation = "service_queues.create"
	WorkbenchOperationServiceQueuesUpdate       WorkbenchOperation = "service_queues.update"
	WorkbenchOperationOperationBatchesCreate    WorkbenchOperation = "operation_batches.create"
	WorkbenchOperationOperationBatchesUpdate    WorkbenchOperation = "operation_batches.update"
	WorkbenchOperationOperationBatchesMembers   WorkbenchOperation = "operation_batches.members"
	WorkbenchOperationRecruitingProjectUpdate   WorkbenchOperation = "recruiting_project.update"
	WorkbenchOperationRecruitingSourcesUpsert   WorkbenchOperation = "recruiting_sources.upsert"
	WorkbenchOperationForwardRulesCreate        WorkbenchOperation = "forward_rules.create"
	WorkbenchOperationForwardRulesUpdate        WorkbenchOperation = "forward_rules.update"
	WorkbenchOperationMessagesSendPrivate       WorkbenchOperation = "messages.send_private"
	WorkbenchOperationMessagesSendGroup         WorkbenchOperation = "messages.send_group"
	WorkbenchOperationMessagesSendAudience      WorkbenchOperation = "messages.send_audience"
	WorkbenchOperationForwardingSendEvent       WorkbenchOperation = "forwarding.send_event"
	WorkbenchOperationFridaInitialize           WorkbenchOperation = "frida.initialize"
	WorkbenchOperationFridaRescan               WorkbenchOperation = "frida.rescan"
	WorkbenchOperationFridaDetach               WorkbenchOperation = "frida.detach"
	WorkbenchOperationCandidatesTerminal        WorkbenchOperation = "candidates.transition_terminal"
	WorkbenchOperationCandidatesAnonymize       WorkbenchOperation = "candidates.anonymize"
	WorkbenchOperationJobsTerminal              WorkbenchOperation = "jobs.transition_terminal"
	WorkbenchOperationJobsUpdateCompensation    WorkbenchOperation = "jobs.update_compensation"
	WorkbenchOperationLeadsTerminal             WorkbenchOperation = "leads.transition_terminal"
	WorkbenchOperationRevisionsActivate         WorkbenchOperation = "revisions.activate"
	WorkbenchOperationOnboardingsCreate         WorkbenchOperation = "onboardings.create"
	WorkbenchOperationOnboardingsTransition     WorkbenchOperation = "onboardings.transition"
	WorkbenchOperationFleetTransition           WorkbenchOperation = "fleet.transition"
	WorkbenchOperationServiceRequestsTransition WorkbenchOperation = "service_requests.transition_sensitive"
	WorkbenchOperationOperationBatchesExecute   WorkbenchOperation = "operation_batches.execute"
	WorkbenchOperationFundsCreate               WorkbenchOperation = "funds.create"
	WorkbenchOperationFundsUpdate               WorkbenchOperation = "funds.update"
	WorkbenchOperationFundsConfirmCommitment    WorkbenchOperation = "funds.confirm_commitment"
	WorkbenchOperationFundsConfirmFinal         WorkbenchOperation = "funds.confirm_final"
	WorkbenchOperationRecruitingSourcesDelete   WorkbenchOperation = "recruiting_sources.delete"
	WorkbenchOperationForwardRulesDelete        WorkbenchOperation = "forward_rules.delete"
)

type WorkbenchOperationCatalogEntry struct {
	Operation            WorkbenchOperation             `json:"operation"`
	Label                string                         `json:"label"`
	Description          string                         `json:"description"`
	Category             string                         `json:"category"`
	Risk                 AutomationRisk                 `json:"risk"`
	SideEffect           WorkbenchSideEffect            `json:"side_effect"`
	ImplementationStatus WorkbenchImplementationState   `json:"implementation_status"`
	Exposure             WorkbenchExposure              `json:"exposure"`
	Availability         WorkbenchAvailability          `json:"availability"`
	ValidDecisions       []AutomationPermissionDecision `json:"valid_decisions"`
	ArgumentsSchema      json.RawMessage                `json:"arguments_schema"`
	MaxResultBytes       int                            `json:"max_result_bytes"`
}

type WorkbenchToolCatalogEntry struct {
	Name             AutomationToolName               `json:"name"`
	Label            string                           `json:"label"`
	Description      string                           `json:"description"`
	Risk             AutomationRisk                   `json:"risk"`
	SideEffect       WorkbenchSideEffect              `json:"side_effect"`
	Availability     WorkbenchAvailability            `json:"availability"`
	ValidDecisions   []AutomationPermissionDecision   `json:"valid_decisions"`
	MaxArgumentBytes int                              `json:"max_argument_bytes"`
	MaxResultBytes   int                              `json:"max_result_bytes"`
	Operations       []WorkbenchOperationCatalogEntry `json:"operations"`
}

type WorkbenchExclusion struct {
	Class       string `json:"class"`
	Description string `json:"description"`
}

type WorkbenchCatalog struct {
	Version    string                      `json:"version"`
	Tools      []WorkbenchToolCatalogEntry `json:"tools"`
	Exclusions []WorkbenchExclusion        `json:"exclusions"`
}

type WorkbenchAvailableCatalog struct {
	Version string                      `json:"version"`
	Tools   []WorkbenchToolCatalogEntry `json:"tools"`
}

type workbenchOperationSpec struct {
	catalog        WorkbenchOperationCatalogEntry
	requiredFields []string
	newArguments   func() any
}

func WorkbenchProviderTools() []AutomationToolName {
	return []AutomationToolName{
		AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
		AutomationToolWorkbenchAllowOnce,
	}
}

func WorkbenchProviderToolDefinitions() ([]AutomationToolDefinition, error) {
	return AutomationToolDefinitions(WorkbenchProviderTools())
}

func WorkbenchToolCatalog() WorkbenchCatalog {
	return workbenchToolCatalogWithDenySnapshot(workbenchCatalogDenySnapshot())
}

func workbenchToolCatalogWithDenySnapshot(
	denied map[string]struct{},
) WorkbenchCatalog {
	tools := make([]WorkbenchToolCatalogEntry, 0, 3)
	for _, tool := range WorkbenchProviderTools() {
		entry, _ := workbenchToolCatalogEntryWithDenySnapshot(tool, denied)
		tools = append(tools, entry)
	}
	return WorkbenchCatalog{
		Version: WorkbenchToolCatalogVersion,
		Tools:   tools,
		Exclusions: []WorkbenchExclusion{
			{Class: "self_modification", Description: "Provider, API key, prompt, policy, permission, authorization, and runtime configuration changes are unavailable."},
			{Class: "secrets", Description: "Credentials, tokens, cookies, private keys, and secret stores are unavailable."},
			{Class: "exports", Description: "Exports, backups, unrestricted snapshots, and bulk data extraction are unavailable."},
			{Class: "raw_attachments", Description: "Raw attachment bodies and attachment streams are unavailable."},
			{Class: "arbitrary_execution", Description: "Arbitrary HTTP, network, filesystem, shell, SQL, scripts, plugins, and generic RPC are unavailable."},
			{Class: "runtime_targets", Description: "Arbitrary recipients, sinks, URLs, PIDs, process names, Frida scripts, and filesystem paths are unavailable."},
			{Class: "payment_execution", Description: "Payment execution and generic destructive operations are unavailable."},
		},
	}
}

func WorkbenchAvailableToolCatalog() WorkbenchAvailableCatalog {
	return workbenchAvailableToolCatalogWithDenySnapshot(workbenchCatalogDenySnapshot())
}

func workbenchAvailableToolCatalogWithDenySnapshot(
	denied map[string]struct{},
) WorkbenchAvailableCatalog {
	full := workbenchToolCatalogWithDenySnapshot(denied)
	tools := make([]WorkbenchToolCatalogEntry, 0, len(full.Tools))
	for _, tool := range full.Tools {
		filtered := make([]WorkbenchOperationCatalogEntry, 0, len(tool.Operations))
		for _, operation := range tool.Operations {
			if operation.Availability == WorkbenchAvailabilityAvailable {
				filtered = append(filtered, cloneWorkbenchOperationCatalogEntry(operation))
			}
		}
		tool.ValidDecisions = append([]AutomationPermissionDecision(nil), tool.ValidDecisions...)
		tool.Operations = filtered
		tools = append(tools, tool)
	}
	return WorkbenchAvailableCatalog{
		Version: full.Version,
		Tools:   tools,
	}
}

func workbenchCatalogDenySnapshot() map[string]struct{} {
	workbenchOperationDenyMu.RLock()
	defer workbenchOperationDenyMu.RUnlock()
	snapshot := make(map[string]struct{}, len(workbenchOperationDenySet))
	for key := range workbenchOperationDenySet {
		snapshot[key] = struct{}{}
	}
	return snapshot
}

func workbenchAvailableCatalogOperationCount(catalog WorkbenchAvailableCatalog) int {
	total := 0
	for _, tool := range catalog.Tools {
		total += len(tool.Operations)
	}
	return total
}

func workbenchAvailableCatalogSlice(
	catalog WorkbenchAvailableCatalog,
	offset int,
	count int,
) WorkbenchAvailableCatalog {
	tools := make([]WorkbenchToolCatalogEntry, len(catalog.Tools))
	remainingOffset := offset
	remainingCount := count
	for index, tool := range catalog.Tools {
		cloned := tool
		cloned.ValidDecisions = append([]AutomationPermissionDecision(nil), tool.ValidDecisions...)
		cloned.Operations = make([]WorkbenchOperationCatalogEntry, 0)
		if remainingOffset >= len(tool.Operations) {
			remainingOffset -= len(tool.Operations)
		} else if remainingCount > 0 {
			start := remainingOffset
			remainingOffset = 0
			end := start + remainingCount
			if end > len(tool.Operations) {
				end = len(tool.Operations)
			}
			cloned.Operations = make([]WorkbenchOperationCatalogEntry, end-start)
			for operationIndex := start; operationIndex < end; operationIndex++ {
				cloned.Operations[operationIndex-start] = cloneWorkbenchOperationCatalogEntry(
					tool.Operations[operationIndex],
				)
			}
			remainingCount -= end - start
		}
		tools[index] = cloned
	}
	return WorkbenchAvailableCatalog{
		Version: catalog.Version,
		Tools:   tools,
	}
}

func WorkbenchToolCatalogEntryFor(tool AutomationToolName) (WorkbenchToolCatalogEntry, bool) {
	return workbenchToolCatalogEntryWithDenySnapshot(tool, workbenchCatalogDenySnapshot())
}

func workbenchToolCatalogEntryWithDenySnapshot(
	tool AutomationToolName,
	denied map[string]struct{},
) (WorkbenchToolCatalogEntry, bool) {
	risk, sideEffect, decisions, label, description, ok := workbenchToolMetadata(tool)
	if !ok {
		return WorkbenchToolCatalogEntry{}, false
	}
	definitions := workbenchOperationDefinitionsForTool(tool)
	operations := make([]WorkbenchOperationCatalogEntry, len(definitions))
	for i, definition := range definitions {
		operation, err := workbenchOperationCatalogEntryWithDenySnapshot(
			definition,
			risk,
			sideEffect,
			workbenchPermissionDecisions(tool, definition.Operation, decisions),
			denied,
		)
		if err != nil {
			panic(err)
		}
		operations[i] = operation
	}
	return WorkbenchToolCatalogEntry{
		Name:             tool,
		Label:            label,
		Description:      description,
		Risk:             risk,
		SideEffect:       sideEffect,
		Availability:     WorkbenchAvailabilityAvailable,
		ValidDecisions:   append([]AutomationPermissionDecision(nil), decisions...),
		MaxArgumentBytes: WorkbenchMaxArgumentsJSONBytes,
		MaxResultBytes:   WorkbenchMaxResultJSONBytes,
		Operations:       operations,
	}, true
}

func workbenchOperationCatalogEntryWithDenySnapshot(
	definition WorkbenchOperationDefinition,
	risk AutomationRisk,
	sideEffect WorkbenchSideEffect,
	decisions []AutomationPermissionDecision,
	denied map[string]struct{},
) (WorkbenchOperationCatalogEntry, error) {
	schema, err := workbenchArgumentsSchema(definition.NewArguments())
	if err != nil {
		return WorkbenchOperationCatalogEntry{}, err
	}
	if definition.Tool == AutomationToolWorkbenchQuery {
		schema, _ = workbenchOptionalQueryPagination(schema, definition.RequiredFields)
	}
	available := workbenchOperationBaseAvailable(definition)
	if _, isDenied := denied[workbenchOperationDenyKey(definition.Tool, definition.Operation)]; isDenied {
		available = false
	}
	availability := WorkbenchAvailabilityUnavailable
	if available {
		availability = WorkbenchAvailabilityAvailable
	} else {
		decisions = []AutomationPermissionDecision{}
	}
	return WorkbenchOperationCatalogEntry{
		Operation:            definition.Operation,
		Label:                definition.Label,
		Description:          definition.Description,
		Category:             definition.Category,
		Risk:                 risk,
		SideEffect:           sideEffect,
		ImplementationStatus: definition.State,
		Exposure:             definition.Exposure,
		Availability:         availability,
		ValidDecisions:       append([]AutomationPermissionDecision(nil), decisions...),
		ArgumentsSchema:      schema,
		MaxResultBytes:       WorkbenchMaxResultJSONBytes,
	}, nil
}

func WorkbenchOperationCatalogEntryFor(
	tool AutomationToolName,
	operation WorkbenchOperation,
) (WorkbenchOperationCatalogEntry, bool) {
	spec, ok := lookupWorkbenchOperationSpec(tool, operation)
	if !ok {
		return WorkbenchOperationCatalogEntry{}, false
	}
	entry := cloneWorkbenchOperationCatalogEntry(spec.catalog)
	entry.ValidDecisions = workbenchPermissionDecisions(tool, operation, entry.ValidDecisions)
	return entry, true
}

func WorkbenchValidPermissionDecisions(
	tool AutomationToolName,
	operation WorkbenchOperation,
) []AutomationPermissionDecision {
	entry, ok := WorkbenchOperationCatalogEntryFor(tool, operation)
	if !ok {
		return []AutomationPermissionDecision{}
	}
	return append([]AutomationPermissionDecision(nil), entry.ValidDecisions...)
}

func IsValidWorkbenchOperation(tool AutomationToolName, operation WorkbenchOperation) bool {
	_, ok := WorkbenchOperationDefinitionFor(tool, operation)
	return ok
}

func workbenchToolMetadata(
	tool AutomationToolName,
) (
	AutomationRisk,
	WorkbenchSideEffect,
	[]AutomationPermissionDecision,
	string,
	string,
	bool,
) {
	switch tool {
	case AutomationToolWorkbenchQuery:
		return AutomationRiskR0,
			WorkbenchSideEffectReadOnly,
			[]AutomationPermissionDecision{},
			"Workbench Query",
			"Inspect bounded, sanitized workbench data without side effects.",
			true
	case AutomationToolWorkbenchMutate:
		return AutomationRiskR1,
			WorkbenchSideEffectReversible,
			[]AutomationPermissionDecision{
				AutomationPermissionAllowOnce,
				AutomationPermissionAllowRun,
				AutomationPermissionAllowPersistent,
				AutomationPermissionDenyOnce,
				AutomationPermissionDenyPersistent,
			},
			"Workbench Mutate",
			"Apply one reversible local workbench mutation through existing domain services.",
			true
	case AutomationToolWorkbenchAllowOnce:
		return AutomationRiskR3,
			WorkbenchSideEffectHighImpact,
			[]AutomationPermissionDecision{
				AutomationPermissionAllowOnce,
				AutomationPermissionDenyOnce,
			},
			"Workbench Allow Once",
			"Request one fresh approval for a bounded high-impact operation.",
			true
	default:
		return "", "", nil, "", "", false
	}
}

func workbenchOperationAvailability(
	tool AutomationToolName,
	operation WorkbenchOperation,
) WorkbenchAvailability {
	definition, ok := WorkbenchOperationDefinitionFor(tool, operation)
	if ok && workbenchOperationBaseAvailable(definition) {
		return WorkbenchAvailabilityAvailable
	}
	return WorkbenchAvailabilityUnavailable
}

func cloneWorkbenchOperationCatalogEntry(
	entry WorkbenchOperationCatalogEntry,
) WorkbenchOperationCatalogEntry {
	entry.ValidDecisions = append([]AutomationPermissionDecision(nil), entry.ValidDecisions...)
	entry.ArgumentsSchema = append(json.RawMessage(nil), entry.ArgumentsSchema...)
	return entry
}
