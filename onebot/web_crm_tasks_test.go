package main

import (
	"strings"
	"testing"
)

func TestWebCRMTasksRegistrationAndAPIContract(t *testing.T) {
	script := crmTasksReadAsset(t, "web/crm_tasks.js")
	for _, marker := range []string{
		"window.OneBotWorkbench",
		"wb.registerView({",
		`id: "tasks"`,
		`label: "跟进任务"`,
		"order: 50",
		"createState: crmTasksCreateState",
		"render: crmTasksRender",
		"onEnter: crmTasksOnEnter",
		"bind: crmTasksBind",
		`"/api/crm/tasks"`,
		`"/api/crm/tasks/"`,
		`"/status"`,
		`"/api/crm/candidates"`,
		`"/api/crm/leads"`,
		`method: "PATCH"`,
		`method: "POST"`,
		"expected_version",
		"candidate_id",
		"lead_id",
		"limit: 200",
		"offset: 0",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("crm_tasks.js does not contain contract marker %q", marker)
		}
	}
}

func TestWebCRMTasksFiltersDateStatusAndEscaping(t *testing.T) {
	script := crmTasksReadAsset(t, "web/crm_tasks.js")
	for _, marker := range []string{
		"name=\"status\"",
		"name=\"owner\"",
		"name=\"due_from\"",
		"name=\"due_to\"",
		"name=\"overdue\"",
		`type="datetime-local"`,
		"crmTasksLocalToUTC",
		"date.toISOString()",
		"date.getFullYear()",
		`status: nextStatus`,
		`nextStatus = task.status === "completed" ? "open" : "completed"`,
		"wb.escapeHTML(task.title",
		"wb.escapeHTML(crmTasksContext(item))",
		"wb.escapeHTML(task.owner",
		"wb.renderStatus(s.error",
		`throw new Error("必须选择候选人或 Lead")`,
		`data-crm-tasks-submit="reassign"`,
		"任务已重开",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("crm_tasks.js does not contain behavior marker %q", marker)
		}
	}

	clearForm := strings.Index(script, "s.form = crmTasksEmptyForm();")
	saveCatch := strings.Index(script, "crmTasksSave(s, form, submitter)")
	if clearForm < 0 || saveCatch < 0 || clearForm < saveCatch {
		t.Fatal("task form must only be cleared by the explicit new-task flow")
	}
}

func TestWebCRMTasksResponsiveAndSelfContained(t *testing.T) {
	script := strings.ToLower(crmTasksReadAsset(t, "web/crm_tasks.js"))
	style := strings.ToLower(crmTasksReadAsset(t, "web/crm_tasks.css"))

	for _, marker := range []string{
		".crmtasks-layout",
		"grid-template-columns: minmax(420px, 1.08fr) minmax(340px, 0.92fr)",
		"@media (max-width: 430px)",
		"grid-template-columns: 1fr",
		"overflow-wrap: anywhere",
		"word-break: break-word",
		".crmtasks-formactions",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("crm_tasks.css does not contain responsive marker %q", marker)
		}
	}

	for assetName, body := range map[string]string{
		"crm_tasks.js":  script,
		"crm_tasks.css": style,
	} {
		for _, forbidden := range []string{
			"http://",
			"https://",
			"//cdn",
			"@import",
			"linear-gradient",
			"radial-gradient",
			"conic-gradient",
			"gradient(",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contains forbidden marker %q", assetName, forbidden)
			}
		}
	}
}

func crmTasksReadAsset(t *testing.T, assetPath string) string {
	t.Helper()
	data, err := webAssets.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
