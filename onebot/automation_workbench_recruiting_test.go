package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type m304AudienceResolver struct {
	audience AutomationAudience
}

func (r m304AudienceResolver) ResolveAutomationAudience(id string) (AutomationAudience, bool) {
	if id != r.audience.ID {
		return AutomationAudience{}, false
	}
	return r.audience, true
}

type m304CancellingAudienceResolver struct {
	audience AutomationAudience
	cancel   context.CancelFunc
}

func (r m304CancellingAudienceResolver) ResolveAutomationAudience(id string) (AutomationAudience, bool) {
	if id != r.audience.ID {
		return AutomationAudience{}, false
	}
	r.cancel()
	return r.audience, true
}

type m304Sender struct {
	calls atomic.Int32
}

func (s *m304Sender) SendPrivateText(
	ctx context.Context,
	peerID string,
	text string,
) AutomationDeliveryResult {
	return s.SendText(ctx, ChatTypePrivate, peerID, text)
}

func (s *m304Sender) SendText(
	_ context.Context,
	_ ChatType,
	_ string,
	_ string,
) AutomationDeliveryResult {
	s.calls.Add(1)
	return AutomationDeliveryResult{
		Status:            AutomationDeliverySent,
		DispatchAttempted: true,
	}
}

func TestWorkbenchRecruitingConfigurationOperations(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	rules := NewForwardRuleStore(crm)
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: crm, ForwardRules: rules,
	})

	project := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRecruitingProjectGet,
		WorkbenchEmptyArguments{},
	)
	m304AssertSuccess(t, project)
	m304AssertJSONKeys(t, project.Data,
		"organization_name", "require_authoritative_job", "source_group_count", "version")

	name := "Synthetic Recruiting"
	updated := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationRecruitingProjectUpdate,
		WorkbenchRecruitingProjectUpdateArguments{
			ExpectedVersion: 1,
			Update: RecruitingProjectUpdate{
				OrganizationName: &name,
			},
		},
	)
	m304AssertSuccess(t, updated)
	var projectResult workbenchRecruitingProjectProjection
	m304Decode(t, updated.Data, &projectResult)
	if projectResult.OrganizationName != name || projectResult.Version != 2 {
		t.Fatalf("project update = %+v", projectResult)
	}

	upsert := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationRecruitingSourcesUpsert,
		WorkbenchRecruitingSourceUpsertArguments{
			ExpectedProjectVersion: 2,
			Source: RecruitingSourceGroupUpsert{
				GroupID: "jobs_b@chatroom", DisplayName: "Jobs B",
				Enabled: true, GateEnabled: true,
			},
		},
	)
	m304AssertSuccess(t, upsert)
	var sourceResult workbenchRecruitingSourceMutationResult
	m304Decode(t, upsert.Data, &sourceResult)
	if sourceResult.ProjectVersion != 3 ||
		sourceResult.Source.GroupID != "jobs_b@chatroom" {
		t.Fatalf("source upsert = %+v", sourceResult)
	}
	_, err := crm.UpsertRecruitingSourceGroup(3, RecruitingSourceGroupUpsert{
		GroupID: "jobs_a@chatroom", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRecruitingSourcesList,
		WorkbenchPageArguments{Limit: 1, Offset: 0},
	)
	m304AssertSuccess(t, list)
	var sourceList workbenchRecruitingSourceListData
	m304Decode(t, list.Data, &sourceList)
	if len(sourceList.Sources) != 1 ||
		sourceList.Sources[0].ID != "source_00000001" ||
		list.Page == nil ||
		list.Page.Total != 2 ||
		!list.Page.HasMore ||
		list.Page.NextOffset != 1 {
		t.Fatalf("source list = %+v page=%+v", sourceList, list.Page)
	}

	createdRule := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationForwardRulesCreate,
		WorkbenchForwardRuleCreateArguments{
			GroupID: "jobs_a@chatroom", Keywords: []string{"job"},
			CooldownSeconds: 60, Enabled: true,
		},
	)
	m304AssertSuccess(t, createdRule)
	var rule ForwardRule
	m304Decode(t, createdRule.Data, &rule)
	if rule.ID != "rule_00000001" {
		t.Fatalf("created rule = %+v", rule)
	}
	disabled := false
	updatedRule := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationForwardRulesUpdate,
		WorkbenchForwardRuleUpdateArguments{RuleID: rule.ID, Enabled: &disabled},
	)
	m304AssertSuccess(t, updatedRule)
	m304Decode(t, updatedRule.Data, &rule)
	if rule.Enabled {
		t.Fatalf("updated rule = %+v", rule)
	}
}

func TestWorkbenchRecruitingListsBoundMaximumRecords(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	project := crm.RecruitingProject()
	for index := 0; index < WorkbenchMaxPageLimit; index++ {
		var err error
		project, err = crm.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
			GroupID:     "jobs_" + m304Number(index) + "@chatroom",
			DisplayName: "source",
			Enabled:     true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rules := NewForwardRuleStore(crm)
	for index := 0; index < WorkbenchMaxPageLimit; index++ {
		if _, err := rules.Create(
			"jobs_"+m304Number(index)+"@chatroom",
			[]string{"keyword"},
			index,
			true,
		); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: crm, ForwardRules: rules,
	})
	for _, fixture := range []struct {
		operation WorkbenchOperation
	}{
		{WorkbenchOperationRecruitingSourcesList},
		{WorkbenchOperationForwardRulesList},
	} {
		result := m304Dispatch(
			t, dispatcher, WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			fixture.operation,
			WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: 0},
		)
		m304AssertSuccess(t, result)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf("%s result bytes = %d", fixture.operation, len(encoded))
		}
		if result.Page == nil || result.Page.Total != WorkbenchMaxPageLimit ||
			result.Page.Returned < 1 || result.Page.Returned > WorkbenchMaxPageLimit {
			t.Fatalf("%s page = %+v", fixture.operation, result.Page)
		}
	}
}

