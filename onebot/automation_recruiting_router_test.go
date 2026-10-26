package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestRecruitingAutomationRouterRoutesPersistedConversations(t *testing.T) {
	store := automationRecruitingOpenTestStore(t)
	project := automationRecruitingConfigureProject(t, store)
	router := NewRecruitingAutomationRouter(store)

	tests := []struct {
		name        string
		event       MessageEvent
		kind        string
		disposition MessageDisposition
		sourceID    string
		tools       []AutomationToolName
	}{
		{
			name:        "enabled source group",
			event:       automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false),
			kind:        automationConversationWeChatSourceGroup,
			disposition: MessageDispositionSourceGroupJob,
			sourceID:    project.SourceGroups[0].ID,
			tools: []AutomationToolName{
				AutomationToolMessageClassify,
				automationRecruitingToolJobCreate,
			},
		},
		{
			name:        "disabled source group",
			event:       automationRecruitingTestEvent(ChatTypeGroup, "disabled_jobs@chatroom", false),
			kind:        automationConversationWeChatGroupNoise,
			disposition: MessageDispositionGroupNoise,
			tools:       []AutomationToolName{AutomationToolMessageClassify},
		},
		{
			name:        "unrelated suspicious group",
			event:       automationRecruitingTestEvent(ChatTypeGroup, "unrelated@chatroom", true),
			kind:        automationConversationWeChatGroupNoise,
			disposition: MessageDispositionGroupNoise,
			tools:       []AutomationToolName{AutomationToolMessageClassify},
		},
		{
			name:        "fictional private chat",
			event:       automationRecruitingTestEvent(ChatTypePrivate, "fictional_candidate", false),
			kind:        automationConversationWeChatPrivate,
			disposition: MessageDispositionPrivateCandidate,
			tools: []AutomationToolName{
				AutomationToolMessageClassify,
				AutomationToolCandidateCreate,
				AutomationToolCandidateUpdate,
				AutomationToolCandidateLink,
				AutomationToolJobMatch,
				AutomationToolLeadCreate,
				AutomationToolLeadTransition,
				AutomationToolOnboardingCreate,
				AutomationToolOnboardingUpdate,
				AutomationToolFleetUpdate,
				AutomationToolServiceCreate,
				AutomationToolServiceUpdate,
				AutomationToolFundCreate,
				AutomationToolTaskCreate,
				AutomationToolNoteCreate,
				AutomationToolReplyPrivateText,
			},
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := test.event
			event.Mid = fmt.Sprintf("synthetic-mid-%d", i+1)
			event.MessageKey = MessageKeyForEvent(event)
			persisted, created, err := crmMessagePersistEventToStore(store, event)
			if err != nil || !created {
				t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
			}
			beforeRoute, ok := automationFindMessageEvent(store.Snapshot(), persisted.ID)
			if !ok {
				t.Fatalf("persisted event %q is missing", persisted.ID)
			}

			route, err := router.Route(persisted)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if route.Disposition != test.disposition ||
				route.ConversationKind != test.kind ||
				route.Event.Disposition != test.disposition ||
				route.OrganizationName != "Synthetic Recruiting Org" ||
				!route.RequireAuthoritativeJob {
				t.Fatalf("route = %#v", route)
			}
			assertRecruitingAutomationTools(t, route.AllowedTools, test.tools)
			if test.sourceID == "" {
				if route.SourceGroup != nil {
					t.Fatalf("unexpected source group = %#v", route.SourceGroup)
				}
			} else if route.SourceGroup == nil ||
				route.SourceGroup.ID != test.sourceID ||
				route.SourceGroup.GroupID != event.GroupID {
				t.Fatalf("source group = %#v", route.SourceGroup)
			}

			stored, ok := automationFindMessageEvent(store.Snapshot(), persisted.ID)
			expectedVersion := beforeRoute.Version + 1
			if !ok ||
				stored.Disposition != test.disposition ||
				stored.Version != expectedVersion ||
				stored.RecruitingRouteSourceVersion != beforeRoute.Version ||
				stored.RecruitingProjectVersion == 0 ||
				stored.RecruitingConversationKind != test.kind ||
				stored.RecruitingOrganizationName != "Synthetic Recruiting Org" {
				t.Fatalf("persisted routed event = %#v", stored)
			}
			assertRecruitingAutomationTools(t, stored.RecruitingAllowedTools, test.tools)
			auditCount := len(store.Snapshot().AuditEvents)
			replayed, err := router.Route(persisted)
			if err != nil {
				t.Fatalf("Route(replay) error = %v", err)
			}
			replayedStored, ok := automationFindMessageEvent(store.Snapshot(), persisted.ID)
			if !ok ||
				replayed.Event.Version != stored.Version ||
				replayedStored.Version != stored.Version {
				t.Fatalf("replayed route changed message version: %#v", replayed.Event)
			}
			if replayed.ConversationKind != test.kind {
				t.Fatalf("replayed conversation kind = %q, want %q", replayed.ConversationKind, test.kind)
			}
			assertRecruitingAutomationTools(t, replayed.AllowedTools, test.tools)
			if got := len(store.Snapshot().AuditEvents); got != auditCount {
				t.Fatalf("replayed route added audit records: got %d, want %d", got, auditCount)
			}
		})
	}
}

