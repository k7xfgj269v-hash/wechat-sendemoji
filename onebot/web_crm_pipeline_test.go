package main

import (
	"strings"
	"testing"
)

func TestWebCRMPipelineViewContract(t *testing.T) {
	script := crmPipelineViewReadAsset(t, "web/crm_pipeline.js")

	crmPipelineViewAssertOrdered(t, script, []string{
		`"sourced"`,
		`"screening"`,
		`"interview"`,
		`"offer"`,
		`"hired"`,
		`"rejected"`,
		`"withdrawn"`,
	})
	crmPipelineViewAssertContains(t, script, []string{
		"window.OneBotWorkbench",
		`id: "pipeline"`,
		`label: "招聘流程"`,
		"order: 40",
		"createState: crmPipelineViewCreateState",
		"render: crmPipelineViewRender",
		"onEnter: crmPipelineViewOnEnter",
		"bind: crmPipelineViewBind",
		`"/api/crm/pipeline"`,
		`"/api/crm/candidates?limit=200"`,
		`"/api/crm/jobs?limit=200"`,
		`"/api/crm/leads"`,
		`"/api/crm/notes"`,
		`target_type: "lead"`,
		`expected_version: selected.lead.version`,
		`from_stage: fromStage`,
		`to_stage: toStage`,
		`"/transition"`,
		"api.escapeHTML(",
		"crmPipelineViewIsStale",
		"crmPipelineViewRecoverStale",
		"数据已被其他操作更新，已重新加载最新看板。",
		`"/api/ops/onboarding_cases"`,
		"crmPipelineViewRenderStageHistory",
		"crmPipelineViewRenderHandoff",
		`api.navigate("onboarding-fleet")`,
	})
	if strings.Contains(script, "fetch(") {
		t.Fatal("crm_pipeline.js must use the shared OneBotWorkbench API instead of fetch")
	}
	for _, optimisticWrite := range []string{
		"selected.lead.stage =",
		"selected.lead.owner =",
		"lead.stage = toStage",
		"lead.owner = state.ownerDraft",
	} {
		if strings.Contains(script, optimisticWrite) {
			t.Fatalf("crm_pipeline.js contains optimistic lead mutation %q", optimisticWrite)
		}
	}
}

func TestWebCRMPipelineViewAllowedTransitions(t *testing.T) {
	script := crmPipelineViewReadAsset(t, "web/crm_pipeline.js")
	crmPipelineViewAssertContains(t, script, []string{
		`sourced: ["screening", "rejected", "withdrawn"]`,
		`screening: ["interview", "rejected", "withdrawn"]`,
		`interview: ["offer", "rejected", "withdrawn"]`,
		`offer: ["hired", "rejected", "withdrawn"]`,
		"hired: []",
		"rejected: []",
		"withdrawn: []",
		`data-crm-pipeline-view-transition="`,
		"allowed.indexOf(toStage) < 0",
	})

	postIndex := strings.Index(script, `api.api("/api/crm/leads/" + encodeURIComponent(selected.lead.id) + "/transition"`)
	if postIndex < 0 {
		t.Fatal("transition endpoint call is missing")
	}
	if reloadIndex := strings.Index(script[postIndex:], "crmPipelineViewLoadBoard(state, api)"); reloadIndex < 0 {
		t.Fatal("transition must call the transition endpoint and reload the board after success")
	}
}

func TestWebCRMPipelineViewNotesAndEscaping(t *testing.T) {
	script := crmPipelineViewReadAsset(t, "web/crm_pipeline.js")
	crmPipelineViewAssertContains(t, script, []string{
		`target_id: leadID`,
		`author: state.noteAuthor`,
		`body: state.noteBody`,
		"api.escapeHTML(item.candidate_name",
		"api.escapeHTML(item.job_title",
		"api.escapeHTML(lead.owner",
		"api.escapeHTML(note.author",
		"api.escapeHTML(note.created_at",
		"api.escapeHTML(note.body",
	})
}

func TestWebCRMPipelineViewResponsiveOverflow(t *testing.T) {
	script := crmPipelineViewReadAsset(t, "web/crm_pipeline.js")
	style := crmPipelineViewReadAsset(t, "web/crm_pipeline.css")

	crmPipelineViewAssertContains(t, script, []string{
		`class="crmPipelineView-boardScroll"`,
		`data-crm-pipeline-view-overflow="stage-grid"`,
		`class="crmPipelineView-stageGrid"`,
	})
	crmPipelineViewAssertContains(t, style, []string{
		".crmPipelineView-root",
		".crmPipelineView-boardScroll",
		".crmPipelineView-stageGrid",
		"grid-template-columns: repeat(7",
		"overflow-x: auto",
		"max-width: 100%",
		"@media (max-width: 430px)",
		"overflow-x: hidden",
	})
}

func TestWebCRMPipelineViewHasNoExternalAssetsOrGradients(t *testing.T) {
	for _, asset := range []string{"web/crm_pipeline.js", "web/crm_pipeline.css"} {
		body := strings.ToLower(crmPipelineViewReadAsset(t, asset))
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
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contains forbidden marker %q", asset, forbidden)
			}
		}
	}
}

func crmPipelineViewReadAsset(t *testing.T, name string) string {
	t.Helper()
	body, err := webAssets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func crmPipelineViewAssertContains(t *testing.T, body string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			t.Fatalf("asset does not contain %q", marker)
		}
	}
}

func crmPipelineViewAssertOrdered(t *testing.T, body string, markers []string) {
	t.Helper()
	position := -1
	for _, marker := range markers {
		next := strings.Index(body[position+1:], marker)
		if next < 0 {
			t.Fatalf("asset does not contain ordered marker %q", marker)
		}
		position += next + 1
	}
}