func TestWorkbenchRecruitingErrorsPermissionsAndUnsafeTargets(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	rules := NewForwardRuleStore(crm)
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: crm, ForwardRules: rules,
	})
	stale := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationRecruitingProjectUpdate,
		WorkbenchRecruitingProjectUpdateArguments{
			ExpectedVersion: 99,
			Update: RecruitingProjectUpdate{
				RequireAuthoritativeJob: m304Bool(false),
			},
		},
	)
	m304AssertFailure(t, stale, WorkbenchErrorConflict)

	unsafeRule := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationForwardRulesCreate,
		WorkbenchForwardRuleCreateArguments{
			GroupID:  "https://example.invalid/@chatroom",
			Keywords: []string{"job"}, Enabled: true,
		},
	)
	m304AssertFailure(t, unsafeRule, WorkbenchErrorInvalidArguments)
	if len(rules.List()) != 0 {
		t.Fatal("unsafe forward target was persisted")
	}
	unsafeKeyword := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationForwardRulesCreate,
		WorkbenchForwardRuleCreateArguments{
			GroupID:  "jobs@chatroom",
			Keywords: []string{"/etc/passwd"}, Enabled: true,
		},
	)
	m304AssertFailure(t, unsafeKeyword, WorkbenchErrorInvalidArguments)

	project, err := crm.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
		GroupID: "jobs@chatroom", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	denied := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationRecruitingSourcesDelete,
		WorkbenchRecruitingSourceDeleteArguments{
			GroupID: "jobs@chatroom", ExpectedProjectVersion: project.Version,
		},
	)
	m304AssertFailure(t, denied, WorkbenchErrorAllowOnceRequired)
	if len(crm.RecruitingProject().SourceGroups) != 1 {
		t.Fatal("source delete mutated before approval")
	}

	approved := m304ApprovedFixture(t, WorkbenchOperationRecruitingSourcesDelete)
	approvedProject, err := approved.crm.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
		GroupID: "jobs@chatroom", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalMismatch := m304Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
			CRM: approved.crm, Automation: approved.automation,
		}),
		approved.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationRecruitingSourcesDelete,
		WorkbenchRecruitingSourceDeleteArguments{
			GroupID: "other@chatroom", ExpectedProjectVersion: approvedProject.Version,
		},
	)
	m304AssertFailure(t, approvalMismatch, WorkbenchErrorInvalidArguments)
	if len(approved.crm.RecruitingProject().SourceGroups) != 1 {
		t.Fatal("mismatched approved delete mutated recruiting sources")
	}
	if _, ok := workbenchRecruitingFindBridge(
		approved.automation.Snapshot(),
		approved.parent.ID,
		workbenchRecruitingBridgeDelete,
	); ok {
		t.Fatal("mismatched approved delete created an execution bridge")
	}

	missingDependency := m304Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{}),
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationForwardRulesList,
		WorkbenchPageArguments{Limit: 10},
	)
	m304AssertFailure(t, missingDependency, WorkbenchErrorUnavailable)
}

func TestWorkbenchRecruitingAudienceSendUsesOutboxOnce(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 2)
	resolver := &m304AudienceResolver{audience: audience}
	sender := &m304Sender{}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: fixture.crm, Automation: fixture.automation,
		Audiences: resolver,
		Sender:    sender, ForwardRules: NewForwardRuleStore(fixture.crm),
	}).(*defaultWorkbenchToolDispatcher)
	arguments := WorkbenchAudienceSendArguments{
		AudienceID: audience.ID,
		Intent:     AutomationReplyIntentScheduling,
		Text:       content,
	}
	parent := fixture.parent
	parent.Recipient = workbenchRecruitingAudienceRecipient(audience)

	const workers = 12
	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- workbenchRecruitingSendApprovedAudience(
				context.Background(),
				dispatcher,
				fixture.execution,
				parent,
				&arguments,
			)
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result.OK {
			successes++
			continue
		}
		if result.Error == nil || result.Error.Code != WorkbenchErrorConflict {
			t.Fatalf("concurrent audience result = %s", m304ResultSummary(result))
		}
	}
	if successes != 1 || sender.calls.Load() != 1 {
		t.Fatalf("audience send successes=%d sender calls=%d", successes, sender.calls.Load())
	}
	resolver.audience.Enabled = false
	replayed := workbenchRecruitingSendApprovedAudience(
		context.Background(),
		dispatcher,
		fixture.execution,
		parent,
		&arguments,
	)
	m304AssertSuccess(t, replayed)
	if sender.calls.Load() != 1 {
		t.Fatalf("replay sender calls = %d", sender.calls.Load())
	}
	state := fixture.automation.Snapshot()
	if len(state.Outbox) != 1 || state.Outbox[0].Status != AutomationOutboxSent {
		t.Fatalf("audience outbox = %+v", state.Outbox)
	}
	bridge, ok := workbenchRecruitingFindBridge(
		state, fixture.parent.ID, workbenchRecruitingBridgeAudience,
	)
	if !ok || bridge.Recipient == nil ||
		bridge.Recipient.AudienceID != audience.ID ||
		bridge.Recipient.AudienceVersion != audience.Version {
		t.Fatalf("audience bridge = %+v", bridge)
	}
}

