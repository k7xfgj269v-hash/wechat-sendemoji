package main

import (
	"strings"
	"testing"
)

func TestWebCRMJobsRegistrationAndWorkbenchContract(t *testing.T) {
	script := crmJobsTestAsset(t, "web/crm_jobs.js")
	for _, marker := range []string{
		"window.OneBotWorkbench",
		"crmJobsWorkbench.registerView({",
		`id: "jobs"`,
		`label: "职位"`,
		"order: 30",
		"createState: crmJobsCreateState",
		"render: crmJobsRender",
		"onEnter:",
		"bind: crmJobsBind",
		"crmJobsWorkbench.api(",
		"crmJobsWorkbench.escapeHTML",
		"crmJobsWorkbench.encodeQuery",
		"crmJobsWorkbench.renderStatus",
		"crmJobsWorkbench.requestRender",
	} {
		crmJobsTestContains(t, script, marker)
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "document.querySelector"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("crm_jobs.js bypasses the shared workbench API with %q", forbidden)
		}
	}
	for _, nonexistent := range []string{
		"item.ingestion_decision",
		"detail.source_authority",
		"detail.authoritative_source",
		"entry.provenance",
		"previousDetail.job_provenance",
		"previousDetail.ingestion_decisions",
		"previousDetail.source_authority",
	} {
		if strings.Contains(script, nonexistent) {
			t.Fatalf("crm_jobs.js reads non-projected job field %q", nonexistent)
		}
	}
}

func TestWebCRMJobsAPIsAndStateActions(t *testing.T) {
	script := crmJobsTestAsset(t, "web/crm_jobs.js")
	for _, marker := range []string{
		`"/api/crm/jobs"`,
		"q: state.q",
		"status: state.status",
		"priority: state.priority",
		"site_name: state.site_name",
		"owner: state.owner",
		"limit: state.limit",
		"offset: state.offset",
		`method = "POST"`,
		`method = "PATCH"`,
		"body.expected_version = state.detail.job.version",
		`+ "/status"`,
		"expected_version: job.version",
		`["closed", "关闭职位"]`,
		`["open", "重新开启"]`,
		`["archived", "归档职位"]`,
		"lead_count",
		"lead_ids",
		"latest_ingestion_decision",
		"provenance_count",
		"employment_type",
		"requirements",
		"demand_code",
		"site_name",
		"onsite_contact",
		"headcount",
		"priority",
		"start_at",
		"deadline_at",
		"clear_start_at",
		"clear_deadline_at",
		"shift",
		"work_schedule",
		"compensation_summary",
		"accommodation",
		"transport_requirements",
		"required_documents",
	} {
		crmJobsTestContains(t, script, marker)
	}
}

func TestWebCRMJobsEscapesServerTextAndPreservesDrafts(t *testing.T) {
	script := crmJobsTestAsset(t, "web/crm_jobs.js")
	for _, marker := range []string{
		"function crmJobsParseRequirements(value)",
		`.split(/\r?\n|,/)`,
		"crmJobsEscape(job.title",
		"crmJobsEscape(job.company",
		"crmJobsEscape(job.site_name || job.location",
		"crmJobsEscape(job.owner",
		`crmJobsDetailValue("雇佣类型", job.employment_type`,
		"crmJobsEscape(requirements.length ? requirements.join",
		"crmJobsEscape(form.description)",
		"crmJobsEscape(form.requirements)",
		"crmJobsSyncForm(editor, state)",
		"state.dirty = true",
		"function crmJobsRecoverStaleDetail(state, actionLabel)",
		"var draft = state.form",
		"state.form = draft",
		"state.detail = latest",
		"当前草稿已保留，已加载服务端最新版本",
		"当前草稿已保留",
		"409 版本冲突",
		"（500）",
		"（400）",
		"编辑器中的内容仍会保留",
	} {
		crmJobsTestContains(t, script, marker)
	}
	for _, forbidden := range []string{
		"state.form = crmJobsEmptyForm();\n      state.error = crmJobsErrorMessage",
		"state.detail = null;\n      state.error = crmJobsErrorMessage",
		"state.jobs = [];\n      state.error = crmJobsErrorMessage",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("crm_jobs.js discards editor state on an API failure: %q", forbidden)
		}
	}
}

