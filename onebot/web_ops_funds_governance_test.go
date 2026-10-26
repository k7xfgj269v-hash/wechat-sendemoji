package main

import (
	"strings"
	"testing"
)

func TestWebFundsGovernanceRegistrationAndAPIs(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/ops_funds_governance.js")
	for _, marker := range []string{
		`id: "funds-governance"`,
		`label: "资金与治理"`,
		"order: 56",
		"Workbench.registerView(view)",
		"Workbench.api(",
		"Workbench.automationAPI(",
		"Workbench.escapeHTML",
		"Workbench.encodeQuery",
		"Workbench.renderStatus",
		"Workbench.requestRender",
		`"/api/ops/fund_callbacks"`,
		`"/confirm_commitment"`,
		`"/confirm_final"`,
		`"/api/ops/governance/audit"`,
		`"/export"`,
		`"/anonymize"`,
		`"/api/ops/reports/summary"`,
		`"/api/ops/reports/export.csv"`,
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("ops_funds_governance.js missing %q", marker)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "localStorage", "Authorization"} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("ops_funds_governance.js bypasses workbench API with %q", forbidden)
		}
	}
}

func TestWebFundsGovernanceProtectedConfirmationAndDraftRecovery(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/ops_funds_governance.js")
	for _, marker := range []string{
		"window.confirm(",
		"expected_version: callback.version",
		"final_result",
		"当前草稿已保留",
		"loadFundDetail(state, state.selectedId, true)",
		"确认身份来自当前已认证的操作员会话",
		`data-field="final-result"`,
		`data-action="confirm-commitment"`,
		`data-action="confirm-final"`,
		`data-action="anonymize-candidate"`,
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("ops_funds_governance.js missing protected marker %q", marker)
		}
	}
	if strings.Count(js, "window.confirm(") < 2 {
		t.Fatal("protected fund and anonymization actions require explicit confirmation")
	}
	if strings.Count(js, "escapeHTML(") < 35 {
		t.Fatal("server-rendered values are not consistently escaped")
	}
	for _, forbidden := range []string{
		"operator_id: operatorID",
		`data-field="operator-id"`,
		"请输入操作员 ID",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("protected fund confirmation sends client identity with %q", forbidden)
		}
	}
}

func TestWebFundsGovernanceConfirmationUsesAuthenticatedContext(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/ops_funds_governance.js",
		`
const views = {};
const automationCalls = [];
global.window = {
  confirm() { return true; },
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path, options) {
      automationCalls.push({ path, options });
      return Promise.resolve({
        id: "fund_1",
        candidate_id: "cand_1",
        amount_minor: 10000,
        currency: "CNY",
        condition: "arrival",
        promised_by: "customer",
        status: "committed",
        version: 3
      });
    },
    api(path) {
      if (path.indexOf("/ledger") !== -1) return Promise.resolve({ ledger: [] });
      return Promise.resolve({ total: 1, fund_callbacks: [] });
    },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.document = {};
`,
		`
(async function () {
  const view = views["funds-governance"];
  const state = view.createState();
  state.detail = {
    id: "fund_1",
    candidate_id: "cand_1",
    amount_minor: 10000,
    currency: "CNY",
    condition: "arrival",
    promised_by: "customer",
    status: "awaiting_commitment",
    version: 2
  };
  const listeners = {};
  const root = {
    addEventListener(type, listener) { listeners[type] = listener; }
  };
  view.bind(root, state);
  const target = {
    closest() { return this; },
    getAttribute(name) {
      return name === "data-action" ? "confirm-commitment" : null;
    }
  };
  listeners.click({ target });
  await new Promise((resolve) => setTimeout(resolve, 10));
  if (automationCalls.length !== 1) {
    throw new Error("expected one protected confirmation request");
  }
  const call = automationCalls[0];
  if (call.path !== "/api/ops/fund_callbacks/fund_1/confirm_commitment" ||
      call.options.body.expected_version !== 2 ||
      call.options.body.confirmed !== true) {
    throw new Error("protected confirmation request is malformed");
  }
  if (Object.prototype.hasOwnProperty.call(call.options.body, "operator_id")) {
    throw new Error("protected confirmation sent client operator_id");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebFundsGovernanceResponsiveScopedStyles(t *testing.T) {
	css := readEmbeddedWebAsset(t, "web/ops_funds_governance.css")
	for _, marker := range []string{
		".fundsGovernance {",
		".fundsGovernance__workspace",
		"grid-template-columns: minmax(350px, 0.82fr) minmax(460px, 1.18fr)",
		".fundsGovernance__tableWrap",
		"overflow: auto",
		"overflow-wrap: anywhere",
		"@media (max-width: 430px)",
		"grid-template-columns: minmax(0, 1fr)",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("ops_funds_governance.css missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"@import", "url(", "http://", "https://",
		"linear-gradient", "radial-gradient", "conic-gradient",
		".fundsGovernance__panel .fundsGovernance__panel",
	} {
		if strings.Contains(strings.ToLower(css), strings.ToLower(forbidden)) {
			t.Fatalf("ops_funds_governance.css contains forbidden marker %q", forbidden)
		}
	}
}