func TestWorkbenchRecruitingAudiencePolicyPersistsRecipientBinding(t *testing.T) {
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 2)
	decoded := m304Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationMessagesSendAudience,
		WorkbenchAudienceSendArguments{
			AudienceID: audience.ID,
			Intent:     AutomationReplyIntentScheduling,
			Text:       content,
		},
	)
	recipient, err := workbenchRecruitingAudiencePolicyResolver(
		NewServerAutomationRiskEvaluator(m304AudienceResolver{audience: audience}),
		AutomationPolicyInput{RunActionCount: 1},
		decoded,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recipient == nil ||
		recipient.AudienceID != audience.ID ||
		recipient.AudienceVersion != audience.Version ||
		recipient.ChatType != audience.ChatType ||
		recipient.PeerID != audience.PeerID {
		t.Fatalf("approval recipient binding = %+v, audience = %+v", recipient, audience)
	}
}

func TestWorkbenchRecruitingAudienceMutationAfterApprovalStaysOffline(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*AutomationAudience)
		code   WorkbenchErrorCode
	}{
		{
			name: "version",
			mutate: func(audience *AutomationAudience) {
				audience.Version++
			},
			code: WorkbenchErrorConflict,
		},
		{
			name: "peer",
			mutate: func(audience *AutomationAudience) {
				audience.PeerID = "wxid_replacement"
			},
			code: WorkbenchErrorForbidden,
		},
		{
			name: "chat_type",
			mutate: func(audience *AutomationAudience) {
				audience.ChatType = ChatTypeGroup
				audience.PeerID = "jobs@chatroom"
			},
			code: WorkbenchErrorForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
			content := "Approved scheduling message."
			audience := m304Audience("aud_00000001", content, 2)
			resolver := &m304AudienceResolver{audience: audience}
			sender := &m304Sender{}
			dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
				CRM:          fixture.crm,
				Automation:   fixture.automation,
				Audiences:    resolver,
				Sender:       sender,
				ForwardRules: NewForwardRuleStore(fixture.crm),
			}).(*defaultWorkbenchToolDispatcher)
			parent := fixture.parent
			parent.Recipient = workbenchRecruitingAudienceRecipient(audience)
			before := fixture.automation.Snapshot()
			test.mutate(&resolver.audience)
			result := workbenchRecruitingSendApprovedAudience(
				context.Background(),
				dispatcher,
				fixture.execution,
				parent,
				&WorkbenchAudienceSendArguments{
					AudienceID: audience.ID,
					Intent:     AutomationReplyIntentScheduling,
					Text:       content,
				},
			)
			m304AssertFailure(t, result, test.code)
			after := fixture.automation.Snapshot()
			m304AssertNoAudienceSideEffects(t, before, after, sender)
		})
	}
}

func TestWorkbenchRecruitingUnboundAudienceApprovalStaysOffline(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 2)
	audience.PeerID = "wxid_replacement"
	sender := &m304Sender{}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          fixture.crm,
		Automation:   fixture.automation,
		Audiences:    m304AudienceResolver{audience: audience},
		Sender:       sender,
		ForwardRules: NewForwardRuleStore(fixture.crm),
	})
	before := fixture.automation.Snapshot()
	result := m304Dispatch(
		t,
		dispatcher,
		fixture.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationMessagesSendAudience,
		WorkbenchAudienceSendArguments{
			AudienceID: audience.ID,
			Intent:     AutomationReplyIntentScheduling,
			Text:       content,
		},
	)
	m304AssertFailure(t, result, WorkbenchErrorForbidden)
	m304AssertNoAudienceSideEffects(
		t,
		before,
		fixture.automation.Snapshot(),
		sender,
	)
}

