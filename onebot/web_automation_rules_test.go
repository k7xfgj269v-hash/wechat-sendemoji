package main

import (
	"strings"
	"testing"
)

func TestWebAutomationRulesRegistrationAndPolicyAPIContract(t *testing.T) {
	script := automationRulesReadAsset(t, "web/automation_rules.js")
	for _, marker := range []string{
		"var Workbench = window.OneBotWorkbench;",
		"Workbench.registerView({",
		`id: "automation-rules"`,
		`label: "自动化规则"`,
		"order: 80",
		"createState: automationRulesCreateState",
		"render: automationRulesRender",
		"onEnter: automationRulesOnEnter",
		"bind: automationRulesBind",
		`var AUTOMATION_POLICY_PATH = "/api/automation/policy";`,
		"Workbench.automationAPI(AUTOMATION_POLICY_PATH)",
		`method: "PUT"`,
		"envelope.policy",
		"envelope.immutable_rules",
		"immutable.r3",
		"immutable.r4",
		"expected_version: version",
		"mode: s.draft.mode",
		"r1_auto_approve: s.draft.r1AutoApprove",
		"r1_min_confidence:",
		"r1_allowed_tools:",
		"r2_private_reply_auto_approve: s.draft.r2PrivateReplyAutoApprove",
		"r2_min_confidence:",
		"r2_allowed_intents:",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_rules.js does not contain contract marker %q", marker)
		}
	}

	if count := strings.Count(script, "Workbench.registerView({"); count != 1 {
		t.Fatalf("automation_rules.js registers %d views, want exactly 1", count)
	}
	if count := strings.Count(script, `id: "automation-rules"`); count != 1 {
		t.Fatalf("automation_rules.js contains %d automation-rules view IDs, want exactly 1", count)
	}

	payload := automationRulesFunction(t, script, "function automationRulesPayload", "function automationRulesLoad")
	for _, forbidden := range []string{"immutable_rules", "r3:", "r4:"} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("policy payload must not submit immutable rule marker %q", forbidden)
		}
	}
	automationRulesAssertExactPayloadFields(t, payload, []string{
		"expected_version",
		"mode",
		"r1_auto_approve",
		"r1_min_confidence",
		"r1_allowed_tools",
		"r2_private_reply_auto_approve",
		"r2_min_confidence",
		"r2_allowed_intents",
	})
}

func TestWebAutomationRulesControlsEscapingAndImmutableBoundaries(t *testing.T) {
	script := automationRulesReadAsset(t, "web/automation_rules.js")
	for _, marker := range []string{
		`automationRulesModeOption("manual"`,
		`automationRulesModeOption("automatic"`,
		`type="number"`,
		`min="0" max="1" step="0.01"`,
		`type="checkbox" name="r1_auto_approve"`,
		`type="checkbox" name="r2_private_reply_auto_approve"`,
		`["candidate.create", "创建候选人"]`,
		`["candidate.update", "更新候选人"]`,
		`["candidate.link", "关联候选人与消息"]`,
		`["lead.create", "创建招聘线索"]`,
		`["lead.transition", "推进早期招聘阶段"]`,
		`["task.create", "创建跟进任务"]`,
		`["note.create", "创建内部备注"]`,
		`["general", "一般沟通"]`,
		`["scheduling", "时间安排"]`,
		`["information", "信息回复"]`,
		`automationRulesImmutableList("R3"`,
		`automationRulesImmutableList("R4"`,
		"Workbench.escapeHTML(automationRulesRuleText(rule))",
		"Workbench.escapeHTML(draft.r1MinConfidence)",
		"Workbench.escapeHTML(draft.r2MinConfidence)",
		"Workbench.renderStatus(s.error, s.success",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_rules.js does not contain UI boundary marker %q", marker)
		}
	}

	lower := strings.ToLower(script)
	for _, forbidden := range []string{
		"fetch(",
		"xmlhttprequest",
		"sessionstorage",
		"localstorage",
		"authorization",
		"document.cookie",
		"workbench.api(",
		"workbench.on(",
		"workbench.encodequery(",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("automation_rules.js contains forbidden API or credential marker %q", forbidden)
		}
	}
	automationRulesAssertWorkbenchMethods(t, script)
}

func TestWebAutomationRulesPreservesDraftUntilSaveAndAuthoritativeReload(t *testing.T) {
	script := automationRulesReadAsset(t, "web/automation_rules.js")
	for _, marker := range []string{
		"automationRulesReadForm(s, form);",
		"s.dirty = !automationRulesDraftMatchesPolicy(s);",
		"automationRulesSyncDraft(s, form);",
		"s.error = err.message;",
		"var persisted = false;",
		"persisted = true;",
		`"策略已保存，但权威重载失败："`,
		"automationRulesApplyEnvelope(s, envelope, true);",
		`s.success = "策略已保存并从服务端重新加载。";`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_rules.js does not contain state preservation marker %q", marker)
		}
	}

	save := automationRulesFunction(t, script, "function automationRulesSave", "function automationRulesBind")
	put := strings.Index(save, `method: "PUT"`)
	reload := strings.Index(save, "return Workbench.automationAPI(AUTOMATION_POLICY_PATH);")
	replace := strings.Index(save, "automationRulesApplyEnvelope(s, envelope, true);")
	if put < 0 || reload < 0 || replace < 0 || !(put < reload && reload < replace) {
		t.Fatalf("save must PUT, GET authoritative policy, then replace draft: put=%d reload=%d replace=%d", put, reload, replace)
	}

	catchStart := strings.Index(save, ".catch(function (err)")
	if catchStart < 0 {
		t.Fatal("save does not contain a failure handler")
	}
	catchBody := save[catchStart:]
	for _, forbidden := range []string{
		"s.draft =",
		"automationRulesEmptyDraft()",
		"automationRulesDraftFromPolicy(",
	} {
		if strings.Contains(catchBody, forbidden) {
			t.Fatalf("save failure handler resets unsaved draft through %q", forbidden)
		}
	}
}

