package main

import (
	"strings"
	"testing"
)

func crmInboxReadAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := webAssets.ReadFile("web/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func crmInboxRequireMarkers(t *testing.T, body string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			t.Fatalf("asset does not contain %q", marker)
		}
	}
}

func TestWebCRMInboxModuleContract(t *testing.T) {
	js := crmInboxReadAsset(t, "crm_inbox.js")
	crmInboxRequireMarkers(t, js, []string{
		`window.OneBotWorkbench.registerView(view)`,
		`id: "inbox"`,
		`label: "消息收件箱"`,
		`order: 10`,
		`createState: createState`,
		`render: render`,
		`onEnter: onEnter`,
		`bind: bind`,
		`Workbench.api`,
		`Workbench.escapeHTML`,
		`Workbench.encodeQuery`,
		`Workbench.renderStatus`,
		`Workbench.requestRender`,
	})
}

func TestWebCRMInboxAPIContract(t *testing.T) {
	js := crmInboxReadAsset(t, "crm_inbox.js")
	crmInboxRequireMarkers(t, js, []string{
		`"/api/crm/message_events"`,
		`"/api/crm/message_events/"`,
		`"/api/crm/candidates"`,
		`"/api/crm/message_candidate_links"`,
		`review_status: state.filters.review_status`,
		`chat_type: state.filters.chat_type`,
		`candidate_id: state.filters.candidate_id`,
		`linked: state.filters.linked`,
		`q: state.filters.q`,
		`limit: state.limit`,
		`offset: state.offset`,
		`data && data.ingestion_totals`,
		`data && Array.isArray(data.recent_decisions)`,
		`method: "PATCH"`,
		`expected_version: event.version`,
		`review_status: reviewStatus`,
		`method: "POST"`,
		`message_event_id: event.id`,
		`expected_event_version: event.version`,
		`candidate_id: state.selectedCandidateId`,
		`candidate: {`,
	})
}

func TestWebCRMInboxSafeRenderingAndFormState(t *testing.T) {
	js := crmInboxReadAsset(t, "crm_inbox.js")
	crmInboxRequireMarkers(t, js, []string{
		`escapeHTML(event.text || "无文本内容")`,
		`escapeHTML(event.id)`,
		`escapeHTML(candidate.id)`,
		`escapeHTML(candidate.full_name || "未命名候选人")`,
		`renderKinds(event.kinds)`,
		`event.disposition`,
		`event.job_ingestion`,
		`provenance.content_sha256`,
		`provenance.canonical_fingerprint`,
		`provenance.job_revision`,
		`ingestionDecisionLabel`,
		`append-only provenance`,
		`candidateForm: createCandidateForm()`,
		`candidateQuery: ""`,
		`selectedCandidateId: ""`,
		`linkMode: "existing"`,
		`syncFormValue(state, event.target)`,
		`state.error = err.message`,
		`full_name: form.full_name.trim()`,
		`skills: parseSkills(form.skills)`,
	})

	for _, forbidden := range []string{
		".innerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
		"http://",
		"https://",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("crm_inbox.js contains unsafe rendering marker %q", forbidden)
		}
	}
	for _, nonexistent := range []string{
		"event.ingestion)",
		"event.ingestion_decision",
		"event.job_provenance",
		"event.provenance",
		"item.provenance",
	} {
		if strings.Contains(js, nonexistent) {
			t.Fatalf("crm_inbox.js reads non-projected ingestion field %q", nonexistent)
		}
	}

	if strings.Count(js, `selectedId: ""`) != 1 {
		t.Fatal("selected event state must only be initialized, not reset on async states")
	}
	if strings.Count(js, `candidateForm: createCandidateForm()`) != 1 {
		t.Fatal("candidate form state must only be initialized, not replaced on async states")
	}
}

func TestWebCRMInboxRendersAuthoritativeIngestionProjection(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/crm_inbox.js",
		`
const views = {};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    api() { return Promise.resolve({}); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
`,
		`
const view = views.inbox;
const state = view.createState();
const event = {
  id: "event_projection",
  text: "projected source job",
  chat_type: "group",
  group_id: "source@chatroom",
  author_id: "author_1",
  review_status: "unreviewed",
  disposition: "source_group_job",
  ingestion_decision: "failure",
  job_provenance: { id: "legacy_provenance" },
  job_ingestion: {
    decision: "update",
    provenance: {
      id: "provenance_projection",
      decision: "update",
      job_id: "job_projection",
      job_revision: 2,
      demand_code: "DEMAND-PROJECTION",
      group_id: "source@chatroom",
      content_sha256: "projected_sha",
      canonical_fingerprint: "projected_fingerprint"
    }
  }
};
state.events = [event];
state.total = 1;
state.selectedId = event.id;
state.detail = event;
state.ingestionTotals = { create: 1, update: 4, duplicate: 0, noop: 0, noise: 0, failure: 0 };
state.recentDecisions = [{
  id: "recent_projection",
  decision: "create",
  job_id: "job_recent",
  demand_code: "DEMAND-RECENT",
  group_id: "source@chatroom"
}];
const html = view.render(state);
for (const expected of [
  "更新职位",
  "provenance_projection",
  "job_projection",
  "projected_sha",
  "4</strong>更新职位",
  "DEMAND-RECENT"
]) {
  if (!html.includes(expected)) throw new Error("missing ingestion projection " + expected);
}
for (const forbidden of ["legacy_provenance"]) {
  if (html.includes(forbidden)) throw new Error("rendered legacy ingestion field " + forbidden);
}
`,
	)
}

func TestWebCRMInboxStyleContract(t *testing.T) {
	css := crmInboxReadAsset(t, "crm_inbox.css")
	crmInboxRequireMarkers(t, css, []string{
		`.crmInboxLayout`,
		`.crmInboxIngestionSummary`,
		`.crmInboxIngestionTotals`,
		`.crmInboxIngestion`,
		`grid-template-columns: minmax(380px, 0.9fr) minmax(400px, 1.1fr)`,
		`@media (max-width: 430px)`,
		`overflow-wrap: anywhere`,
		`grid-template-columns: minmax(0, 1fr)`,
	})

	lower := strings.ToLower(css)
	for _, forbidden := range []string{
		"http://",
		"https://",
		"@import",
		"url(",
		"gradient",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("crm_inbox.css contains forbidden external or gradient marker %q", forbidden)
		}
	}

	for _, line := range strings.Split(css, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "{") && strings.HasPrefix(line, ".") && !strings.HasPrefix(line, ".crmInbox") {
			t.Fatalf("CSS selector does not use crmInbox prefix: %s", line)
		}
	}
}
