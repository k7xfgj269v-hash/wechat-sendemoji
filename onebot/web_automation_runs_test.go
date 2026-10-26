package main

import (
	"strings"
	"testing"
)

func TestWebAutomationRunsRegistrationAndAPIContract(t *testing.T) {
	script := automationRunsReadAsset(t, "web/automation_runs.js")

	automationRunsRequireMarkers(t, script, []string{
		"var Workbench = window.OneBotWorkbench",
		`id: "automation-runs"`,
		`label: "运行历史"`,
		"order: 90",
		"createState: automationRunsCreateState",
		"render: automationRunsRender",
		"onEnter: automationRunsOnEnter",
		"bind: automationRunsBind",
		`var AUTOMATION_RUNS_PATH = "/api/automation/runs"`,
		"Workbench.automationAPI(AUTOMATION_RUNS_PATH + Workbench.encodeQuery({",
		"Workbench.automationAPI(AUTOMATION_RUNS_PATH + \"/\" + encodeURIComponent(runID))",
		"status: state.filters.status",
		"source_message_event_id: state.filters.sourceMessageEventID",
		"limit: state.limit",
		"offset: state.offset",
		"data && data.total",
		"data && data.limit",
		"data && data.offset",
		"data && Array.isArray(data.runs)",
		"data && data.run",
		"data && data.source_message",
		"data && Array.isArray(data.actions)",
		"data && data.action_summary",
		"data && Array.isArray(data.audit_events)",
	})

	for _, forbidden := range []string{
		"fetch(",
		"XMLHttpRequest",
		"sessionStorage",
		"localStorage",
		"Authorization",
		"Workbench.api(",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("automation_runs.js contains forbidden API marker %q", forbidden)
		}
	}
	if strings.Count(script, `id: "automation-runs"`) != 1 {
		t.Fatal("automation_runs.js must register exactly one automation-runs view")
	}
}

func TestWebAutomationRunsEscapingAndOutcomeContract(t *testing.T) {
	script := automationRunsReadAsset(t, "web/automation_runs.js")

	automationRunsRequireMarkers(t, script, []string{
		"Workbench.escapeHTML(run && run.id",
		"Workbench.escapeHTML(run && run.provider_name",
		"Workbench.escapeHTML(message.text",
		"Workbench.escapeHTML(action && action.tool",
		"Workbench.escapeHTML(action.error_message",
		"Workbench.escapeHTML(automationRunsFormatJSON(action && action.payload))",
		"Workbench.escapeHTML(automationRunsFormatJSON(action && action.result_entity))",
		"Workbench.escapeHTML(event && event.event_type",
		"Workbench.escapeHTML(automationRunsFormatJSON(event && event.data))",
		"depends_on_action_ids",
		"action.scheduler",
		"scheduler.not_ready_reason",
		"AUTOMATION_ACTION_SUMMARY_STATES",
		"automationRunsDependencyLabel",
	})

	for _, invented := range []string{
		"automationRunsResultObject",
		"automationRunsExternalOutcome",
		"automationRunsCountActions",
		"action.dependency_state",
		"action.execution_state",
		"action.blocked_reason",
		"action.ready === true",
		"action && action.result)",
	} {
		if strings.Contains(script, invented) {
			t.Fatalf("automation_runs.js derives backend projection with %q", invented)
		}
	}
}

