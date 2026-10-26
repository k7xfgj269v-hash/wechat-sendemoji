package main

import (
	"strings"
	"testing"
)

func TestWebAutomationNavigationUsesRegisteredViews(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	for _, marker := range []string{
		`var navigationSections = ["招聘交付", "驻场履约", "AI 中心", "联系人", "系统"]`,
		"function navigationItems()",
		"return Object.keys(views).map(function (id)",
		"navigationItems().map(navButton)",
		"navigationItems().forEach(function (item)",
		"function navigationSectionRank(section)",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("app.js missing registered navigation marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"function navigationViewDefinitions(",
		`section: "AI 自动化"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("app.js still contains legacy navigation marker %q", forbidden)
		}
	}
}

func TestWebAutomationAPIAuthAndImmutableInputs(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	api := webAutomationFunction(t, body, "function automationAPI", "function navigationItems")

	for _, marker := range []string{
		`var target = new URL(path, window.location.href)`,
		`target.origin !== window.location.origin`,
		`var callerOptions = options || {}`,
		`var init = Object.assign({}, callerOptions)`,
		`var headers = new Headers(callerOptions.headers || {})`,
		`var tokenKey = "onebot.automation.operator_token"`,
		`window.sessionStorage.getItem(tokenKey)`,
		`window.prompt("请输入 AI 自动化操作员令牌")`,
		`window.sessionStorage.setItem(tokenKey, token)`,
		`headers.set("Authorization", "Bearer " + token)`,
		`window.sessionStorage.removeItem(tokenKey)`,
		`fetch(target.pathname + target.search, init)`,
		`requestError.status = response.status`,
		`requestError.code = data && typeof data === "object" && data.code ? data.code : ""`,
	} {
		if !strings.Contains(api, marker) {
			t.Fatalf("automationAPI does not contain contract marker %q", marker)
		}
	}

	if strings.Count(api, "window.prompt(") != 1 {
		t.Fatal("automationAPI must prompt exactly once when the token is absent")
	}
	for _, forbidden := range []string{
		"localStorage",
		"document.cookie",
		"console.",
		"options.headers =",
		"callerOptions.headers =",
		"options.body =",
		`url.searchParams.set("token"`,
	} {
		if strings.Contains(api, forbidden) {
			t.Fatalf("automationAPI contains forbidden mutation or leakage marker %q", forbidden)
		}
	}
}

func TestWebAutomationAPIKeepsWorkbenchAndLoaderContract(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	for _, marker := range []string{
		"workbenchAPI = Object.freeze({",
		"registerView: registerView",
		"api: api",
		"automationAPI: automationAPI",
		"escapeHTML: escapeHTML",
		"encodeQuery: encodeQuery",
		"renderStatus: renderStatus",
		"requestRender: requestRender",
		"navigate: navigate",
		"on: on",
		"window.OneBotWorkbench = workbenchAPI",
		`api("/workbench/modules.json")`,
		"Promise.all(styles.map(loadStyleAsset))",
		"scripts.reduce",
		"function navigationItems()",
		"return Object.keys(views).map(function (id)",
		"function navigationSectionRank(section)",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("app.js does not preserve workbench marker %q", marker)
		}
	}
}

func webAutomationFunction(t *testing.T, body, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(body, startMarker)
	if start < 0 {
		t.Fatalf("missing function marker %q", startMarker)
	}
	end := strings.Index(body[start:], endMarker)
	if end < 0 {
		t.Fatalf("missing function end marker %q", endMarker)
	}
	return body[start : start+end]
}