func TestWorkbenchRecruitingAudienceMissingOrPartialBindingStaysOffline(t *testing.T) {
	for _, test := range []struct {
		name              string
		recipient         func(AutomationAudience) *AutomationRecipient
		mutateAudience    func(*AutomationAudience)
		expectedErrorCode WorkbenchErrorCode
	}{
		{
			name: "nil",
			recipient: func(AutomationAudience) *AutomationRecipient {
				return nil
			},
			expectedErrorCode: WorkbenchErrorForbidden,
		},
		{
			name: "nil_version_1_peer_changed",
			recipient: func(AutomationAudience) *AutomationRecipient {
				return nil
			},
			mutateAudience: func(audience *AutomationAudience) {
				audience.PeerID = "wxid_replacement"
			},
			expectedErrorCode: WorkbenchErrorForbidden,
		},
		{
			name: "zero_version",
			recipient: func(audience AutomationAudience) *AutomationRecipient {
				recipient := workbenchRecruitingAudienceRecipient(audience)
				recipient.AudienceVersion = 0
				return recipient
			},
			expectedErrorCode: WorkbenchErrorForbidden,
		},
		{
			name: "missing_audience_id",
			recipient: func(audience AutomationAudience) *AutomationRecipient {
				recipient := workbenchRecruitingAudienceRecipient(audience)
				recipient.AudienceID = ""
				return recipient
			},
			expectedErrorCode: WorkbenchErrorForbidden,
		},
		{
			name: "missing_peer",
			recipient: func(audience AutomationAudience) *AutomationRecipient {
				recipient := workbenchRecruitingAudienceRecipient(audience)
				recipient.PeerID = ""
				return recipient
			},
			expectedErrorCode: WorkbenchErrorForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
			content := "Approved scheduling message."
			audience := m304Audience("aud_00000001", content, 2)
			parent := fixture.parent
			parent.Recipient = test.recipient(audience)
			if test.mutateAudience != nil {
				test.mutateAudience(&audience)
			}
			sender := &m304Sender{}
			dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
				CRM:          fixture.crm,
				Automation:   fixture.automation,
				Audiences:    m304AudienceResolver{audience: audience},
				Sender:       sender,
				ForwardRules: NewForwardRuleStore(fixture.crm),
			}).(*defaultWorkbenchToolDispatcher)
			before := fixture.automation.Snapshot()
			result := workbenchRecruitingSendApprovedAudience(
				context.Background(),
				dispatcher,
				fixture.execution,
				parent,
				&WorkbenchAudienceSendArguments{
					AudienceID: audience.ID,
					Intent:     AutomationReplyIntentScheduling,
					Text:       content,
				},
			)
			m304AssertFailure(t, result, test.expectedErrorCode)
			m304AssertNoAudienceSideEffects(
				t,
				before,
				fixture.automation.Snapshot(),
				sender,
			)
		})
	}
}

func TestWorkbenchRecruitingAudienceBindingRejectsRecipientChanges(t *testing.T) {
	content := "Approved scheduling message."
	approved := m304Audience("aud_00000001", content, 2)
	parent := AutomationAction{Recipient: workbenchRecruitingAudienceRecipient(approved)}
	for _, test := range []struct {
		name   string
		mutate func(*AutomationAudience)
		err    error
		code   WorkbenchErrorCode
	}{
		{
			name: "version",
			mutate: func(audience *AutomationAudience) {
				audience.Version++
			},
			err:  errWorkbenchRecruitingAudienceVersionChanged,
			code: WorkbenchErrorConflict,
		},
		{
			name: "peer",
			mutate: func(audience *AutomationAudience) {
				audience.PeerID = "wxid_replacement"
			},
			err:  errWorkbenchRecruitingAudienceRecipientChanged,
			code: WorkbenchErrorForbidden,
		},
		{
			name: "chat_type",
			mutate: func(audience *AutomationAudience) {
				audience.ChatType = ChatTypeGroup
				audience.PeerID = "jobs@chatroom"
			},
			err:  errWorkbenchRecruitingAudienceRecipientChanged,
			code: WorkbenchErrorForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := approved
			test.mutate(&current)
			_, err := workbenchRecruitingApprovedAudienceRecipient(parent, current)
			if !errors.Is(err, test.err) {
				t.Fatalf("binding error = %v, want %v", err, test.err)
			}
			result := workbenchRecruitingFailure(
				WorkbenchOperationMessagesSendAudience,
				err,
			)
			m304AssertFailure(t, result, test.code)
		})
	}
}

func TestWorkbenchRecruitingAudienceRateLimit(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 1)
	sender := &m304Sender{}
	resolver := &m304AudienceResolver{audience: audience}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: fixture.crm, Automation: fixture.automation,
		Audiences: resolver, Sender: sender,
		ForwardRules: NewForwardRuleStore(fixture.crm),
	}).(*defaultWorkbenchToolDispatcher)
	arguments := WorkbenchAudienceSendArguments{
		AudienceID: audience.ID,
		Intent:     AutomationReplyIntentScheduling,
		Text:       content,
	}
	parent := fixture.parent
	parent.Recipient = workbenchRecruitingAudienceRecipient(audience)
	result := workbenchRecruitingSendApprovedAudience(
		context.Background(),
		dispatcher,
		fixture.execution,
		parent,
		&arguments,
	)
	m304AssertSuccess(t, result)

	second := m304ApprovedFixtureInStore(
		t, fixture.crm, fixture.automation, WorkbenchOperationMessagesSendAudience,
	)
	secondParent := second.parent
	secondParent.Recipient = workbenchRecruitingAudienceRecipient(audience)
	result = workbenchRecruitingSendApprovedAudience(
		context.Background(),
		dispatcher,
		second.execution,
		secondParent,
		&arguments,
	)
	if result.OK {
		t.Fatal("audience hourly limit was not enforced")
	}
	if sender.calls.Load() != 1 {
		t.Fatalf("rate-limited sender calls = %d", sender.calls.Load())
	}
}

