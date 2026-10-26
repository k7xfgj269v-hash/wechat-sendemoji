package main

import (
	"strings"
	"testing"
)

func TestWebCRMCandidatesRegistersWorkbenchView(t *testing.T) {
	body := crmCandidatesReadAsset(t, "web/crm_candidates.js")
	for _, marker := range []string{
		"window.OneBotWorkbench",
		"workbench.registerView({",
		`id: "candidates"`,
		`label: "候选人"`,
		"order: 20",
		"createState: crmCandidatesCreateState",
		"render: crmCandidatesRender",
		"onEnter:",
		"bind: crmCandidatesBind",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("crm_candidates.js does not contain registration marker %q", marker)
		}
	}
}

func TestWebCRMCandidatesUsesCandidateAPIContract(t *testing.T) {
	body := crmCandidatesReadAsset(t, "web/crm_candidates.js")
	for _, marker := range []string{
		`"/api/crm/candidates"`,
		"api.encodeQuery({",
		"q: state.filters.q.trim()",
		"status: state.filters.status",
		"owner: state.filters.owner.trim()",
		"source: state.filters.source.trim()",
		"limit: PAGE_SIZE",
		"offset: page * PAGE_SIZE",
		`method: "POST"`,
		`options.method = "PATCH"`,
		"expected_version: state.form.version",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("crm_candidates.js does not contain API marker %q", marker)
		}
	}
}

func TestWebCRMCandidatesEscapesServerTextAndSubmitsSkillsArray(t *testing.T) {
	body := crmCandidatesReadAsset(t, "web/crm_candidates.js")
	for _, marker := range []string{
		"api.escapeHTML(candidate.full_name",
		"api.escapeHTML(candidate.owner",
		"api.escapeHTML(candidate.source",
		"api.escapeHTML(candidate.wechat_id",
		"api.escapeHTML(candidate.phone",
		"api.escapeHTML(candidate.email",
		"api.escapeHTML(skill)",
		"api.escapeHTML(value)",
		"api.renderStatus(state.error, state.success, busyText)",
		".split(/[\\n,，;；]+/)",
		"skills: crmCandidatesSplitSkills(form.skills)",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("crm_candidates.js does not contain safety marker %q", marker)
		}
	}
}

func TestWebCRMCandidatesPreservesInteractiveState(t *testing.T) {
	body := crmCandidatesReadAsset(t, "web/crm_candidates.js")
	for _, marker := range []string{
		`formMode: ""`,
		`state.formMode = "create"`,
		`state.formMode = "edit"`,
		"state.saving = true",
		"state.detailLoading = true",
		"state.selectedId = id",
		"state.error = err.message",
		"crmCandidatesReadForm(form, state.form)",
		"request !== state.listRequest",
		"request !== state.detailRequest",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("crm_candidates.js does not contain state marker %q", marker)
		}
	}

	for _, forbidden := range []string{
		"state.selectedId = \"\"",
		"state.detail = null;",
		"state.form = crmCandidatesBlankForm();\n      state.error = err.message",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("crm_candidates.js contains state-clearing error path %q", forbidden)
		}
	}
}

func TestWebCRMCandidatesResponsiveAndLocalOnly(t *testing.T) {
	css := crmCandidatesReadAsset(t, "web/crm_candidates.css")
	for _, marker := range []string{
		".crmCandidatesWorkspace",
		"grid-template-columns: minmax(360px, 0.9fr) minmax(420px, 1.1fr)",
		"@media (max-width: 430px)",
		"grid-template-columns: 1fr",
		"overflow-wrap: anywhere",
		"word-break: break-word",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("crm_candidates.css does not contain responsive marker %q", marker)
		}
	}

	combined := strings.ToLower(crmCandidatesReadAsset(t, "web/crm_candidates.js") + css)
	for _, forbidden := range []string{
		"http://",
		"https://",
		"@import",
		"linear-gradient",
		"radial-gradient",
		"conic-gradient",
		"gradient(",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("candidate frontend contains forbidden external or gradient marker %q", forbidden)
		}
	}
}

func crmCandidatesReadAsset(t *testing.T, assetPath string) string {
	t.Helper()
	data, err := webAssets.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