func TestRecruitingAutomationRouterFreezesRouteAcrossProjectChanges(t *testing.T) {
	store := automationRecruitingOpenTestStore(t)
	project := automationRecruitingConfigureProject(t, store)
	router := NewRecruitingAutomationRouter(store)
	event := automationRecruitingTestEvent(
		ChatTypeGroup,
		"synthetic_jobs@chatroom",
		false,
	)
	event.Mid = "frozen-route"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(store, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	original, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}
	originalVersion := original.Event.Version
	changedName := "Changed Recruiting Org"
	requireAuthoritative := false
	project, err = store.UpdateRecruitingProject(project.Version, RecruitingProjectUpdate{
		OrganizationName:        &changedName,
		RequireAuthoritativeJob: &requireAuthoritative,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := project.SourceGroups[0]
	project, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:          source.ID,
		GroupID:     source.GroupID,
		DisplayName: "Disabled Later",
		Enabled:     false,
	})
	if err != nil {
		t.Fatal(err)
	}

	replayed, err := router.Route(original.Event)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Event.Version != originalVersion ||
		replayed.ConversationKind != automationConversationWeChatSourceGroup ||
		replayed.OrganizationName != "Synthetic Recruiting Org" ||
		!replayed.RequireAuthoritativeJob ||
		replayed.SourceGroup == nil ||
		replayed.SourceGroup.ID != original.SourceGroup.ID ||
		replayed.SourceGroup.DisplayName != original.SourceGroup.DisplayName {
		t.Fatalf("route drifted after project changes: original=%#v replayed=%#v", original, replayed)
	}
	assertRecruitingAutomationTools(t, replayed.AllowedTools, original.AllowedTools)

	input, err := BuildAutomationProviderInput(
		"airun_00000001",
		replayed.Event,
		store.Snapshot(),
		automationAllTools(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.Recruiting.ConversationKind != automationConversationWeChatSourceGroup ||
		input.Recruiting.OrganizationName != original.OrganizationName ||
		!input.Recruiting.RequireAuthoritativeJob ||
		input.Recruiting.SourceGroup == nil ||
		input.Recruiting.SourceGroup.ID != original.SourceGroup.ID {
		t.Fatalf("provider route drifted after recovery: %#v", input.Recruiting)
	}
	assertRecruitingAutomationTools(t, input.AllowedTools, original.AllowedTools)
}

func TestRecruitingAutomationRouterSnapshotIsDeepCopied(t *testing.T) {
	store := automationRecruitingOpenTestStore(t)
	automationRecruitingConfigureProject(t, store)
	router := NewRecruitingAutomationRouter(store)
	event := automationRecruitingTestEvent(ChatTypePrivate, "fictional_candidate", false)
	event.Mid = "route-snapshot-copy"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(store, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	route, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := append([]AutomationToolName(nil), route.AllowedTools...)
	route.AllowedTools[0] = AutomationToolForwardEvent
	route.Event.RecruitingAllowedTools[0] = AutomationToolReplyAudienceText

	snapshot := store.Snapshot()
	snapshot.MessageEvents[0].RecruitingAllowedTools[0] = AutomationToolForwardEvent
	stored := store.Snapshot().MessageEvents[0]
	assertRecruitingAutomationTools(t, stored.RecruitingAllowedTools, wantTools)

	replayed, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}
	assertRecruitingAutomationTools(t, replayed.AllowedTools, wantTools)
	assertRecruitingAutomationTools(t, replayed.Event.RecruitingAllowedTools, wantTools)
}

func TestRecruitingAutomationRouterConcurrentFirstRouteFreezesOnce(t *testing.T) {
	store := automationRecruitingOpenTestStore(t)
	automationRecruitingConfigureProject(t, store)
	router := NewRecruitingAutomationRouter(store)
	event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
	event.Mid = "concurrent-first-route"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(store, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}
	before := store.Snapshot()

	const workers = 32
	routes := make(chan RecruitingAutomationRoute, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			route, err := router.Route(persisted)
			if err != nil {
				errs <- err
				return
			}
			routes <- route
		}()
	}
	for i := 0; i < workers; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent Route() error = %v", err)
		case route := <-routes:
			if route.Event.Version != persisted.Version+1 ||
				route.Event.RecruitingRouteSourceVersion != persisted.Version ||
				route.ConversationKind != automationConversationWeChatSourceGroup {
				t.Fatalf("concurrent route = %#v", route)
			}
		}
	}

	after := store.Snapshot()
	stored, ok := automationFindMessageEvent(after, persisted.ID)
	if !ok ||
		stored.Version != persisted.Version+1 ||
		stored.RecruitingRouteSourceVersion != persisted.Version ||
		len(after.AuditEvents) != len(before.AuditEvents)+1 {
		t.Fatalf("concurrent freeze state = %#v, audits before=%d after=%d", stored, len(before.AuditEvents), len(after.AuditEvents))
	}
}