func TestWorkbenchRecruitingAudienceRateLimitIsAtomicAcrossApprovedActions(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	automation := m304AutomationStore(t)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 1)
	sender := &m304Sender{}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          crm,
		Automation:   automation,
		Audiences:    m304AudienceResolver{audience: audience},
		Sender:       sender,
		ForwardRules: NewForwardRuleStore(crm),
	}).(*defaultWorkbenchToolDispatcher)
	arguments := WorkbenchAudienceSendArguments{
		AudienceID: audience.ID,
		Intent:     AutomationReplyIntentScheduling,
		Text:       content,
	}

	const workers = 12
	fixtures := make([]m304ApprovedState, workers)
	for i := range fixtures {
		fixtures[i] = m304ApprovedFixtureInStore(
			t,
			crm,
			automation,
			WorkbenchOperationMessagesSendAudience,
		)
		fixtures[i].parent.Recipient = workbenchRecruitingAudienceRecipient(audience)
	}

	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, workers)
	var wait sync.WaitGroup
	for i := range fixtures {
		wait.Add(1)
		go func(fixture m304ApprovedState) {
			defer wait.Done()
			<-start
			results <- workbenchRecruitingSendApprovedAudience(
				context.Background(),
				dispatcher,
				fixture.execution,
				fixture.parent,
				&arguments,
			)
		}(fixtures[i])
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result.OK {
			successes++
			continue
		}
		if result.Error == nil || result.Error.Code != WorkbenchErrorInvalidArguments {
			t.Fatalf("atomic rate-limit result = %s", m304ResultSummary(result))
		}
	}
	if successes != 1 || sender.calls.Load() != 1 {
		t.Fatalf("atomic rate limit successes=%d sender calls=%d", successes, sender.calls.Load())
	}
	state := automation.Snapshot()
	bridges := 0
	for _, action := range state.Actions {
		if action.Tool == AutomationToolReplyAudienceText {
			bridges++
		}
	}
	if bridges != 1 || len(state.Outbox) != 1 {
		t.Fatalf("atomic rate limit bridges=%d outbox=%d", bridges, len(state.Outbox))
	}
}

func TestWorkbenchRecruitingAudienceCancellationBeforeBridgeHasNoSideEffects(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 2)
	ctx, cancel := context.WithCancel(context.Background())
	sender := &m304Sender{}
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          fixture.crm,
		Automation:   fixture.automation,
		Audiences:    m304CancellingAudienceResolver{audience: audience, cancel: cancel},
		Sender:       sender,
		ForwardRules: NewForwardRuleStore(fixture.crm),
	}).(*defaultWorkbenchToolDispatcher)
	parent := fixture.parent
	parent.Recipient = workbenchRecruitingAudienceRecipient(audience)
	before := fixture.automation.Snapshot()
	result := workbenchRecruitingSendApprovedAudience(
		ctx,
		dispatcher,
		fixture.execution,
		parent,
		&WorkbenchAudienceSendArguments{
			AudienceID: audience.ID,
			Intent:     AutomationReplyIntentScheduling,
			Text:       content,
		},
	)
	m304AssertFailure(t, result, WorkbenchErrorCancelled)
	m304AssertNoAudienceSideEffects(t, before, fixture.automation.Snapshot(), sender)
}

func TestWorkbenchRecruitingUnavailableAgentSenderIsRetryable(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationMessagesSendAudience)
	content := "Approved scheduling message."
	audience := m304Audience("aud_00000001", content, 2)
	sender := NewAgentAutomationTextSender(
		nil,
		AgentAutomationControlledSendResolverFunc(func(
			context.Context,
			ChatType,
			string,
			string,
		) (AgentControlledSendRequest, error) {
			t.Fatal("authorization resolver should not run without an agent client")
			return AgentControlledSendRequest{}, nil
		}),
	)
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          fixture.crm,
		Automation:   fixture.automation,
		Audiences:    m304AudienceResolver{audience: audience},
		Sender:       sender,
		ForwardRules: NewForwardRuleStore(fixture.crm),
	}).(*defaultWorkbenchToolDispatcher)
	parent := fixture.parent
	parent.Recipient = workbenchRecruitingAudienceRecipient(audience)
	result := workbenchRecruitingSendApprovedAudience(
		context.Background(),
		dispatcher,
		fixture.execution,
		parent,
		&WorkbenchAudienceSendArguments{
			AudienceID: audience.ID,
			Intent:     AutomationReplyIntentScheduling,
			Text:       content,
		},
	)
	m304AssertFailure(t, result, WorkbenchErrorUnavailable)
	if !result.Error.Retryable {
		t.Fatalf("unavailable sender result = %s", m304ResultSummary(result))
	}
	state := fixture.automation.Snapshot()
	bridge, ok := workbenchRecruitingFindBridge(
		state,
		fixture.parent.ID,
		workbenchRecruitingBridgeAudience,
	)
	if !ok || bridge.Status != AutomationActionFailed || !bridge.Retryable ||
		bridge.ErrorCode != "sender_unavailable" {
		t.Fatalf("unavailable sender bridge = %+v", bridge)
	}
	if len(state.Outbox) != 1 ||
		state.Outbox[0].Status != AutomationOutboxFailed ||
		state.Outbox[0].DispatchAttempted {
		t.Fatalf("unavailable sender outbox = %+v", state.Outbox)
	}
}

