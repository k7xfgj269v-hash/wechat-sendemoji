package main

import (
	"strings"
	"testing"
)

func TestWebAutomationActionsRegistrationAndAPIContract(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/automation_actions.js")

	for _, marker := range []string{
		`id: "ai-actions"`,
		`label: "AI 工作台"`,
		"order: 65",
		"navigation: false",
		"window.OneBotWorkbench.registerView(view)",
		"Workbench.automationAPI",
		"Workbench.escapeHTML",
		"Workbench.encodeQuery",
		"Workbench.renderStatus",
		"Workbench.requestRender",
		`"/api/automation/actions"`,
		`"/approve"`,
		`"/edit_approve"`,
		`"/reject"`,
		`"/retry"`,
		"expected_version",
		"operator_id",
		"arguments",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("automation_actions.js does not contain contract marker %q", marker)
		}
	}
	if count := strings.Count(js, "registerView(view)"); count != 1 {
		t.Fatalf("automation_actions.js registers %d views, want exactly 1", count)
	}
	for _, forbidden := range []string{
		"fetch(",
		"XMLHttpRequest",
		"sessionStorage",
		"localStorage",
		"Authorization",
		"Workbench.api(",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("automation_actions.js contains forbidden API marker %q", forbidden)
		}
	}
}

func TestWebAutomationActionsPreservesReviewStateAndGuardsStaleRequests(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/automation_actions.js")

	for _, marker := range []string{
		"filters:",
		"selectedId:",
		"detail:",
		"operatorId:",
		"reason:",
		"editDraft:",
		"editDrafts:",
		"listRequestSeq:",
		"detailRequestSeq:",
		"mutationRequestSeq:",
		"requestID !== state.listRequestSeq",
		"requestID !== state.detailRequestSeq",
		"requestID !== state.mutationRequestSeq",
		"authoritativeReload",
		"data-automation-actions-field=\"operatorId\"",
		"data-automation-actions-field=\"reason\"",
		"data-automation-actions-field=\"editDraft\"",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("automation_actions.js does not contain state marker %q", marker)
		}
	}
}

func TestWebAutomationActionsRendersRequiredReviewEvidence(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/automation_actions.js")

	for _, marker := range []string{
		"source_message",
		"source.text",
		"evidence",
		"confidence",
		"risk_reasons",
		"rule_ids",
		"policy_rule_ids",
		"precondition_checks",
		"expected_version",
		"current_version",
		"dependencies",
		"depends_on_action_ids",
		"action.scheduler",
		"scheduler.not_ready_reason",
		"dependency.satisfied",
		"dependency.not_ready_reason",
		"dependency.result_entity",
		"action.result_entity",
		"executionStateLabel",
		"diff",
		"approvals",
		"outbox",
		"audit_events",
		"dispatch_attempted",
		"unknown: true",
		"sent: true",
		"denied: true",
		"stale: true",
		"superseded: true",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("automation_actions.js does not contain review marker %q", marker)
		}
	}
	if strings.Count(js, "escapeHTML(") < 50 {
		t.Fatal("automation_actions.js does not consistently escape rendered values")
	}
	for _, forbidden := range []string{
		"action.dependency_state",
		"action.execution_state",
		"action.blocked_reason",
		"action.ready === true",
		"dependency.blocked_reason",
		"jsonText(action.result)",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("automation_actions.js derives projection state with %q", forbidden)
		}
	}
}

func TestWebAutomationActionsRendersAuthoritativeProjection(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_actions.js",
		`
const views = {};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI() { return Promise.resolve({}); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
`,
		`
const view = views["ai-actions"];
const state = view.createState();
const action = {
  id: "action_1",
  run_id: "run_1",
  provider_call_id: "call_1",
  tool: "job.update",
  status: "succeeded",
  dependency_state: "ready",
  blocked_reason: "legacy blocked reason",
  ready: true,
  scheduler: {
    state: "blocked",
    not_ready_reason: "authoritative scheduler reason"
  },
  dependencies: [{
    action_id: "action_0",
    tool: "candidate.create",
    status: "succeeded",
    satisfied: false,
    not_ready_reason: "authoritative dependency reason",
    result_entity: { entity_type: "candidate", entity_id: "cand_projection" }
  }],
  result: { entity_id: "legacy_result" },
  result_entity: { entity_type: "job", entity_id: "job_projection", version: 3 },
  depends_on_action_ids: ["action_0"],
  payload: {},
  evidence: [],
  risk_reasons: [],
  version: 2
};
state.actions = [action];
state.selectedId = action.id;
state.detail = {
  action,
  source_message: {},
  approvals: [],
  outbox: null,
  audit_events: [],
  dependencies: action.dependencies,
  precondition_checks: [],
  diff: { changes: [] }
};
const html = view.render(state);
for (const expected of [
  "依赖阻塞",
  "authoritative scheduler reason",
  "authoritative dependency reason",
  "cand_projection",
  "job_projection"
]) {
  if (!html.includes(expected)) throw new Error("missing projection output " + expected);
}
for (const forbidden of ["legacy blocked reason", "legacy_result"]) {
  if (html.includes(forbidden)) throw new Error("rendered legacy inferred field " + forbidden);
}
`,
	)
}

func TestWebAutomationActionsScopedResponsiveCSS(t *testing.T) {
	css := readEmbeddedWebAsset(t, "web/automation_actions.css")

	for _, marker := range []string{
		".automationActions {",
		".automationActions .automationActions__workspace",
		"grid-template-columns: minmax(350px, 0.78fr) minmax(460px, 1.22fr)",
		"overflow-wrap: anywhere",
		"@media (max-width: 430px)",
		"grid-template-columns: minmax(0, 1fr)",
		".automationActions .automationActions__actionRow button",
		".automationActions .automationActions__blockedReason",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("automation_actions.css does not contain responsive marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"@import",
		"url(",
		"http://",
		"https://",
		"linear-gradient",
		"radial-gradient",
		"conic-gradient",
		".automationActions__panel .automationActions__panel",
	} {
		if strings.Contains(strings.ToLower(css), strings.ToLower(forbidden)) {
			t.Fatalf("automation_actions.css contains forbidden marker %q", forbidden)
		}
	}
}
