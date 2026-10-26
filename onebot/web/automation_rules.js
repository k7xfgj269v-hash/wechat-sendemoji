(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.automationAPI !== "function") {
    return;
  }

  var AUTOMATION_POLICY_PATH = "/api/automation/policy";
  var R1_TOOL_OPTIONS = [
    ["candidate.create", "创建候选人"],
    ["candidate.update", "更新候选人"],
    ["candidate.link", "关联候选人与消息"],
    ["lead.create", "创建招聘线索"],
    ["lead.transition", "推进早期招聘阶段"],
    ["task.create", "创建跟进任务"],
    ["note.create", "创建内部备注"],
    ["job.update", "更新非薪酬现场需求"],
    ["onboarding.create", "创建入职协同"],
    ["onboarding.update", "更新入职协同"],
    ["onboarding.fleet_update", "更新车队协同"],
    ["service_request.create", "创建低风险服务请求"],
    ["service_request.update", "更新低风险服务请求"]
  ];
  var R2_INTENT_OPTIONS = [
    ["general", "一般沟通"],
    ["scheduling", "时间安排"],
    ["information", "信息回复"]
  ];

  function automationRulesEmptyDraft() {
    return {
      mode: "manual",
      r1AutoApprove: false,
      r1MinConfidence: "0.90",
      r1AllowedTools: [],
      r2PrivateReplyAutoApprove: false,
      r2MinConfidence: "0.98",
      r2AllowedIntents: []
    };
  }

  function automationRulesCreateState() {
    return {
      entered: false,
      policy: null,
      immutableRules: {
        r3: [],
        r4: []
      },
      draft: automationRulesEmptyDraft(),
      dirty: false,
      loading: false,
      saving: false,
      error: "",
      success: ""
    };
  }

  function automationRulesDisabled(value) {
    return value ? " disabled" : "";
  }

  function automationRulesChecked(value) {
    return value ? " checked" : "";
  }

  function automationRulesCopyStrings(value) {
    return Array.isArray(value) ? value.map(function (item) {
      return String(item);
    }) : [];
  }

  function automationRulesDraftFromPolicy(policy) {
    return {
      mode: policy.mode === "automatic" ? "automatic" : "manual",
      r1AutoApprove: !!policy.r1_auto_approve,
      r1MinConfidence: String(policy.r1_min_confidence),
      r1AllowedTools: automationRulesCopyStrings(policy.r1_allowed_tools),
      r2PrivateReplyAutoApprove: !!policy.r2_private_reply_auto_approve,
      r2MinConfidence: String(policy.r2_min_confidence),
      r2AllowedIntents: automationRulesCopyStrings(policy.r2_allowed_intents)
    };
  }

  function automationRulesSameStrings(left, right) {
    if (left.length !== right.length) {
      return false;
    }
    return left.every(function (value, index) {
      return value === right[index];
    });
  }

  function automationRulesDraftMatchesPolicy(s) {
    if (!s.policy) {
      return false;
    }
    var policyDraft = automationRulesDraftFromPolicy(s.policy);
    return s.draft.mode === policyDraft.mode &&
      s.draft.r1AutoApprove === policyDraft.r1AutoApprove &&
      Number(s.draft.r1MinConfidence) === Number(policyDraft.r1MinConfidence) &&
      automationRulesSameStrings(s.draft.r1AllowedTools, policyDraft.r1AllowedTools) &&
      s.draft.r2PrivateReplyAutoApprove === policyDraft.r2PrivateReplyAutoApprove &&
      Number(s.draft.r2MinConfidence) === Number(policyDraft.r2MinConfidence) &&
      automationRulesSameStrings(s.draft.r2AllowedIntents, policyDraft.r2AllowedIntents);
  }

  function automationRulesNormalizeEnvelope(envelope) {
    if (!envelope || !envelope.policy || typeof envelope.policy !== "object") {
      throw new Error("策略响应缺少 policy");
    }
    var immutable = envelope.immutable_rules;
    if (!immutable || typeof immutable !== "object") {
      throw new Error("策略响应缺少 immutable_rules");
    }
    return {
      policy: envelope.policy,
      immutableRules: {
        r3: Array.isArray(immutable.r3) ? immutable.r3.slice() : [],
        r4: Array.isArray(immutable.r4) ? immutable.r4.slice() : []
      }
    };
  }

  function automationRulesApplyEnvelope(s, envelope, replaceDraft) {
    var normalized = automationRulesNormalizeEnvelope(envelope);
    var hadPolicy = !!s.policy;
    s.policy = normalized.policy;
    s.immutableRules = normalized.immutableRules;
    if (replaceDraft || !hadPolicy || !s.dirty) {
      s.draft = automationRulesDraftFromPolicy(normalized.policy);
      s.dirty = false;
    }
  }

  function automationRulesFormatUpdatedAt(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return value || "未知";
    }
    return date.toLocaleString("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false
    });
  }

  function automationRulesOptionList(name, options, selected, disabled) {
    return '<div class="automationRules-options">' + options.map(function (option) {
      var value = option[0];
      return '<label class="automationRules-option"><input type="checkbox" name="' +
        Workbench.escapeHTML(name) + '" value="' + Workbench.escapeHTML(value) + '"' +
        automationRulesChecked(selected.indexOf(value) !== -1) + automationRulesDisabled(disabled) +
        '><span><strong>' + Workbench.escapeHTML(option[1]) + '</strong><small>' +
        Workbench.escapeHTML(value) + "</small></span></label>";
    }).join("") + "</div>";
  }

  function automationRulesRuleText(rule) {
    if (typeof rule === "string") {
      return rule;
    }
    if (rule && typeof rule === "object") {
      if (rule.description) {
        return String(rule.description);
      }
      if (rule.rule) {
        return String(rule.rule);
      }
      if (rule.id) {
        return String(rule.id);
      }
      return JSON.stringify(rule);
    }
    return String(rule == null ? "" : rule);
  }

  function automationRulesImmutableList(risk, items, fallback) {
    var content = items.length ? items.map(function (rule, index) {
      return '<li><span>' + Workbench.escapeHTML(index + 1) + '</span><p>' +
        Workbench.escapeHTML(automationRulesRuleText(rule)) + "</p></li>";
    }).join("") : '<li class="automationRules-emptyRule"><p>' + Workbench.escapeHTML(fallback) + "</p></li>";
    return '<section class="automationRules-ruleGroup" aria-label="' + Workbench.escapeHTML(risk) +
      ' 不可变规则"><div class="automationRules-ruleHeading"><strong>' + Workbench.escapeHTML(risk) +
      '</strong><span>' + Workbench.escapeHTML(risk === "R3" ? "始终人工复核" : "始终拒绝") +
      '</span></div><ol>' + content + "</ol></section>";
  }

  function automationRulesModeOption(value, title, description, selected, disabled) {
    return '<label class="automationRules-modeOption"><input type="radio" name="mode" value="' +
      Workbench.escapeHTML(value) + '"' + automationRulesChecked(selected) +
      automationRulesDisabled(disabled) + '><span><strong>' + Workbench.escapeHTML(title) +
      '</strong><small>' + Workbench.escapeHTML(description) + "</small></span></label>";
  }

  function automationRulesRenderEditor(s) {
    var draft = s.draft;
    var busy = s.loading || s.saving || !s.policy;
    var automatic = draft.mode === "automatic";
    var automaticSettingsDisabled = busy || !automatic;
    var effectiveMode = s.policy && s.policy.mode === "automatic" ? "automatic" : "manual";
    var modeChanged = !!s.policy && draft.mode !== effectiveMode;
    var saveLabel = "保存策略";
    if (modeChanged) {
      saveLabel = automatic ? "保存并启用自动模式" : "保存并切换为手动模式";
    }
    return '<form class="automationRules-form" data-automation-rules-form="policy">' +
      '<section class="automationRules-modeControl" aria-label="自动化运行模式">' +
      '<div class="automationRules-modeHeading"><div><span>运行模式</span><h3>' +
      Workbench.escapeHTML(automatic ? "AI 自动执行，人负责例外审核" : "AI 生成方案，人确认后执行") +
      '</h3><p>' + Workbench.escapeHTML(automatic ?
        "仅符合下方白名单和置信度的低风险动作自动执行，其余动作进入审核。" :
        "AI 继续分析消息并生成动作，但所有有副作用的动作都等待人工确认。") +
      '</p></div><strong data-mode-state="' + Workbench.escapeHTML(modeChanged ? "pending" : "active") + '">' +
      Workbench.escapeHTML(modeChanged ? "待保存" : "当前生效") + "</strong></div>" +
      '<div class="automationRules-modeSwitch" role="radiogroup" aria-label="手动或自动模式">' +
      automationRulesModeOption("manual", "手动模式", "AI 提议，人审核并执行", draft.mode === "manual", busy) +
      automationRulesModeOption("automatic", "自动模式", "低风险动作按策略执行", automatic, busy) +
      "</div>" +
      (modeChanged ? '<p class="automationRules-modeNotice">当前仍为' +
        Workbench.escapeHTML(effectiveMode === "automatic" ? "自动模式" : "手动模式") +
        "；保存后才会切换。</p>" : "") + "</section>" +
      '<div class="automationRules-sectionHeading"><div><h3>自动执行范围</h3><p>' +
      Workbench.escapeHTML(automatic ?
        "配置自动模式允许执行的 R1 与低风险私聊 R2。" :
        "自动模式参数已保留；切换到自动模式后可编辑并生效。") +
      '</p></div><span>' + Workbench.escapeHTML(s.dirty ? "有未保存修改" : "已与服务端同步") + "</span></div>" +
      '<fieldset class="automationRules-fieldset"' + (automatic ? "" : ' aria-disabled="true"') +
      '><legend>R1 内部写操作</legend>' +
      '<label class="automationRules-toggle"><input type="checkbox" name="r1_auto_approve"' +
      automationRulesChecked(draft.r1AutoApprove) + automationRulesDisabled(automaticSettingsDisabled) +
      '><span><strong>允许自动批准</strong><small>仅在自动模式、工具允许且置信度达标时生效。</small></span></label>' +
      '<label class="automationRules-number">最低置信度<input type="number" name="r1_min_confidence" min="0" max="1" step="0.01" inputmode="decimal" value="' +
      Workbench.escapeHTML(draft.r1MinConfidence) + '"' + automationRulesDisabled(automaticSettingsDisabled) + "></label>" +
      '<div class="automationRules-optionHeading"><span>允许的工具</span><small>' +
      Workbench.escapeHTML(draft.r1AllowedTools.length + " / " + R1_TOOL_OPTIONS.length) + "</small></div>" +
      automationRulesOptionList("r1_allowed_tools", R1_TOOL_OPTIONS, draft.r1AllowedTools, automaticSettingsDisabled) +
      "</fieldset>" +
      '<fieldset class="automationRules-fieldset"' + (automatic ? "" : ' aria-disabled="true"') +
      '><legend>R2 私聊回复灰度</legend>' +
      '<label class="automationRules-toggle"><input type="checkbox" name="r2_private_reply_auto_approve"' +
      automationRulesChecked(draft.r2PrivateReplyAutoApprove) + automationRulesDisabled(automaticSettingsDisabled) +
      '><span><strong>允许低风险私聊自动批准</strong><small>仅回复原始私聊对象；可疑消息仍强制复核。</small></span></label>' +
      '<label class="automationRules-number">最低置信度<input type="number" name="r2_min_confidence" min="0" max="1" step="0.01" inputmode="decimal" value="' +
      Workbench.escapeHTML(draft.r2MinConfidence) + '"' + automationRulesDisabled(automaticSettingsDisabled) + "></label>" +
      '<div class="automationRules-optionHeading"><span>允许的回复意图</span><small>' +
      Workbench.escapeHTML(draft.r2AllowedIntents.length + " / " + R2_INTENT_OPTIONS.length) + "</small></div>" +
      automationRulesOptionList("r2_allowed_intents", R2_INTENT_OPTIONS, draft.r2AllowedIntents, automaticSettingsDisabled) +
      "</fieldset>" +
      '<div class="automationRules-actions"><button class="primary" type="submit"' +
      automationRulesDisabled(busy || !s.dirty) + ">" + (s.saving ? "保存并重载中..." : saveLabel) +
      '</button><span>提交版本 ' + Workbench.escapeHTML(s.policy ? s.policy.version : "") + "</span></div></form>";
  }

  function automationRulesRender(s) {
    var policy = s.policy || {};
    return '<div class="automationRules"><section class="topbar automationRules-topbar"><div><h2>自动化规则</h2>' +
      '<p>控制内部写操作与低风险私聊的自动批准边界。</p></div><button type="button" data-automation-rules-action="reload"' +
      automationRulesDisabled(s.loading || s.saving) + ">" + (s.loading ? "加载中..." : "刷新") + "</button></section>" +
      Workbench.renderStatus(s.error, s.success, s.loading ? "正在加载自动化策略..." : "") +
      '<div class="automationRules-meta"><span>当前模式：' +
      Workbench.escapeHTML(policy.mode === "automatic" ? "自动" : policy.mode === "manual" ? "手动" : "未加载") +
      '</span><span>策略版本：' + Workbench.escapeHTML(policy.version || "未加载") +
      '</span><span>更新时间：' + Workbench.escapeHTML(automationRulesFormatUpdatedAt(policy.updated_at)) + "</span></div>" +
      '<section class="automationRules-workspace" aria-label="自动化策略工作区"><div class="automationRules-editor">' +
      automationRulesRenderEditor(s) + '</div><aside class="automationRules-immutable" aria-label="不可变安全规则">' +
      '<div class="automationRules-sectionHeading"><div><h3>强制安全边界</h3><p>这些规则由服务端执行，不能通过策略编辑器降低。</p></div>' +
      '<span>只读</span></div>' +
      automationRulesImmutableList("R3", s.immutableRules.r3, "服务端未返回额外说明；R3 仍始终要求人工复核。") +
      automationRulesImmutableList("R4", s.immutableRules.r4, "服务端未返回额外说明；R4 仍始终拒绝执行。") +
      "</aside></section></div>";
  }

  function automationRulesReadSelection(form, name) {
    return Array.prototype.map.call(form.querySelectorAll('input[name="' + name + '"]:checked'), function (input) {
      return input.value;
    });
  }

  function automationRulesReadForm(s, form) {
    s.draft.mode = form.elements.mode.value;
    s.draft.r1AutoApprove = form.elements.r1_auto_approve.checked;
    s.draft.r1MinConfidence = form.elements.r1_min_confidence.value;
    s.draft.r1AllowedTools = automationRulesReadSelection(form, "r1_allowed_tools");
    s.draft.r2PrivateReplyAutoApprove = form.elements.r2_private_reply_auto_approve.checked;
    s.draft.r2MinConfidence = form.elements.r2_min_confidence.value;
    s.draft.r2AllowedIntents = automationRulesReadSelection(form, "r2_allowed_intents");
  }

  function automationRulesSyncDraft(s, form) {
    automationRulesReadForm(s, form);
    s.dirty = !automationRulesDraftMatchesPolicy(s);
  }

  function automationRulesConfidence(value, label) {
    var text = String(value == null ? "" : value).trim();
    var number = Number(text);
    if (!text || !Number.isFinite(number) || number < 0 || number > 1) {
      throw new Error(label + "必须是 0 到 1 之间的数字");
    }
    return number;
  }

  function automationRulesValidatedSelection(values, options, label) {
    var allowed = options.map(function (option) {
      return option[0];
    });
    for (var index = 0; index < values.length; index += 1) {
      if (allowed.indexOf(values[index]) === -1) {
        throw new Error(label + "包含不支持的选项");
      }
    }
    return allowed.filter(function (value) {
      return values.indexOf(value) !== -1;
    });
  }

  function automationRulesPayload(s) {
    if (!s.policy) {
      throw new Error("策略尚未加载");
    }
    if (s.draft.mode !== "manual" && s.draft.mode !== "automatic") {
      throw new Error("运行模式无效");
    }
    var version = Number(s.policy.version);
    if (!Number.isSafeInteger(version) || version < 1) {
      throw new Error("策略版本无效，请刷新后重试");
    }
    return {
      expected_version: version,
      mode: s.draft.mode,
      r1_auto_approve: s.draft.r1AutoApprove,
      r1_min_confidence: automationRulesConfidence(s.draft.r1MinConfidence, "R1 最低置信度"),
      r1_allowed_tools: automationRulesValidatedSelection(s.draft.r1AllowedTools, R1_TOOL_OPTIONS, "R1 工具列表"),
      r2_private_reply_auto_approve: s.draft.r2PrivateReplyAutoApprove,
      r2_min_confidence: automationRulesConfidence(s.draft.r2MinConfidence, "R2 最低置信度"),
      r2_allowed_intents: automationRulesValidatedSelection(s.draft.r2AllowedIntents, R2_INTENT_OPTIONS, "R2 意图列表")
    };
  }

  function automationRulesLoad(s, replaceDraft) {
    if (s.loading || s.saving) {
      return null;
    }
    s.loading = true;
    s.error = "";
    s.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(AUTOMATION_POLICY_PATH).then(function (envelope) {
      automationRulesApplyEnvelope(s, envelope, replaceDraft);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.loading = false;
      Workbench.requestRender();
    });
  }

  function automationRulesSave(s, form) {
    automationRulesSyncDraft(s, form);
    var body;
    try {
      body = automationRulesPayload(s);
    } catch (err) {
      s.error = err.message;
      s.success = "";
      Workbench.requestRender();
      return;
    }

    var persisted = false;
    s.saving = true;
    s.error = "";
    s.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(AUTOMATION_POLICY_PATH, {
      method: "PUT",
      body: body
    }).then(function (envelope) {
      automationRulesNormalizeEnvelope(envelope);
      persisted = true;
      return Workbench.automationAPI(AUTOMATION_POLICY_PATH);
    }).then(function (envelope) {
      automationRulesApplyEnvelope(s, envelope, true);
      s.success = "策略已保存并从服务端重新加载。";
    }).catch(function (err) {
      s.error = persisted ? "策略已保存，但权威重载失败：" + err.message : err.message;
    }).finally(function () {
      s.saving = false;
      Workbench.requestRender();
    });
  }

  function automationRulesBind(root, s) {
    var form = root.querySelector('[data-automation-rules-form="policy"]');
    if (form) {
      form.addEventListener("input", function (event) {
        automationRulesSyncDraft(s, form);
        if (!event.target || event.target.type !== "number") {
          Workbench.requestRender();
        }
      });
      form.addEventListener("change", function () {
        automationRulesSyncDraft(s, form);
        Workbench.requestRender();
      });
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        automationRulesSave(s, form);
      });
    }

    var reload = root.querySelector('[data-automation-rules-action="reload"]');
    if (reload) {
      reload.addEventListener("click", function () {
        automationRulesLoad(s, false);
      });
    }
  }

  function automationRulesOnEnter(s) {
    if (s.entered) {
      return null;
    }
    s.entered = true;
    return automationRulesLoad(s, true);
  }

  Workbench.registerView({
    id: "automation-rules",
    label: "自动化规则",
    section: "AI 中心",
    order: 80,
    createState: automationRulesCreateState,
    render: automationRulesRender,
    onEnter: automationRulesOnEnter,
    bind: automationRulesBind
  });
})();