func TestWebAutomationRulesModeSwitchShowsEffectiveAndPendingState(t *testing.T) {
	script := automationRulesReadAsset(t, "web/automation_rules.js")
	style := automationRulesReadAsset(t, "web/automation_rules.css")

	for _, marker := range []string{
		`class="automationRules-modeControl"`,
		`automationRulesModeOption("manual", "手动模式", "AI 提议，人审核并执行"`,
		`automationRulesModeOption("automatic", "自动模式", "低风险动作按策略执行"`,
		`modeChanged ? "待保存" : "当前生效"`,
		`"保存并启用自动模式"`,
		`"保存并切换为手动模式"`,
		`automaticSettingsDisabled = busy || !automatic`,
		`aria-disabled="true"`,
		`automationRulesDisabled(busy || !s.dirty)`,
		`Workbench.requestRender();`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_rules.js does not contain mode-switch marker %q", marker)
		}
	}

	for _, marker := range []string{
		".automationRules .automationRules-modeControl",
		".automationRules .automationRules-modeSwitch",
		".automationRules .automationRules-modeOption input:checked + span",
		`.automationRules .automationRules-modeHeading > strong[data-mode-state="pending"]`,
		`.automationRules .automationRules-fieldset[aria-disabled="true"]`,
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("automation_rules.css does not contain mode-switch marker %q", marker)
		}
	}
}

func TestWebAutomationRulesResponsiveScopedAndSelfContained(t *testing.T) {
	script := strings.ToLower(automationRulesReadAsset(t, "web/automation_rules.js"))
	style := strings.ToLower(automationRulesReadAsset(t, "web/automation_rules.css"))

	for _, marker := range []string{
		".automationrules {",
		".automationrules .automationrules-workspace",
		"grid-template-columns: minmax(420px, 1.15fr) minmax(300px, 0.85fr)",
		"@media (max-width: 430px)",
		"grid-template-columns: 1fr",
		"overflow-wrap: anywhere",
		"min-height: 36px",
		".automationrules .automationrules-actions button",
		"width: 100%",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("automation_rules.css does not contain responsive marker %q", marker)
		}
	}
	automationRulesAssertScopedCSS(t, style)

	for assetName, body := range map[string]string{
		"automation_rules.js":  script,
		"automation_rules.css": style,
	} {
		for _, forbidden := range []string{
			"http://",
			"https://",
			"//cdn",
			"@import",
			" import ",
			"linear-gradient",
			"radial-gradient",
			"conic-gradient",
			"gradient(",
			"url(",
			"card",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contains forbidden marker %q", assetName, forbidden)
			}
		}
	}
}

func automationRulesAssertExactPayloadFields(t *testing.T, payload string, expected []string) {
	t.Helper()
	start := strings.Index(payload, "return {")
	if start < 0 {
		t.Fatal("policy payload does not contain an object return")
	}
	end := strings.Index(payload[start:], "\n    };")
	if end < 0 {
		t.Fatal("policy payload object is not closed with the expected marker")
	}

	fields := make([]string, 0, len(expected))
	for _, line := range strings.Split(payload[start:start+end], "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field, _, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("policy payload line has no field separator: %q", line)
		}
		fields = append(fields, strings.TrimSpace(field))
	}
	if strings.Join(fields, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("policy payload fields = %#v, want exactly %#v", fields, expected)
	}
}

func automationRulesAssertWorkbenchMethods(t *testing.T, script string) {
	t.Helper()
	allowed := map[string]bool{
		"automationAPI": true,
		"escapeHTML":    true,
		"registerView":  true,
		"renderStatus":  true,
		"requestRender": true,
	}
	remaining := script
	for {
		index := strings.Index(remaining, "Workbench.")
		if index < 0 {
			return
		}
		remaining = remaining[index+len("Workbench."):]
		end := 0
		for end < len(remaining) {
			character := remaining[end]
			if (character < 'A' || character > 'Z') &&
				(character < 'a' || character > 'z') {
				break
			}
			end++
		}
		method := remaining[:end]
		if !allowed[method] {
			t.Fatalf("automation_rules.js uses unsupported Workbench method %q", method)
		}
		remaining = remaining[end:]
	}
}

func automationRulesAssertScopedCSS(t *testing.T, style string) {
	t.Helper()
	remaining := style
	for {
		open := strings.Index(remaining, "{")
		if open < 0 {
			return
		}
		prefix := remaining[:open]
		if previous := strings.LastIndex(prefix, "}"); previous >= 0 {
			prefix = prefix[previous+1:]
		}
		selector := strings.TrimSpace(prefix)
		if !strings.HasPrefix(selector, "@media") {
			for _, part := range strings.Split(selector, ",") {
				if !strings.HasPrefix(strings.TrimSpace(part), ".automationrules") {
					t.Fatalf("automation_rules.css contains unscoped selector %q", selector)
				}
			}
		}
		remaining = remaining[open+1:]
	}
}

func automationRulesFunction(t *testing.T, script, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("script does not contain function marker %q", startMarker)
	}
	end := strings.Index(script[start:], endMarker)
	if end < 0 {
		t.Fatalf("script does not contain following function marker %q", endMarker)
	}
	return script[start : start+end]
}

func automationRulesReadAsset(t *testing.T, assetPath string) string {
	t.Helper()
	data, err := webAssets.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
