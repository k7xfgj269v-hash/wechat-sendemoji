package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitializeCRMRuntimeUsesDefaults(t *testing.T) {
	oldMessageStore := crmMessageCurrentStore()
	oldForwardRuleStore := forwardRuleStore
	t.Cleanup(func() {
		crmMessageSetStore(oldMessageStore)
		forwardRuleStore = oldForwardRuleStore
	})

	t.Setenv("HOME", t.TempDir())
	cfg := &Config{}
	store, err := initializeCRMRuntime(cfg)
	if err != nil {
		t.Fatalf("initialize CRM runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	wantPath, err := DefaultCRMDataPath()
	if err != nil {
		t.Fatalf("resolve default CRM data path: %v", err)
	}
	if cfg.CRMDataPath != wantPath {
		t.Fatalf("CRM data path = %q, want %q", cfg.CRMDataPath, wantPath)
	}
	if cfg.ReceiveHost != defaultReceiveHost {
		t.Fatalf("default receive host = %q, want %q", cfg.ReceiveHost, defaultReceiveHost)
	}
	dirInfo, err := os.Stat(filepath.Dir(wantPath))
	if err != nil {
		t.Fatalf("stat CRM directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("CRM directory mode = %o, want 700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat CRM snapshot: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("CRM snapshot mode = %o, want 600", fileInfo.Mode().Perm())
	}
}

func TestCRMEndToEndPersistence(t *testing.T) {
	oldMessageStore := crmMessageCurrentStore()
	oldForwardRuleStore := forwardRuleStore
	var activeStore *CRMStore
	t.Cleanup(func() {
		if activeStore != nil {
			_ = activeStore.Close()
		}
		crmMessageSetStore(oldMessageStore)
		forwardRuleStore = oldForwardRuleStore
	})

	path := filepath.Join(t.TempDir(), "crm.json")
	cfg := &Config{CRMDataPath: path}
	store, err := initializeCRMRuntime(cfg)
	if err != nil {
		t.Fatalf("initialize CRM runtime: %v", err)
	}
	activeStore = store
	if cfg.ReceiveHost != defaultReceiveHost {
		t.Fatalf("default receive host = %q, want %q", cfg.ReceiveHost, defaultReceiveHost)
	}

	event, created, err := crmMessageCaptureNormalizedInboundToStore(store, &ForwardRecord{
		T:      1784635200,
		Chat:   string(ChatTypePrivate),
		Dir:    "in",
		Peer:   "wxid_candidate",
		Author: "wxid_candidate",
		Text:   "Interested in the backend role",
		Kinds:  []string{"text"},
		Mid:    "mid-integration-1",
	}, time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC))
	if err != nil || !created {
		t.Fatalf("capture inbound event: created=%v err=%v", created, err)
	}

	link, err := crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
		MessageEventID:       event.ID,
		ExpectedEventVersion: event.Version,
		Candidate: &CandidateDraft{
			FullName: "Lin Chen",
			Skills:   []string{"Go", "Linux"},
		},
	})
	if err != nil {
		t.Fatalf("create candidate from message: %v", err)
	}

	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:        "Backend Engineer",
		Company:      "Example",
		Requirements: []string{"Go"},
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: link.Candidate.ID,
		JobID:       job.ID,
	})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
		ExpectedVersion: lead.Version,
		FromStage:       LeadStageSourced,
		ToStage:         LeadStageScreening,
	})
	if err != nil {
		t.Fatalf("transition lead: %v", err)
	}
	owner := "recruiter-a"
	lead, err = crmPipelineUpdateLead(store, lead.ID, LeadUpdate{
		ExpectedVersion: lead.Version,
		Owner:           &owner,
	})
	if err != nil {
		t.Fatalf("assign lead owner: %v", err)
	}

	activity := crmActivityNewService(store)
	note, err := activity.CreateNote(NoteCreate{
		TargetType: NoteTargetLead,
		TargetID:   lead.ID,
		Author:     owner,
		Body:       "Schedule technical screening",
	})
	if err != nil {
		t.Fatalf("create lead note: %v", err)
	}
	task, err := activity.CreateTask(TaskCreate{
		Title:  "Arrange screening",
		Owner:  owner,
		DueAt:  time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
		LeadID: lead.ID,
	})
	if err != nil {
		t.Fatalf("create follow-up task: %v", err)
	}
	rule, err := forwardRuleStore.Create("recruiting@chatroom", []string{"backend"}, 30, true)
	if err != nil {
		t.Fatalf("create forwarding rule: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	activeStore = nil

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	activeStore = reopened
	crmMessageSetStore(reopened)
	forwardRuleStore = NewForwardRuleStore(reopened)

	state := reopened.Snapshot()
	if len(state.MessageEvents) != 1 || state.MessageEvents[0].ID != event.ID ||
		state.MessageEvents[0].CandidateID != link.Candidate.ID {
		t.Fatalf("message event linkage after restart = %+v", state.MessageEvents)
	}
	if len(state.Candidates) != 1 || state.Candidates[0].ID != link.Candidate.ID ||
		state.Candidates[0].SourceEventID != event.ID {
		t.Fatalf("candidate after restart = %+v", state.Candidates)
	}
	if len(state.Jobs) != 1 || state.Jobs[0].ID != job.ID {
		t.Fatalf("job after restart = %+v", state.Jobs)
	}
	if len(state.Leads) != 1 || state.Leads[0].ID != lead.ID ||
		state.Leads[0].Stage != LeadStageScreening || state.Leads[0].Owner != owner ||
		state.Leads[0].CandidateID != link.Candidate.ID || state.Leads[0].JobID != job.ID {
		t.Fatalf("lead after restart = %+v", state.Leads)
	}
	if len(state.Notes) != 1 || state.Notes[0].ID != note.ID || state.Notes[0].TargetID != lead.ID {
		t.Fatalf("note after restart = %+v", state.Notes)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].ID != task.ID ||
		state.Tasks[0].LeadID != lead.ID || state.Tasks[0].Owner != owner {
		t.Fatalf("task after restart = %+v", state.Tasks)
	}
	rules := forwardRuleStore.List()
	if len(rules) != 1 || rules[0].ID != rule.ID || rules[0].GroupID != rule.GroupID {
		t.Fatalf("forwarding rules after restart = %+v", rules)
	}
}

func TestRegisterHTTPRoutesIncludesCRMAndLegacyEndpoints(t *testing.T) {
	oldMessageStore := crmMessageCurrentStore()
	t.Cleanup(func() {
		crmMessageSetStore(oldMessageStore)
	})

	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	mux := http.NewServeMux()
	registerHTTPRoutes(mux, store, NewForwardRuleStore(store))

	routes := []struct {
		path    string
		pattern string
	}{
		{"/", "/"},
		{"/app.css", "/app.css"},
		{"/app.js", "/app.js"},
		{"/workbench/modules.json", "/workbench/modules.json"},
		{"/assets/candidates.js", "/assets/"},
		{"/send_private_msg", "/send_private_msg"},
		{"/send_group_msg", "/send_group_msg"},
		{"/search_contact", "/search_contact"},
		{"/ws", "/ws"},
		{"/test_ws", "/test_ws"},
		{"/api/contacts", "/api/contacts"},
		{"/api/groups", "/api/groups"},
		{"/api/group_members", "/api/group_members"},
		{"/api/contact_detail", "/api/contact_detail"},
		{"/api/contacts/refresh", "/api/contacts/refresh"},
		{"/api/forward_rules", "/api/forward_rules"},
		{"/api/crm/message_events", "/api/crm/message_events"},
		{"/api/crm/message_events/event_00000001", "/api/crm/message_events/"},
		{"/api/crm/candidates", "/api/crm/candidates"},
		{"/api/crm/candidates/cand_00000001", "/api/crm/candidates/"},
		{"/api/crm/message_candidate_links", "/api/crm/message_candidate_links"},
		{"/api/crm/jobs", "/api/crm/jobs"},
		{"/api/crm/jobs/job_00000001", "/api/crm/jobs/"},
		{crmJobIngestionPath, crmJobIngestionPath},
		{crmJobProvenancePath, crmJobProvenancePath},
		{"/api/crm/leads", "/api/crm/leads"},
		{"/api/crm/leads/lead_00000001", "/api/crm/leads/"},
		{"/api/crm/pipeline", "/api/crm/pipeline"},
		{"/api/crm/notes", "/api/crm/notes"},
		{"/api/crm/tasks", "/api/crm/tasks"},
		{"/api/crm/tasks/task_00000001", "/api/crm/tasks/"},
	}
	for _, route := range routes {
		request, err := http.NewRequest(http.MethodGet, route.path, nil)
		if err != nil {
			t.Fatalf("new request for %s: %v", route.path, err)
		}
		_, pattern := mux.Handler(request)
		if pattern != route.pattern {
			t.Errorf("route %s matched %q, want %q", route.path, pattern, route.pattern)
		}
	}
}

func TestRegisterHRWorkbenchHTTPRoutesIncludesJobIngestionEndpoints(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	mux := http.NewServeMux()
	registerHRWorkbenchHTTPRoutes(
		mux,
		store,
		NewForwardRuleStore(store),
		nil,
		nil,
		nil,
	)

	for _, path := range []string{crmJobIngestionPath, crmJobProvenancePath} {
		request, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("new request for %s: %v", path, err)
		}
		_, pattern := mux.Handler(request)
		if pattern != path {
			t.Errorf("route %s matched %q, want %q", path, pattern, path)
		}
	}
}
