package main

func init() {
	definitions := make([]WorkbenchOperationDefinition, 0, workbenchOperationDefinitionCount)
	definitions = append(definitions, workbenchQueryOperationDefinitions()...)
	definitions = append(definitions, workbenchMutationOperationDefinitions()...)
	definitions = append(definitions, workbenchAllowOnceOperationDefinitions()...)
	declareWorkbenchOperations("core-operation-definitions", definitions...)
}

func workbenchQueryOperationDefinitions() []WorkbenchOperationDefinition {
	tool := AutomationToolWorkbenchQuery
	empty := workbenchNewArguments[WorkbenchEmptyArguments]
	page := workbenchNewArguments[WorkbenchPageArguments]
	id := workbenchNewArguments[WorkbenchIDArguments]
	return []WorkbenchOperationDefinition{
		wiredWorkbenchOperation(tool, WorkbenchOperationCatalogList, "Catalog", "system", "List the sanitized tool and operation catalog.", page, "limit", "offset"),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationSystemHealth, "System health", "system", "Inspect local workbench health.", empty),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationAgentHealth, "Agent health", "system", "Inspect the local agent bridge health.", empty),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationMetricsGet, "Metrics", "system", "Inspect bounded operational metrics.", empty),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationFridaStatus, "Frida status", "system", "Inspect Frida lifecycle status without changing it.", empty),
		wiredWorkbenchOperation(tool, WorkbenchOperationContactsSearch, "Search contacts", "contacts", "Search the local contact cache.", workbenchNewArguments[WorkbenchContactSearchArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationContactsGet, "Contact detail", "contacts", "Inspect one contact by username.", workbenchNewArguments[WorkbenchContactGetArguments], "username"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationGroupsList, "Groups", "contacts", "List cached groups.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationGroupsMembers, "Group members", "contacts", "List cached members of one group.", workbenchNewArguments[WorkbenchGroupMembersArguments], "group_id", "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationMessagesList, "Messages", "crm", "List bounded message records.", workbenchNewArguments[WorkbenchMessageListArguments], "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationMessagesGet, "Message detail", "crm", "Inspect one message record.", id, "id"),
		wiredWorkbenchOperation(tool, WorkbenchOperationCandidatesList, "Candidates", "crm", "List bounded candidate summaries.", workbenchNewArguments[WorkbenchCandidateListArguments], "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationCandidatesGet, "Candidate detail", "crm", "Inspect one candidate.", id, "id"),
		wiredWorkbenchOperation(tool, WorkbenchOperationJobsList, "Jobs", "crm", "List bounded job summaries.", workbenchNewArguments[WorkbenchJobListArguments], "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationJobsGet, "Job detail", "crm", "Inspect one job.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRevisionsList, "Demand revisions", "crm", "List bounded demand revisions for one job.", workbenchNewArguments[WorkbenchRevisionListArguments], "job_id", "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRevisionsGet, "Demand revision", "crm", "Inspect one demand revision.", workbenchNewArguments[WorkbenchRevisionGetArguments], "job_id", "revision_id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRevisionsCompare, "Compare revisions", "crm", "Compare two demand revisions.", workbenchNewArguments[WorkbenchRevisionCompareArguments], "job_id", "from_revision_id", "to_revision_id"),
		wiredWorkbenchOperation(tool, WorkbenchOperationLeadsList, "Leads", "crm", "List bounded pipeline leads.", workbenchNewArguments[WorkbenchLeadListArguments], "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationLeadsGet, "Lead detail", "crm", "Inspect one pipeline lead.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationPipelineGet, "Pipeline", "crm", "Inspect the bounded pipeline board.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationNotesList, "Notes", "crm", "List bounded notes for one target.", workbenchNewArguments[WorkbenchNoteListArguments], "target_type", "target_id", "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationTasksList, "Tasks", "crm", "List bounded follow-up tasks.", workbenchNewArguments[WorkbenchTaskListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationTasksGet, "Task detail", "crm", "Inspect one follow-up task.", id, "id"),
		wiredWorkbenchOperation(tool, WorkbenchOperationOnboardingsList, "Onboarding", "crm", "List bounded onboarding cases.", workbenchNewArguments[WorkbenchOnboardingListArguments], "limit", "offset"),
		wiredWorkbenchOperation(tool, WorkbenchOperationOnboardingsGet, "Onboarding detail", "crm", "Inspect one onboarding case.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceRequestsList, "Service requests", "crm", "List bounded service requests.", workbenchNewArguments[WorkbenchServiceRequestListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceRequestsGet, "Service request detail", "crm", "Inspect one service request.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceQueuesList, "Service queues", "crm", "List service queues.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceQueuesGet, "Service queue detail", "crm", "Inspect one service queue.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesList, "Operation batches", "crm", "List bounded operation batches.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesGet, "Operation batch detail", "crm", "Inspect one operation batch.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesPreview, "Operation batch preview", "crm", "Preview one operation batch without executing it.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsList, "Fund callbacks", "crm", "List bounded fund callbacks.", workbenchNewArguments[WorkbenchFundListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsGet, "Fund callback detail", "crm", "Inspect one fund callback.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsLedger, "Fund ledger", "crm", "Inspect the bounded immutable ledger for one fund callback.", workbenchNewArguments[WorkbenchFundLedgerArguments], "id", "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRecruitingProjectGet, "Recruiting project", "configuration", "Inspect sanitized recruiting project and source configuration.", empty),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRecruitingSourcesList, "Recruiting sources", "configuration", "List configured recruiting source groups.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationForwardRulesList, "Forward rules", "configuration", "List configured forwarding rules.", page, "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationSessionsList, "Automation sessions", "automation", "List bounded automation sessions.", workbenchNewArguments[WorkbenchAutomationListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationSessionsGet, "Automation session detail", "automation", "Inspect one automation session trace.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationRunsList, "Automation runs", "automation", "List bounded automation runs.", workbenchNewArguments[WorkbenchAutomationListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationRunsGet, "Automation run detail", "automation", "Inspect one automation run.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationActionsList, "Automation actions", "automation", "List bounded automation actions.", workbenchNewArguments[WorkbenchAutomationListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationActionsGet, "Automation action detail", "automation", "Inspect one automation action.", id, "id"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationCRMAuditList, "CRM audit", "audit", "List bounded sanitized CRM audit events.", workbenchNewArguments[WorkbenchAuditListArguments], "limit", "offset"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationAutomationAuditList, "Automation audit", "audit", "List bounded sanitized automation audit events.", workbenchNewArguments[WorkbenchAuditListArguments], "limit", "offset"),
	}
}

func workbenchMutationOperationDefinitions() []WorkbenchOperationDefinition {
	tool := AutomationToolWorkbenchMutate
	return []WorkbenchOperationDefinition{
		backingReadyWorkbenchOperation(tool, WorkbenchOperationContactsRefresh, "Refresh contacts", "contacts", "Refresh the local contact cache.", workbenchNewArguments[WorkbenchEmptyArguments]),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationMessagesReview, "Review message", "crm", "Set one message review state.", workbenchNewArguments[WorkbenchMessageReviewArguments], "message_id", "expected_version", "review_status"),
		wiredWorkbenchOperation(tool, WorkbenchOperationCandidatesCreate, "Create candidate", "crm", "Create one candidate.", workbenchNewArguments[WorkbenchCandidateCreateArguments], "candidate"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationCandidatesUpdate, "Update candidate", "crm", "Update one nonterminal candidate.", workbenchNewArguments[WorkbenchCandidateUpdateArguments], "candidate_id", "update"),
		wiredWorkbenchOperation(tool, WorkbenchOperationCandidatesLinkMessage, "Link candidate message", "crm", "Link one persisted message to a candidate.", workbenchNewArguments[WorkbenchCandidateLinkMessageArguments], "request"),
		wiredWorkbenchOperation(tool, WorkbenchOperationJobsCreate, "Create job", "crm", "Create one ordinary job.", workbenchNewArguments[WorkbenchJobCreateArguments], "job"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationJobsUpdate, "Update job", "crm", "Update one nonterminal job.", workbenchNewArguments[WorkbenchJobUpdateArguments], "job_id", "update"),
		wiredWorkbenchOperation(tool, WorkbenchOperationLeadsCreate, "Create lead", "crm", "Create one lead.", workbenchNewArguments[WorkbenchLeadCreateArguments], "lead"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationLeadsUpdate, "Update lead", "crm", "Update one lead owner.", workbenchNewArguments[WorkbenchLeadUpdateArguments], "lead_id", "update"),
		wiredWorkbenchOperation(tool, WorkbenchOperationLeadsTransition, "Transition lead", "crm", "Apply one nonterminal lead transition.", workbenchNewArguments[WorkbenchLeadTransitionArguments], "lead_id", "expected_version", "from_stage", "to_stage"),
		wiredWorkbenchOperation(tool, WorkbenchOperationNotesCreate, "Create note", "crm", "Create one CRM note.", workbenchNewArguments[WorkbenchNoteCreateArguments], "target_type", "target_id", "body"),
		wiredWorkbenchOperation(tool, WorkbenchOperationTasksCreate, "Create task", "crm", "Create one follow-up task.", workbenchNewArguments[WorkbenchTaskCreateArguments], "task"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationTasksUpdate, "Update task", "crm", "Update one follow-up task.", workbenchNewArguments[WorkbenchTaskUpdateArguments], "task_id", "update"),
		wiredWorkbenchOperation(tool, WorkbenchOperationTasksSetStatus, "Set task status", "crm", "Set one reversible follow-up task status.", workbenchNewArguments[WorkbenchTaskStatusArguments], "task_id", "request"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOnboardingsUpdate, "Update onboarding", "crm", "Update ordinary onboarding details.", workbenchNewArguments[WorkbenchOnboardingUpdateArguments], "onboarding_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFleetUpdate, "Update fleet", "crm", "Update ordinary fleet coordination details.", workbenchNewArguments[WorkbenchFleetUpdateArguments], "onboarding_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceRequestsCreate, "Create service request", "crm", "Create one ordinary service request.", workbenchNewArguments[WorkbenchServiceRequestCreateArguments], "request"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceRequestsUpdate, "Update service request", "crm", "Update ordinary service request fields.", workbenchNewArguments[WorkbenchServiceRequestUpdateArguments], "request_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceQueuesCreate, "Create service queue", "crm", "Create one service queue.", workbenchNewArguments[WorkbenchServiceQueueCreateArguments], "queue"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceQueuesUpdate, "Update service queue", "crm", "Update one service queue.", workbenchNewArguments[WorkbenchServiceQueueUpdateArguments], "queue_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesCreate, "Create batch draft", "crm", "Create one nonexecuting operation batch draft.", workbenchNewArguments[WorkbenchOperationBatchCreateArguments], "batch"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesUpdate, "Update batch draft", "crm", "Update one operation batch draft.", workbenchNewArguments[WorkbenchOperationBatchUpdateArguments], "batch_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesMembers, "Update batch members", "crm", "Update members of one operation batch draft.", workbenchNewArguments[WorkbenchOperationBatchMembersArguments], "batch_id", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRecruitingProjectUpdate, "Update recruiting project", "configuration", "Update nonsecret recruiting project fields.", workbenchNewArguments[WorkbenchRecruitingProjectUpdateArguments], "expected_version", "update"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRecruitingSourcesUpsert, "Upsert recruiting source", "configuration", "Create or update one recruiting source group.", workbenchNewArguments[WorkbenchRecruitingSourceUpsertArguments], "expected_project_version", "source"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationForwardRulesCreate, "Create forward rule", "configuration", "Create one configured forwarding rule.", workbenchNewArguments[WorkbenchForwardRuleCreateArguments], "keywords", "cooldown_seconds", "enabled"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationForwardRulesUpdate, "Update forward rule", "configuration", "Update one configured forwarding rule.", workbenchNewArguments[WorkbenchForwardRuleUpdateArguments], "rule_id"),
	}
}

func workbenchAllowOnceOperationDefinitions() []WorkbenchOperationDefinition {
	tool := AutomationToolWorkbenchAllowOnce
	empty := workbenchNewArguments[WorkbenchEmptyArguments]
	versioned := workbenchNewArguments[WorkbenchVersionedIDArguments]
	return []WorkbenchOperationDefinition{
		wiredWorkbenchOperation(tool, WorkbenchOperationMessagesSendPrivate, "Send private message", "messaging", "Send one controlled reply to the persisted private event target.", workbenchNewArguments[WorkbenchControlledSendArguments], "event_id", "intent", "text"),
		wiredWorkbenchOperation(tool, WorkbenchOperationMessagesSendGroup, "Send group message", "messaging", "Send one controlled reply to the persisted group event target.", workbenchNewArguments[WorkbenchControlledSendArguments], "event_id", "intent", "text"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationMessagesSendAudience, "Send to audience", "messaging", "Send one controlled message to a server-owned audience.", workbenchNewArguments[WorkbenchAudienceSendArguments], "audience_id", "intent", "text"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationForwardingSendEvent, "Forward event", "messaging", "Forward one persisted event through one persisted rule.", workbenchNewArguments[WorkbenchForwardEventArguments], "event_id", "rule_id"),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationFridaInitialize, "Initialize Frida", "runtime", "Initialize Frida using server-owned runtime configuration.", empty),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationFridaRescan, "Rescan Frida", "runtime", "Rescan the configured Frida target.", empty),
		forbiddenWorkbenchOperation(tool, WorkbenchOperationFridaDetach, "Detach Frida", "runtime", "Detach the configured Frida session.", empty),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationCandidatesTerminal, "Terminal candidate transition", "crm", "Apply one terminal candidate status transition.", workbenchNewArguments[WorkbenchCandidateTerminalArguments], "candidate_id", "expected_version", "status"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationCandidatesAnonymize, "Anonymize candidate", "crm", "Anonymize one candidate after fresh approval.", versioned, "id", "expected_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationJobsTerminal, "Terminal job transition", "crm", "Apply one terminal job status transition.", workbenchNewArguments[WorkbenchJobTerminalArguments], "job_id", "expected_version", "status"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationJobsUpdateCompensation, "Update job compensation", "crm", "Update compensation or related commitment-bearing job terms.", workbenchNewArguments[WorkbenchJobCompensationArguments], "job_id", "expected_version"),
		wiredWorkbenchOperation(tool, WorkbenchOperationLeadsTerminal, "Terminal lead transition", "crm", "Apply one terminal lead transition.", workbenchNewArguments[WorkbenchLeadTerminalArguments], "lead_id", "expected_version", "from_stage", "to_stage"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRevisionsActivate, "Activate revision", "crm", "Activate one demand revision.", workbenchNewArguments[WorkbenchRevisionActivateArguments], "job_id", "revision_id", "expected_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOnboardingsCreate, "Create onboarding", "crm", "Create onboarding for one lead.", workbenchNewArguments[WorkbenchOnboardingCreateArguments], "onboarding"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOnboardingsTransition, "Transition onboarding", "crm", "Apply one commitment or terminal onboarding transition.", workbenchNewArguments[WorkbenchOnboardingTransitionArguments], "onboarding_id", "expected_version", "to_status"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFleetTransition, "Transition fleet", "crm", "Apply one high-impact fleet transition.", workbenchNewArguments[WorkbenchFleetTransitionArguments], "onboarding_id", "expected_version", "to_status"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationServiceRequestsTransition, "Sensitive service transition", "crm", "Apply one sensitive service request transition.", workbenchNewArguments[WorkbenchServiceRequestTransitionArguments], "request_id", "expected_version", "from_status", "to_status"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationOperationBatchesExecute, "Execute batch", "crm", "Execute one persisted operation batch.", versioned, "id", "expected_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsCreate, "Create fund callback", "funds", "Create one fund callback record without executing payment.", workbenchNewArguments[WorkbenchFundCreateArguments], "candidate_id", "amount_minor", "currency", "condition", "promised_by"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsUpdate, "Update fund callback", "funds", "Update one high-impact fund callback.", workbenchNewArguments[WorkbenchFundUpdateArguments], "fund_id", "expected_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsConfirmCommitment, "Confirm fund commitment", "funds", "Confirm one fund callback commitment.", versioned, "id", "expected_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationFundsConfirmFinal, "Confirm final fund result", "funds", "Confirm one final fund callback result.", workbenchNewArguments[WorkbenchFundFinalArguments], "fund_id", "expected_version", "final_result"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationRecruitingSourcesDelete, "Delete recruiting source", "configuration", "Delete one persisted recruiting source group.", workbenchNewArguments[WorkbenchRecruitingSourceDeleteArguments], "group_id", "expected_project_version"),
		backingReadyWorkbenchOperation(tool, WorkbenchOperationForwardRulesDelete, "Delete forward rule", "configuration", "Delete one persisted forwarding rule.", workbenchNewArguments[WorkbenchIDArguments], "id"),
	}
}

func wiredWorkbenchOperation(
	tool AutomationToolName,
	operation WorkbenchOperation,
	label string,
	category string,
	description string,
	newArguments func() any,
	requiredFields ...string,
) WorkbenchOperationDefinition {
	return newWorkbenchOperationDefinition(
		tool,
		operation,
		label,
		category,
		description,
		newArguments,
		WorkbenchOperationWired,
		WorkbenchExposureAllowed,
		requiredFields...,
	)
}

func backingReadyWorkbenchOperation(
	tool AutomationToolName,
	operation WorkbenchOperation,
	label string,
	category string,
	description string,
	newArguments func() any,
	requiredFields ...string,
) WorkbenchOperationDefinition {
	return newWorkbenchOperationDefinition(
		tool,
		operation,
		label,
		category,
		description,
		newArguments,
		WorkbenchOperationBackingReady,
		WorkbenchExposureAllowed,
		requiredFields...,
	)
}

func forbiddenWorkbenchOperation(
	tool AutomationToolName,
	operation WorkbenchOperation,
	label string,
	category string,
	description string,
	newArguments func() any,
	requiredFields ...string,
) WorkbenchOperationDefinition {
	return newWorkbenchOperationDefinition(
		tool,
		operation,
		label,
		category,
		description,
		newArguments,
		WorkbenchOperationBackingReady,
		WorkbenchExposureForbidden,
		requiredFields...,
	)
}

func newWorkbenchOperationDefinition(
	tool AutomationToolName,
	operation WorkbenchOperation,
	label string,
	category string,
	description string,
	newArguments func() any,
	state WorkbenchImplementationState,
	exposure WorkbenchExposure,
	requiredFields ...string,
) WorkbenchOperationDefinition {
	var policyResolver WorkbenchPolicyResolver
	if exposure == WorkbenchExposureAllowed {
		policyResolver = workbenchDefaultPolicyResolver
	}
	return WorkbenchOperationDefinition{
		Tool:           tool,
		Operation:      operation,
		Label:          label,
		Description:    description,
		Category:       category,
		NewArguments:   newArguments,
		RequiredFields: append([]string(nil), requiredFields...),
		State:          state,
		Exposure:       exposure,
		PolicyResolver: policyResolver,
	}
}