func TestRecruitingAutomationRouterRejectsUnpersistedEvent(t *testing.T) {
	router := NewRecruitingAutomationRouter(automationRecruitingOpenTestStore(t))
	event := automationRecruitingTestEvent(ChatTypePrivate, "fictional_candidate", false)
	event.ID = "event_00000099"
	event.MessageKey = MessageKeyForEvent(event)

	if _, err := router.Route(event); err == nil {
		t.Fatal("Route() accepted an unpersisted event")
	}
}

func TestRecruitingAutomationRunEngineRoutesBeforeRunCreation(t *testing.T) {
	crm := automationRecruitingOpenTestStore(t)
	automationRecruitingConfigureProject(t, crm)
	event := automationRecruitingTestEvent(
		ChatTypeGroup,
		"synthetic_jobs@chatroom",
		false,
	)
	event.Mid = "runtime-route"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	var captured MessageEvent
	delegate := &automationRecruitingTestRunEngine{
		ensureRun: func(
			_ context.Context,
			event MessageEvent,
			providerName string,
		) (AutomationRun, bool, error) {
			captured = event
			if providerName != "synthetic-provider" {
				t.Fatalf("provider name = %q", providerName)
			}
			return AutomationRun{
				ID:                   "airun_00000001",
				SourceMessageEventID: event.ID,
				SourceMessageVersion: event.Version,
				Status:               AutomationRunQueued,
			}, true, nil
		},
	}
	engine := newRecruitingAutomationRunEngine(
		NewRecruitingAutomationRouter(crm),
		delegate,
	)
	run, created, err := engine.EnsureRun(
		context.Background(),
		persisted,
		"synthetic-provider",
	)
	if err != nil || !created {
		t.Fatalf("EnsureRun() run=%#v, created=%t, err=%v", run, created, err)
	}
	if captured.Disposition != MessageDispositionSourceGroupJob ||
		captured.Version != persisted.Version+1 ||
		captured.RecruitingProjectVersion == 0 ||
		run.SourceMessageVersion != captured.Version {
		t.Fatalf("delegate received unrouted event: %#v, run=%#v", captured, run)
	}
	stored, ok := automationFindMessageEvent(crm.Snapshot(), persisted.ID)
	if !ok ||
		stored.Disposition != MessageDispositionSourceGroupJob ||
		stored.Version != captured.Version {
		t.Fatalf("stored routed event = %#v", stored)
	}
}