func TestWebCRMJobsOnsiteVerticalSlice(t *testing.T) {
	script := crmJobsTestAsset(t, "web/crm_jobs.js")
	for _, marker := range []string{
		`name="demand_code"`,
		`name="site_name"`,
		`name="onsite_contact"`,
		`name="headcount"`,
		`name="priority"`,
		`name="start_at"`,
		`name="deadline_at"`,
		`name="shift"`,
		`name="work_schedule"`,
		`name="compensation_summary"`,
		`name="accommodation"`,
		`name="transport_requirements"`,
		`name="required_documents"`,
		`crmJobsDetailValue("需求编号"`,
		`crmJobsDetailValue("现场名称"`,
		`crmJobsDetailValue("现场联系人"`,
		`crmJobsDetailValue("需求人数"`,
		`crmJobsDetailValue("优先级"`,
		`crmJobsDetailValue("入场证件"`,
		`crmJobsRenderProvenance(detail)`,
		`detail.provenance`,
		`detail.revisions`,
		`record.content_sha256`,
		`record.canonical_fingerprint`,
		`record.job_revision`,
		`append-only provenance`,
		"crmJobsDateTimeAPI(state.form.start_at)",
		"crmJobsDateTimeAPI(state.form.deadline_at)",
		"crmJobsParseList(state.form.required_documents)",
		"body.expected_version = state.detail.job.version",
		`crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(id))`,
	} {
		crmJobsTestContains(t, script, marker)
	}
}

func TestWebCRMJobsRendersDetailProvenanceAndRevisions(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/crm_jobs.js",
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
const view = views["jobs"];
const state = view.createState();
state.mode = "edit";
state.selectedID = "job_1";
state.detail = {
  job: {
    id: "job_1",
    title: "Operator",
    company: "Example",
    requirements: [],
    required_documents: [],
    status: "open",
    priority: "normal",
    version: 2,
    demand_revision_number: 2,
    active_demand_revision_id: "demand_2"
  },
  lead_ids: [],
  lead_count: 0,
  provenance: [{
    id: "provenance_projection",
    decision: "update",
    event_id: "event_1",
    message_key: "message_1",
    group_id: "group_1",
    author_id: "author_1",
    job_revision: 2
  }],
  revisions: [{
    id: "demand_2",
    number: 2,
    reason: "projection revision",
    created_by: "operator"
  }]
};
state.form = {
  title: "Operator",
  company: "Example",
  demand_code: "",
  owner: "",
  status: "open",
  priority: "normal",
  location: "",
  site_name: "",
  onsite_contact: "",
  headcount: "0",
  start_at: "",
  deadline_at: "",
  shift: "",
  employment_type: "",
  work_schedule: "",
  compensation_summary: "",
  accommodation: "",
  transport_requirements: "",
  description: "",
  requirements: "",
  required_documents: ""
};
state.revisions = state.detail.revisions;
const html = view.render(state);
for (const expected of ["provenance_projection", "更新", "projection revision", "当前 #2"]) {
  if (!html.includes(expected)) throw new Error("missing job detail projection " + expected);
}
`,
	)
}

func TestWebCRMJobsResponsiveStylesAndAssetPolicy(t *testing.T) {
	css := crmJobsTestAsset(t, "web/crm_jobs.css")
	for _, marker := range []string{
		".crmJobsLayout",
		"grid-template-columns: minmax(360px, 0.9fr) minmax(420px, 1.1fr)",
		"@media (max-width: 430px)",
		".crmJobsLayout,",
		".crmJobsTextGrid",
		".crmJobsSection",
		".crmJobsProvenance",
		".crmJobsProvenanceRow",
		"grid-template-columns: minmax(0, 1fr)",
		"overflow-wrap: anywhere",
		"white-space: normal",
	} {
		crmJobsTestContains(t, css, marker)
	}
	for assetPath, body := range map[string]string{
		"web/crm_jobs.js":  crmJobsTestAsset(t, "web/crm_jobs.js"),
		"web/crm_jobs.css": css,
	} {
		lower := strings.ToLower(body)
		for _, forbidden := range []string{
			"http://",
			"https://",
			"//cdn",
			"cdn.",
			"@import",
			"linear-gradient",
			"radial-gradient",
			"conic-gradient",
			"gradient(",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains forbidden reference %q", assetPath, forbidden)
			}
		}
	}
}

func crmJobsTestAsset(t *testing.T, assetPath string) string {
	t.Helper()
	return readEmbeddedWebAsset(t, assetPath)
}

func crmJobsTestContains(t *testing.T, body, marker string) {
	t.Helper()
	if !strings.Contains(body, marker) {
		t.Fatalf("asset does not contain %q", marker)
	}
}
