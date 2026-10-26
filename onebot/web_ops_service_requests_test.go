package main

import (
	"strings"
	"testing"
)

func TestWebOpsServiceRequestsContract(t *testing.T) {
	script := serviceRequestReadAsset(t, "web/ops_service_requests.js")
	for _, marker := range []string{
		"window.OneBotWorkbench",
		"wb.registerView({",
		`id: "service-requests"`,
		`label: "服务请求"`,
		`"/api/ops/service_requests"`,
		`"/transition"`,
		`method: "PATCH"`,
		`method: "POST"`,
		"expected_version",
		"from_status",
		"to_status",
		"requires_human",
		"protectedTransition",
		"wb.automationAPI",
		"service_requests",
		"HUMAN_CATEGORIES",
		"isConflict",
		"保留草稿",
		"wb.escapeHTML(item.description)",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("ops_service_requests.js missing %q", marker)
		}
	}
}

func TestWebOpsServiceRequestsResponsive(t *testing.T) {
	style := strings.ToLower(serviceRequestReadAsset(t, "web/ops_service_requests.css"))
	for _, marker := range []string{
		".opsservicerequests-layout",
		"grid-template-columns: minmax(440px, 1.08fr) minmax(360px, 0.92fr)",
		"overflow-wrap: anywhere",
		"word-break: break-word",
		"@media (max-width: 460px)",
		"grid-template-columns: 1fr",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("ops_service_requests.css missing %q", marker)
		}
	}
	for _, forbidden := range []string{"linear-gradient", "radial-gradient", "@import", "https://", "http://"} {
		if strings.Contains(style, forbidden) {
			t.Fatalf("ops_service_requests.css contains %q", forbidden)
		}
	}
}

func serviceRequestReadAsset(t *testing.T, path string) string {
	t.Helper()
	data, err := webAssets.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