func TestWebAutomationRunsUsesSchedulerAndServerCounts(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_runs.js",
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
const view = views["automation-runs"];
const state = view.createState();
state.selectedRunID = "run_1";
state.detailEnvelope = {
  run: { id: "run_1", status: "succeeded", provider_name: "provider" },
  source_message: {},
  action_summary: {
    total: 9,
    ready: 0,
    pending: 0,
    awaiting_review: 0,
    blocked: 7,
    executing: 0,
    succeeded: 2,
    failed: 0
  },
  actions: [{
    id: "action_1",
    tool: "reply.private_text",
    status: "succeeded",
    dependency_state: "ready",
    blocked_reason: "legacy blocked reason",
    scheduler: {
      state: "blocked",
      not_ready_reason: "authoritative run scheduler reason"
    },
    result: { delivery_status: "sent", entity_id: "legacy_result" },
    result_entity: { entity_type: "message", entity_id: "message_projection" },
    payload: {},
    depends_on_action_ids: []
  }],
  audit_events: []
};
const html = view.render(state);
for (const expected of [
  "7</strong> 依赖阻塞",
  "authoritative run scheduler reason",
  "message_projection"
]) {
  if (!html.includes(expected)) throw new Error("missing server projection " + expected);
}
for (const forbidden of ["legacy blocked reason", "legacy_result"]) {
  if (html.includes(forbidden)) throw new Error("rendered client-derived field " + forbidden);
}
`,
	)
}

func TestWebAutomationRunsStableStateAndRequestGuards(t *testing.T) {
	script := automationRunsReadAsset(t, "web/automation_runs.js")

	automationRunsRequireMarkers(t, script, []string{
		`selectedRunID: ""`,
		"detailEnvelope: null",
		"listRequestSequence: 0",
		"detailRequestSequence: 0",
		"var requestID = state.listRequestSequence + 1",
		"requestID !== state.listRequestSequence",
		"var requestID = state.detailRequestSequence + 1",
		"requestID !== state.detailRequestSequence || state.selectedRunID !== runID",
		"requestID === state.detailRequestSequence && state.selectedRunID === runID",
		"state.selectedRunID = runID",
		"automationRunsSelectionAbsentFromPage",
		"所选运行不在当前页，详情选择已保留。",
		"当前保留上次成功加载的详情。",
		"automationRunsOrderedAudit",
		"created_at",
		"localeCompare",
	})

	if strings.Count(script, `selectedRunID: ""`) != 1 {
		t.Fatal("selected run state must only be initialized once")
	}
	for _, destructiveFailureMarker := range []string{
		"state.runs = []",
		"state.detailEnvelope = null",
		`state.selectedRunID = ""`,
	} {
		if strings.Contains(script, destructiveFailureMarker) {
			t.Fatalf("automation_runs.js clears preserved state with %q", destructiveFailureMarker)
		}
	}
}

func TestWebAutomationRunsScopedResponsiveCSS(t *testing.T) {
	style := automationRunsReadAsset(t, "web/automation_runs.css")

	automationRunsRequireMarkers(t, style, []string{
		".automationRuns {",
		".automationRuns .automationRuns-filterBar",
		".automationRuns .automationRuns-layout",
		"grid-template-columns: minmax(330px, 0.72fr) minmax(470px, 1.28fr)",
		".automationRuns .automationRuns-detailPane",
		".automationRuns .automationRuns-counts",
		".automationRuns .automationRuns-actionDependencies",
		".automationRuns .automationRuns-tone-blocked",
		"overflow-wrap: anywhere",
		"@media (max-width: 430px)",
		"grid-template-columns: minmax(0, 1fr)",
		"min-height: 36px",
	})

	lower := strings.ToLower(style)
	for _, forbidden := range []string{
		"http://",
		"https://",
		"//cdn",
		"cdn.",
		"@import",
		"url(",
		"linear-gradient",
		"radial-gradient",
		"conic-gradient",
		"gradient(",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("automation_runs.css contains forbidden marker %q", forbidden)
		}
	}

	for _, line := range strings.Split(style, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "{") && strings.HasPrefix(line, ".") && !strings.HasPrefix(line, ".automationRuns") {
			t.Fatalf("automation_runs.css selector is not scoped: %s", line)
		}
	}
}

func automationRunsReadAsset(t *testing.T, name string) string {
	t.Helper()
	body, err := webAssets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func automationRunsRequireMarkers(t *testing.T, body string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			t.Fatalf("asset does not contain %q", marker)
		}
	}
}