func TestRecruitingAutomationRunEnginePreservesDefaultChatEngine(t *testing.T) {
	crm := automationRecruitingOpenTestStore(t)
	router := NewRecruitingAutomationRouter(crm)
	delegate := &defaultAutomationRunEngine{}

	engine := newRecruitingAutomationRunEngine(router, delegate)
	if engine != delegate || delegate.recruitingRouter != router {
		t.Fatalf("default engine was hidden behind a wrapper: engine=%T delegate=%#v", engine, delegate)
	}
}

type automationRecruitingTestRunEngine struct {
	ensureRun func(context.Context, MessageEvent, string) (AutomationRun, bool, error)
}

func (e *automationRecruitingTestRunEngine) EnsureRun(
	ctx context.Context,
	event MessageEvent,
	providerName string,
) (AutomationRun, bool, error) {
	return e.ensureRun(ctx, event, providerName)
}

func (*automationRecruitingTestRunEngine) RetryRun(
	context.Context,
	string,
	uint64,
	string,
	string,
) (AutomationRun, error) {
	return AutomationRun{}, nil
}

func (*automationRecruitingTestRunEngine) ProcessQueued(context.Context) error {
	return nil
}

func (*automationRecruitingTestRunEngine) ProcessRun(context.Context, string) error {
	return nil
}

func (*automationRecruitingTestRunEngine) RecoverScheduledActions(context.Context) error {
	return nil
}

func automationRecruitingOpenTestStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func automationRecruitingConfigureProject(
	t *testing.T,
	store *CRMStore,
) RecruitingProjectConfig {
	t.Helper()
	project := store.RecruitingProject()
	organization := "Synthetic Recruiting Org"
	project, err := store.UpdateRecruitingProject(project.Version, RecruitingProjectUpdate{
		OrganizationName: &organization,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.UpsertRecruitingSourceGroup(
		project.Version,
		RecruitingSourceGroupUpsert{
			GroupID:     "synthetic_jobs@chatroom",
			DisplayName: "Synthetic Job Orders",
			Enabled:     true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.UpsertRecruitingSourceGroup(
		project.Version,
		RecruitingSourceGroupUpsert{
			GroupID:     "disabled_jobs@chatroom",
			DisplayName: "Disabled Synthetic Orders",
			Enabled:     false,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func automationRecruitingTestEvent(
	chatType ChatType,
	peerID string,
	suspicious bool,
) MessageEvent {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	event := MessageEvent{
		Source:       crmMessageSource,
		Mid:          "synthetic-mid",
		ChatType:     chatType,
		PeerID:       peerID,
		AuthorID:     "fictional_author",
		Text:         "synthetic recruiting message",
		Kinds:        []string{"text"},
		Timestamp:    now.Unix(),
		Suspicious:   suspicious,
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if chatType == ChatTypeGroup {
		event.GroupID = peerID
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event
}

func assertRecruitingAutomationTools(
	t *testing.T,
	got []AutomationToolName,
	want []AutomationToolName,
) {
	t.Helper()
	normalizedGot, err := normalizeAutomationProviderTools(got)
	if err != nil {
		t.Fatalf("normalize got tools: %v", err)
	}
	normalizedWant, err := normalizeAutomationProviderTools(want)
	if err != nil {
		t.Fatalf("normalize want tools: %v", err)
	}
	if fmt.Sprint(normalizedGot) != fmt.Sprint(normalizedWant) {
		t.Fatalf("tools = %v, want %v", normalizedGot, normalizedWant)
	}
}
