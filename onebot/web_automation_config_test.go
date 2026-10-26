package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebAutomationConfigRegistrationAPIsAndSecretHandling(t *testing.T) {
	script := readEmbeddedWebAsset(t, "web/automation_config.js")
	for _, marker := range []string{
		`id: "automation-config"`,
		`label: "AI 配置"`,
		`var PROVIDER_PATH = "/api/automation/provider";`,
		`var PROVIDER_TEST_PATH = "/api/automation/provider/test";`,
		`var AUDIENCES_PATH = "/api/automation/audiences";`,
		`protocol: "chat_completions"`,
		`protocol: state.draft.protocol`,
		`name="protocol"`,
		`value="responses"`,
		`gateModel: String(config.gate_model || "")`,
		`gatePromptTemplate: String(config.gate_prompt_template || "")`,
		`name="gate_model"`,
		`name="gate_prompt_template"`,
		`gate_model: state.draft.gateModel.trim()`,
		`gate_prompt_template: state.draft.gatePromptTemplate.trim()`,
		`placeholder="留空则使用主 Model"`,
		`name="gate_prompt_template" maxlength="65536" rows="8" required`,
		`config.readiness`,
		`latest_connection_test`,
		`endpoint_configured`,
		`model_configured`,
		`api_key_configured`,
		`matches_current_config`,
		`type="password"`,
		`name="clear_api_key"`,
		`body.clear_api_key = true;`,
		`body.expected_version = Number(state.config.version);`,
		`method: "PUT"`,
		`method: "POST"`,
		`method = "PATCH"`,
		`method: "DELETE"`,
		`err.status === 409`,
		`state.draft = providerDraft;`,
		`state.keyDraft = "";`,
		`load(state, "连接测试成功。", true)`,
		`load(state, "连接测试失败，已刷新 readiness。", true)`,
		`Workbench.escapeHTML`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_config.js missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage", "document.cookie", "Authorization", "fetch("} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("automation_config.js contains forbidden marker %q", forbidden)
		}
	}
}

func TestWebAutomationConfigConnectionTestPreservesUnsavedDraft(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_config.js",
		`
const views = {};
const calls = [];
const savedConfig = {
  enabled: true,
  protocol: "chat_completions",
  endpoint: "https://saved.example/v1/chat/completions",
  model: "saved-model",
  gate_model: "",
  timeout_seconds: 90,
  prompt_template: "saved prompt",
  gate_prompt_template: "saved gate prompt",
  version: 4,
  api_key_configured: true,
  audiences: [],
  readiness: { status: "ready" }
};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path, options) {
      calls.push({ path, options: options || {} });
      if (path === "/api/automation/provider/test") {
        return Promise.resolve({ status: "ok" });
      }
      if (path === "/api/automation/provider") {
        return Promise.resolve(savedConfig);
      }
      if (path === "/api/automation/prompts") {
        return Promise.resolve({ revisions: [], active_revision_id: "" });
      }
      throw new Error("unexpected path " + path);
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
`,
		`
(async function () {
  const view = views["automation-config"];
  const state = view.createState();
  await view.onEnter(state);
  const html = view.render(state);
  if (state.draft.gateModel !== "" ||
      state.draft.gatePromptTemplate !== "saved gate prompt" ||
      !html.includes('name="gate_model" maxlength="200" autocomplete="off" value=""') ||
      !html.includes('placeholder="留空则使用主 Model"') ||
      !html.includes('gate_prompt_template" maxlength="65536" rows="8" required') ||
      !html.includes('saved gate prompt')) {
    throw new Error("gate fields were not rendered from provider config");
  }
  const listeners = {};
  const form = {
    elements: {
      enabled: { checked: true },
      protocol: { value: "responses" },
      endpoint: { value: "https://draft.example/v1/responses" },
      model: { value: "draft-model" },
      gate_model: { value: "draft-gate-model" },
      timeout_seconds: { value: "45" },
      prompt_template: { value: "draft prompt" },
      gate_prompt_template: { value: "draft gate prompt" },
      prompt_reason: { value: "draft reason" },
      api_key: { value: "temporary-key" },
      clear_api_key: { checked: false }
    },
    addEventListener(type, listener) { listeners["form:" + type] = listener; }
  };
  const testButton = {
    addEventListener(type, listener) {
      if (type === "click") listeners.test = listener;
    }
  };
  const root = {
    querySelector(selector) {
      if (selector === '[data-automation-config-form="provider"]') return form;
      if (selector === '[data-automation-config-action="test"]') return testButton;
      return null;
    },
    querySelectorAll() { return []; }
  };
  view.bind(root, state);
  listeners.test();
  await new Promise((resolve) => setTimeout(resolve, 10));
  if (state.draft.protocol !== "responses" ||
      state.draft.endpoint !== "https://draft.example/v1/responses" ||
      state.draft.model !== "draft-model" ||
      state.draft.gateModel !== "draft-gate-model" ||
      state.draft.promptTemplate !== "draft prompt" ||
      state.draft.gatePromptTemplate !== "draft gate prompt") {
    throw new Error("connection test discarded unsaved provider draft");
  }
  if (state.keyDraft !== "") {
    throw new Error("connection test retained API key draft");
  }
  if (state.config !== savedConfig || state.success !== "连接测试成功。") {
    throw new Error("connection test did not refresh authoritative readiness");
  }
  const testCall = calls.find((call) => call.path === "/api/automation/provider/test");
  if (!testCall || testCall.options.body.protocol !== "responses" ||
      testCall.options.body.api_key !== "temporary-key" ||
      testCall.options.body.gate_model !== "draft-gate-model" ||
      testCall.options.body.gate_prompt_template !== "draft gate prompt") {
    throw new Error("connection test did not submit the current draft");
  }
  listeners["form:submit"]({ preventDefault() {} });
  await new Promise((resolve) => setTimeout(resolve, 10));

  const putCall = calls.find((call) =>
    call.path === "/api/automation/provider" && call.options.method === "PUT");
  if (!putCall ||
      putCall.options.body.expected_version !== 4 ||
      putCall.options.body.gate_model !== "draft-gate-model" ||
      putCall.options.body.gate_prompt_template !== "draft gate prompt") {
    throw new Error("gate fields were not saved through the provider payload");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationConfigResponsiveScopedCSS(t *testing.T) {
	style := strings.ToLower(readEmbeddedWebAsset(t, "web/automation_config.css"))
	for _, marker := range []string{
		".automationconfig {",
		".automationconfig .automationconfig-readiness",
		"grid-template-columns: minmax(440px, 1.2fr) minmax(320px, 0.8fr)",
		"@media (max-width: 900px)",
		"@media (max-width: 520px)",
		"grid-template-columns: 1fr",
		"overflow-wrap: anywhere",
		".automationconfig .automationconfig-fieldhelp",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("automation_config.css missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"linear-gradient", "radial-gradient", "@import", "url("} {
		if strings.Contains(style, forbidden) {
			t.Fatalf("automation_config.css contains forbidden marker %q", forbidden)
		}
	}
}

func runWebNodeBehavior(t *testing.T, assetPath, before, after string) {
	t.Helper()
	program := before + "\n" + readEmbeddedWebAsset(t, assetPath) + "\n" + after
	path := filepath.Join(t.TempDir(), "web_behavior_test.js")
	if err := os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("node", path).CombinedOutput()
	if err != nil {
		t.Fatalf("node behavior test failed: %v\n%s", err, output)
	}
}