func TestWorkbenchRecruitingForwardApprovalCooldownAndIdempotency(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationForwardingSendEvent)
	event := m304SeedGroupEvent(t, fixture.crm, "jobs@chatroom", "new job")
	rules := NewForwardRuleStore(fixture.crm)
	rule, err := rules.Create("jobs@chatroom", []string{"job"}, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	sinkPath := filepath.Join(t.TempDir(), "forward.jsonl")
	previousSink := config.Sink
	config.Sink = "file:" + sinkPath
	t.Cleanup(func() { config.Sink = previousSink })
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: fixture.crm, Automation: fixture.automation, ForwardRules: rules,
	})
	arguments := WorkbenchForwardEventArguments{EventID: event.ID, RuleID: rule.ID}

	first := m304Dispatch(
		t, dispatcher, fixture.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationForwardingSendEvent,
		arguments,
	)
	if !first.OK {
		bridge, _ := workbenchRecruitingFindBridge(
			fixture.automation.Snapshot(),
			fixture.parent.ID,
			workbenchRecruitingBridgeForward,
		)
		t.Fatalf(
			"forward result = %s bridge_status=%s bridge_error_code=%q bridge_error=%q",
			m304ResultSummary(first),
			bridge.Status,
			bridge.ErrorCode,
			bridge.ErrorMessage,
		)
	}
	m304AssertSuccess(t, first)
	replayed := m304Dispatch(
		t, dispatcher, fixture.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationForwardingSendEvent,
		arguments,
	)
	m304AssertSuccess(t, replayed)
	data, err := os.ReadFile(sinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := len(m304BytesLines(data)); lines != 1 {
		t.Fatalf("forward lines = %d, want 1", lines)
	}

	second := m304ApprovedFixtureInStore(
		t, fixture.crm, fixture.automation, WorkbenchOperationForwardingSendEvent,
	)
	cooldown := m304Dispatch(
		t, dispatcher, second.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationForwardingSendEvent,
		arguments,
	)
	m304AssertFailure(t, cooldown, WorkbenchErrorConflict)
	data, err = os.ReadFile(sinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := len(m304BytesLines(data)); lines != 1 {
		t.Fatalf("cooldown forward lines = %d, want 1", lines)
	}
}

func TestWorkbenchRecruitingApprovedDeletesReplayWithoutSecondMutation(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		operation WorkbenchOperation
		setup     func(*testing.T, *CRMStore, *ForwardRuleStore) any
	}{
		{
			name:      "source",
			operation: WorkbenchOperationRecruitingSourcesDelete,
			setup: func(t *testing.T, crm *CRMStore, _ *ForwardRuleStore) any {
				project, err := crm.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
					GroupID: "jobs@chatroom", Enabled: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				return WorkbenchRecruitingSourceDeleteArguments{
					GroupID: "jobs@chatroom", ExpectedProjectVersion: project.Version,
				}
			},
		},
		{
			name:      "rule",
			operation: WorkbenchOperationForwardRulesDelete,
			setup: func(t *testing.T, _ *CRMStore, rules *ForwardRuleStore) any {
				rule, err := rules.Create("jobs@chatroom", []string{"job"}, 0, true)
				if err != nil {
					t.Fatal(err)
				}
				return WorkbenchIDArguments{ID: rule.ID}
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			approved := m304ApprovedFixture(t, fixture.operation)
			rules := NewForwardRuleStore(approved.crm)
			arguments := fixture.setup(t, approved.crm, rules)
			dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
				CRM: approved.crm, Automation: approved.automation, ForwardRules: rules,
			})
			first := m304Dispatch(
				t, dispatcher, approved.execution,
				AutomationToolWorkbenchAllowOnce, fixture.operation, arguments,
			)
			m304AssertSuccess(t, first)
			replayed := m304Dispatch(
				t, dispatcher, approved.execution,
				AutomationToolWorkbenchAllowOnce, fixture.operation, arguments,
			)
			m304AssertSuccess(t, replayed)
			if !reflect.DeepEqual(first.Data, replayed.Data) {
				t.Fatalf("delete replay data = %s / %s", first.Data, replayed.Data)
			}
		})
	}
}

func TestWorkbenchRecruitingApprovedDeletePersistenceFailureIsRetryableUnavailable(t *testing.T) {
	fixture := m304ApprovedFixture(t, WorkbenchOperationRecruitingSourcesDelete)
	project, err := fixture.crm.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
		GroupID: "jobs@chatroom",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	injectedErr := errors.New("injected CRM sync failure")
	fixture.crm.syncDirectory = func(string) error { return injectedErr }
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          fixture.crm,
		Automation:   fixture.automation,
		ForwardRules: NewForwardRuleStore(fixture.crm),
	})
	result := m304Dispatch(
		t,
		dispatcher,
		fixture.execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationRecruitingSourcesDelete,
		WorkbenchRecruitingSourceDeleteArguments{
			GroupID:                "jobs@chatroom",
			ExpectedProjectVersion: project.Version,
		},
	)
	m304AssertFailure(t, result, WorkbenchErrorUnavailable)
	if !result.Error.Retryable {
		t.Fatalf("persistence failure result = %s", m304ResultSummary(result))
	}
	if len(fixture.crm.RecruitingProject().SourceGroups) != 1 {
		t.Fatal("persistence failure deleted the recruiting source")
	}
	bridge, ok := workbenchRecruitingFindBridge(
		fixture.automation.Snapshot(),
		fixture.parent.ID,
		workbenchRecruitingBridgeDelete,
	)
	if !ok || bridge.Status != AutomationActionFailed || !bridge.Retryable ||
		bridge.ErrorCode != "dependency_unavailable" {
		t.Fatalf("persistence failure bridge = %+v", bridge)
	}
}

type m304ApprovedState struct {
	crm        *CRMStore
	automation *AutomationStore
	parent     AutomationAction
	execution  WorkbenchExecutionContext
}

func m304ApprovedFixture(
	t *testing.T,
	operation WorkbenchOperation,
) m304ApprovedState {
	return m304ApprovedFixtureInStore(
		t,
		crmMessageOpenTestStore(t),
		m304AutomationStore(t),
		operation,
	)
}

func m304ApprovedFixtureInStore(
	t *testing.T,
	crm *CRMStore,
	automation *AutomationStore,
	operation WorkbenchOperation,
) m304ApprovedState {
	t.Helper()
	arguments := m304OperationArguments(operation)
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchAllowOnce, raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var parent AutomationAction
	var permission AutomationPermissionRequest
	err = automation.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		runID, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		actionID, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		runKey := "run:m304:" + runID
		state.Runs = append(state.Runs, AutomationRun{
			ID: runID, IdempotencyKey: runKey,
			EngineVersion: AutomationEngineVersion, ProviderName: "m304",
			SourceMessageEventID: "event-m304-" + runID,
			SourceMessageKey:     "message-m304-" + runID,
			SourceMessageVersion: 1,
			Status:               AutomationRunRunning, Version: 1, Attempt: 1,
			ActionIDs: []string{actionID},
			CreatedAt: now, StartedAt: m304Time(now),
		})
		state.Idempotency[runKey] = AutomationIdempotencyRecord{
			Kind: "run", ResourceID: runID, CreatedAt: now,
		}
		parent = AutomationAction{
			ID: actionID, RunID: runID, ProviderCallID: "m304-parent-" + actionID,
			Tool: AutomationToolWorkbenchAllowOnce, Operation: operation,
			Status: AutomationActionExecuting, Risk: AutomationRiskR3,
			PolicyDecision: AutomationDecisionReview, PolicyVersion: state.Policy.Version,
			RiskReasons: []string{automationReasonR3WorkbenchAllowOnce},
			Confidence:  1, Evidence: []AutomationEvidence{},
			SourceMessage: AutomationSourceMessageRef{
				EventID: "event-m304-" + runID, MessageKey: "message-m304-" + runID,
				ExpectedVersion: 1, ChatType: ChatTypePrivate, PeerID: "automation_chat",
			},
			Preconditions:      []AutomationEntityPrecondition{},
			DependsOnActionIDs: []string{},
			Payload:            decoded.CanonicalRequest,
			Attempt:            1, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		state.Actions = append(state.Actions, parent)
		permissionID, err := allocator.Next(AutomationIDKindPermissionRequest)
		if err != nil {
			return err
		}
		permission = AutomationPermissionRequest{
			ID: permissionID, RunID: runID, ActionID: actionID,
			Tool: parent.Tool, Operation: operation, Risk: AutomationRiskR3,
			Status: AutomationPermissionStatusAllowed, Version: 1,
			Decision: AutomationPermissionAllowOnce, OperatorID: "operator-m304",
			CreatedAt: now, UpdatedAt: now,
		}
		state.PermissionRequests = append(state.PermissionRequests, permission)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m304ApprovedState{
		crm: crm, automation: automation, parent: parent,
		execution: WorkbenchExecutionContext{
			RunID: parent.RunID, ActionID: parent.ID,
			PermissionRequestID: permission.ID,
			PermissionDecision:  AutomationPermissionAllowOnce,
			OperatorID:          permission.OperatorID,
		},
	}
}

func m304OperationArguments(operation WorkbenchOperation) any {
	switch operation {
	case WorkbenchOperationMessagesSendAudience:
		return WorkbenchAudienceSendArguments{
			AudienceID: "aud_00000001",
			Intent:     AutomationReplyIntentScheduling,
			Text:       "Approved scheduling message.",
		}
	case WorkbenchOperationForwardingSendEvent:
		return WorkbenchForwardEventArguments{
			EventID: "event_00000001", RuleID: "rule_00000001",
		}
	case WorkbenchOperationRecruitingSourcesDelete:
		return WorkbenchRecruitingSourceDeleteArguments{
			GroupID: "jobs@chatroom", ExpectedProjectVersion: 2,
		}
	case WorkbenchOperationForwardRulesDelete:
		return WorkbenchIDArguments{ID: "rule_00000001"}
	default:
		panic("unsupported M3-04 fixture operation")
	}
}

func m304AutomationStore(t *testing.T) *AutomationStore {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func m304Audience(id string, content string, perHour int) AutomationAudience {
	expires := time.Now().UTC().Add(time.Hour)
	normalized := normalizeAutomationAudienceContent(content)
	return AutomationAudience{
		ID: id, Label: "M3-04 audience", ChatType: ChatTypePrivate,
		PeerID: "wxid_m304", AllowedIntents: []string{AutomationReplyIntentScheduling},
		ApprovedContent: normalized, ContentSHA256: automationPromptSHA256(normalized),
		ExpiresAt: &expires, PerHourLimit: perHour, PerRunRecipientCap: 2,
		Enabled: true, Version: 1,
	}
}

func m304SeedGroupEvent(
	t *testing.T,
	crm *CRMStore,
	groupID string,
	text string,
) MessageEvent {
	t.Helper()
	var event MessageEvent
	err := crm.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = MessageEvent{
			ID: id, Source: crmMessageSource,
			Mid: "mid-" + id, ChatType: ChatTypeGroup, PeerID: groupID,
			AuthorID: "wxid_author", GroupID: groupID, Text: text,
			Kinds: []string{"text"}, Timestamp: now.Unix(),
			Disposition:  MessageDispositionGroupNoise,
			ReviewStatus: ReviewStatusUnreviewed, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		event.MessageKey = MessageKeyForEvent(event)
		state.MessageEvents = append(state.MessageEvents, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func m304Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(tool, raw)
	if err != nil {
		t.Fatalf("decode %s: %v", operation, err)
	}
	return dispatcher.Dispatch(context.Background(), execution, decoded)
}

func m304AssertSuccess(t *testing.T, result WorkbenchToolResult) {
	t.Helper()
	if !result.OK || result.Error != nil {
		t.Fatalf("result = %s, want success", m304ResultSummary(result))
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("result envelope bytes = %d", len(encoded))
	}
}

func m304AssertFailure(
	t *testing.T,
	result WorkbenchToolResult,
	code WorkbenchErrorCode,
) {
	t.Helper()
	if result.OK || result.Error == nil || result.Error.Code != code {
		t.Fatalf("result = %s, want failure %s", m304ResultSummary(result), code)
	}
}

func m304AssertNoAudienceSideEffects(
	t *testing.T,
	before AutomationState,
	after AutomationState,
	sender *m304Sender,
) {
	t.Helper()
	if sender.calls.Load() == 0 && reflect.DeepEqual(before, after) {
		return
	}
	t.Fatalf(
		"audience side effects: calls=%d outbox=%d/%d actions=%d/%d permissions=%d/%d audit=%d/%d",
		sender.calls.Load(),
		len(after.Outbox),
		len(before.Outbox),
		len(after.Actions),
		len(before.Actions),
		len(after.PermissionRequests),
		len(before.PermissionRequests),
		len(after.PendingAudit),
		len(before.PendingAudit),
	)
}

func m304ResultSummary(result WorkbenchToolResult) string {
	if result.Error == nil {
		return fmt.Sprintf("operation=%s ok=%t error=<nil>", result.Operation, result.OK)
	}
	return fmt.Sprintf(
		"operation=%s ok=%t error_code=%s message=%q retryable=%t",
		result.Operation,
		result.OK,
		result.Error.Code,
		result.Error.Message,
		result.Error.Retryable,
	)
}

func m304AssertJSONKeys(t *testing.T, data json.RawMessage, keys ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	m304Decode(t, data, &object)
	if len(object) != len(keys) {
		t.Fatalf("JSON keys = %v, want %v", m304SortedKeys(object), keys)
	}
	want := append([]string(nil), keys...)
	m304SortStrings(want)
	got := m304SortedKeys(object)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func m304SortedKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	m304SortStrings(keys)
	return keys
}

func m304SortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func m304Decode(t *testing.T, data []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatal(err)
	}
}

func m304Number(value int) string {
	return fmt.Sprintf("%03d", value)
}

func m304Bool(value bool) *bool {
	return &value
}

func m304Time(value time.Time) *time.Time {
	return &value
}

func m304BytesLines(data []byte) [][]byte {
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return nil
	}
	return lines
}

func TestWorkbenchRecruitingPersistenceFailureDoesNotCommit(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	rules := NewForwardRuleStore(crm)
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM: crm, ForwardRules: rules,
	})
	blocking := filepath.Join(t.TempDir(), "blocking")
	if err := os.WriteFile(blocking, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalPath := crm.path
	crm.path = filepath.Join(blocking, "crm.json")
	result := m304Dispatch(
		t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate,
		WorkbenchOperationForwardRulesCreate,
		WorkbenchForwardRuleCreateArguments{
			GroupID: "jobs@chatroom", Keywords: []string{"job"}, Enabled: true,
		},
	)
	crm.path = originalPath
	m304AssertFailure(t, result, WorkbenchErrorUnavailable)
	if len(rules.List()) != 0 {
		t.Fatal("failed rule create committed state")
	}
}

func TestWorkbenchRecruitingPolicyRejectsUnknownAudienceAndRule(t *testing.T) {
	audienceDecoded := m304Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationMessagesSendAudience,
		WorkbenchAudienceSendArguments{
			AudienceID: "aud_missing",
			Intent:     AutomationReplyIntentScheduling,
			Text:       "approved",
		},
	)
	_, err := workbenchRecruitingAudiencePolicyResolver(
		NewServerAutomationRiskEvaluator(m304AudienceResolver{}),
		AutomationPolicyInput{RunActionCount: 1},
		audienceDecoded,
	)
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("unknown audience policy error = %v", err)
	}

	crm := crmMessageOpenTestStore(t)
	forwardDecoded := m304Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationForwardingSendEvent,
		WorkbenchForwardEventArguments{
			EventID: "event_00000001", RuleID: "rule_00000001",
		},
	)
	evaluator := NewServerAutomationRiskEvaluator()
	evaluator.crm = crm
	_, err = workbenchRecruitingForwardPolicyResolver(
		evaluator,
		AutomationPolicyInput{},
		forwardDecoded,
	)
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("unknown forward policy error = %v", err)
	}
}

func m304Decoded(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchDecodedToolRequest {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(tool, raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
