(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.automationAPI !== "function") {
    return;
  }

  var PROVIDER_PATH = "/api/automation/provider";
  var PROVIDER_TEST_PATH = "/api/automation/provider/test";
  var AUDIENCES_PATH = "/api/automation/audiences";
  var PROMPTS_PATH = "/api/automation/prompts";

  function createState() {
    return {
      entered: false,
      config: null,
      draft: emptyProviderDraft(),
      keyDraft: "",
      clearKey: false,
      audienceDraft: emptyAudienceDraft(),
      editingAudienceId: "",
      promptRevisions: [],
      promptReason: "",
      compareFrom: "",
      compareTo: "",
      comparison: null,
      evaluation: null,
      promptBusyId: "",
      loading: false,
      saving: false,
      testing: false,
      audienceSaving: false,
      deletingAudienceId: "",
      error: "",
      success: ""
    };
  }

  function emptyProviderDraft() {
    return {
      enabled: false,
      protocol: "chat_completions",
      endpoint: "",
      model: "",
      gateModel: "",
      timeoutSeconds: "90",
      promptTemplate: "",
      gatePromptTemplate: ""
    };
  }

  function emptyAudienceDraft() {
    return {
      label: "",
      chatType: "private",
      peerId: "",
      allowedIntents: "",
      approvedContent: "",
      expiresAt: "",
      perHourLimit: "1",
      perRunRecipientCap: "1",
      enabled: true,
      version: 0
    };
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function checked(value) {
    return value ? " checked" : "";
  }

  function selected(value, current) {
    return value === current ? " selected" : "";
  }

  function applyConfig(state, config) {
    if (!config || typeof config !== "object" || !Array.isArray(config.audiences)) {
      throw new Error("配置响应格式无效");
    }
    state.config = config;
    state.draft = {
      enabled: !!config.enabled,
      protocol: String(config.protocol || "chat_completions"),
      endpoint: String(config.endpoint || ""),
      model: String(config.model || ""),
      gateModel: String(config.gate_model || ""),
      timeoutSeconds: String(config.timeout_seconds || 90),
      promptTemplate: String(config.prompt_template || ""),
      gatePromptTemplate: String(config.gate_prompt_template || "")
    };
    state.keyDraft = "";
    state.clearKey = false;
  }

  function applyPromptHistory(state, envelope) {
    if (!envelope || !Array.isArray(envelope.revisions)) {
      throw new Error("Prompt 历史响应格式无效");
    }
    state.promptRevisions = envelope.revisions;
    if (!state.compareFrom && envelope.revisions.length) {
      state.compareFrom = envelope.revisions[0].id;
    }
    if (!state.compareTo && envelope.revisions.length) {
      state.compareTo = envelope.active_revision_id || envelope.revisions[envelope.revisions.length - 1].id;
    }
  }

  function readProviderForm(state, form) {
    state.draft.enabled = form.elements.enabled.checked;
    state.draft.protocol = form.elements.protocol.value;
    state.draft.endpoint = form.elements.endpoint.value;
    state.draft.model = form.elements.model.value;
    state.draft.gateModel = form.elements.gate_model.value;
    state.draft.timeoutSeconds = form.elements.timeout_seconds.value;
    state.draft.promptTemplate = form.elements.prompt_template.value;
    state.draft.gatePromptTemplate = form.elements.gate_prompt_template.value;
    state.promptReason = form.elements.prompt_reason.value;
    state.keyDraft = form.elements.api_key.value;
    state.clearKey = form.elements.clear_api_key.checked;
  }

  function providerPayload(state, includeVersion) {
    var timeout = Number(state.draft.timeoutSeconds);
    if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > 300) {
      throw new Error("超时时间必须是 1 到 300 秒");
    }
    var body = {
      protocol: state.draft.protocol,
      endpoint: state.draft.endpoint.trim(),
      model: state.draft.model.trim(),
      gate_model: state.draft.gateModel.trim(),
      timeout_seconds: timeout,
      prompt_template: state.draft.promptTemplate.trim(),
      gate_prompt_template: state.draft.gatePromptTemplate.trim()
    };
    if (includeVersion) {
      if (!state.config || !Number.isSafeInteger(Number(state.config.version))) {
        throw new Error("配置版本无效，请刷新后重试");
      }
      body.expected_version = Number(state.config.version);
      body.enabled = state.draft.enabled;
      body.created_by = "operator";
      body.reason = state.promptReason.trim();
    }
    if (state.clearKey) {
      body.clear_api_key = true;
    } else if (state.keyDraft.trim()) {
      body.api_key = state.keyDraft.trim();
    }
    return body;
  }

  function splitIntents(value) {
    var seen = Object.create(null);
    return String(value || "").split(/[\n,]/).map(function (item) {
      return item.trim();
    }).filter(function (item) {
      if (!item || seen[item]) {
        return false;
      }
      seen[item] = true;
      return true;
    });
  }

  function localDateTimeValue(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return "";
    }
    var offset = date.getTimezoneOffset() * 60000;
    return new Date(date.getTime() - offset).toISOString().slice(0, 16);
  }

  function isoDateTimeValue(value) {
    if (!String(value || "").trim()) {
      return null;
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      throw new Error("受众作用域过期时间无效");
    }
    return date.toISOString();
  }

  function renderProvider(state) {
    var busy = state.loading || state.saving || state.testing;
    var configured = state.config && state.config.api_key_configured;
    return '<form class="automationConfig-provider" data-automation-config-form="provider">' +
      '<div class="automationConfig-sectionHead"><div><h3>Provider</h3><p>OpenAI-compatible protocol and endpoint</p></div>' +
      '<span data-tone="' + (state.draft.enabled ? "active" : "muted") + '">' +
      Workbench.escapeHTML(state.draft.enabled ? "Enabled" : "Disabled") + "</span></div>" +
      renderProviderReadiness(state) +
      '<label class="automationConfig-toggle"><input type="checkbox" name="enabled"' +
      checked(state.draft.enabled) + disabled(busy) + '><span><strong>启用 AI provider</strong></span></label>' +
      '<div class="automationConfig-fields"><label>Protocol<select name="protocol"' + disabled(busy) +
      '><option value="chat_completions"' + selected("chat_completions", state.draft.protocol) +
      '>Chat Completions</option><option value="responses"' + selected("responses", state.draft.protocol) +
      '>Responses</option></select></label><label>Endpoint<input name="endpoint" type="url" maxlength="2048" autocomplete="off" value="' +
      Workbench.escapeHTML(state.draft.endpoint) + '" placeholder="OpenAI-compatible endpoint"' + disabled(busy) +
      '></label><label>Model<input name="model" maxlength="200" autocomplete="off" value="' +
      Workbench.escapeHTML(state.draft.model) + '"' + disabled(busy) +
      '></label><label>Gate model<input name="gate_model" maxlength="200" autocomplete="off" value="' +
      Workbench.escapeHTML(state.draft.gateModel) + '" placeholder="留空则使用主 Model"' + disabled(busy) +
      '><small class="automationConfig-fieldHelp">复用 Provider 的 protocol、endpoint、API key 与 timeout。</small>' +
      '</label><label>Timeout (seconds)<input name="timeout_seconds" type="number" min="1" max="300" step="1" value="' +
      Workbench.escapeHTML(state.draft.timeoutSeconds) + '"' + disabled(busy) + "></label>" +
      '<label class="automationConfig-secret">API key<input name="api_key" type="password" maxlength="16384" autocomplete="new-password" value="' +
      Workbench.escapeHTML(state.keyDraft) + '" placeholder="' +
      Workbench.escapeHTML(configured ? "已配置；留空则保持不变" : "未配置") + '"' + disabled(busy) +
      '><small>macOS 使用系统 Keychain；已保存值不会回显或导出。</small></label></div>' +
      '<label class="automationConfig-toggle"><input type="checkbox" name="clear_api_key"' +
      checked(state.clearKey) + disabled(busy || !configured) +
      '><span><strong>删除已保存的 API key</strong></span></label>' +
      '<label class="automationConfig-prompt">Planner prompt<textarea name="prompt_template" maxlength="65536" rows="12"' +
      disabled(busy) + ">" + Workbench.escapeHTML(state.draft.promptTemplate) + "</textarea></label>" +
      '<label class="automationConfig-prompt">Gate prompt<textarea name="gate_prompt_template" maxlength="65536" rows="8" required' +
      disabled(busy) + ">" + Workbench.escapeHTML(state.draft.gatePromptTemplate) +
      '</textarea><small class="automationConfig-fieldHelp">用于判断当前输入是否应进入 Planner。</small></label>' +
      '<label>Revision reason<input name="prompt_reason" maxlength="240" value="' +
      Workbench.escapeHTML(state.promptReason) + '" placeholder="说明本次 prompt 修改"' + disabled(busy) + "></label>" +
      '<div class="automationConfig-actions"><button class="primary" type="submit"' + disabled(busy || !state.config) + ">" +
      (state.saving ? "保存中..." : "保存并热重载") + '</button><button type="button" data-automation-config-action="test"' +
      disabled(busy) + ">" + (state.testing ? "测试中..." : "测试连接") +
      '</button><button type="button" data-automation-config-action="reload"' + disabled(busy) + ">刷新</button></div></form>";
  }

  function readinessLabel(status) {
    var labels = {
      disabled: "已停用",
      missing_endpoint: "缺少 Endpoint",
      missing_model: "缺少 Model",
      missing_api_key: "缺少 API key",
      ready: "可用",
      test_failed: "最近测试失败"
    };
    return labels[status] || status || "未知";
  }

  function configuredLabel(value) {
    return value ? "已配置" : "未配置";
  }

  function renderProviderReadiness(state) {
    var readiness = state.config && state.config.readiness || {};
    var latest = readiness.latest_connection_test || null;
    var latestHTML = latest ? '<div class="automationConfig-latestTest"><strong>最近连接测试</strong><span>' +
      Workbench.escapeHTML((latest.status || "未知") + " · " + (latest.protocol || "未知协议")) +
      '</span><time>' + Workbench.escapeHTML(latest.tested_at || "无时间") + '</time><span>' +
      Workbench.escapeHTML(latest.matches_current_config ? "匹配当前配置" : "不匹配当前配置") + '</span>' +
      (latest.error ? '<pre>' + Workbench.escapeHTML(latest.error) + "</pre>" : "") + "</div>" :
      '<div class="automationConfig-latestTest"><strong>最近连接测试</strong><span>尚未测试</span></div>';
    return '<section class="automationConfig-readiness" aria-label="Provider readiness">' +
      '<div><strong>' + Workbench.escapeHTML(readinessLabel(readiness.status)) + '</strong><span>' +
      Workbench.escapeHTML(readiness.protocol || state.draft.protocol) + "</span></div>" +
      '<dl><dt>Endpoint</dt><dd>' + Workbench.escapeHTML(configuredLabel(readiness.endpoint_configured)) +
      '</dd><dt>Model</dt><dd>' + Workbench.escapeHTML(configuredLabel(readiness.model_configured)) +
      '</dd><dt>API key</dt><dd>' + Workbench.escapeHTML(configuredLabel(readiness.api_key_configured)) +
      "</dd></dl>" + latestHTML + "</section>";
  }

  function renderAudienceForm(state) {
    var draft = state.audienceDraft;
    var busy = state.audienceSaving || !!state.deletingAudienceId;
    return '<form class="automationConfig-audienceForm" data-automation-config-form="audience">' +
      '<div class="automationConfig-sectionHead"><div><h3>' +
      Workbench.escapeHTML(state.editingAudienceId ? "编辑受众" : "新增受众") +
      '</h3><p>External reply target whitelist</p></div></div>' +
      '<label>名称<input name="label" maxlength="120" value="' + Workbench.escapeHTML(draft.label) +
      '"' + disabled(busy) + '></label><div class="automationConfig-audienceGrid"><label>类型<select name="chat_type"' +
      disabled(busy) + '><option value="private"' + selected("private", draft.chatType) +
      '>私聊</option><option value="group"' + selected("group", draft.chatType) +
      '>群聊</option></select></label><label>Peer ID<input name="peer_id" maxlength="256" value="' +
      Workbench.escapeHTML(draft.peerId) + '"' + disabled(busy) + "></label></div>" +
      '<label>允许意图<textarea name="allowed_intents" rows="4" placeholder="general&#10;scheduling"' +
      disabled(busy) + ">" + Workbench.escapeHTML(draft.allowedIntents) + "</textarea></label>" +
      '<label>批准内容<textarea name="approved_content" rows="4" maxlength="4000" placeholder="仅此规范化文本可进入自动审批"' +
      disabled(busy) + ">" + Workbench.escapeHTML(draft.approvedContent) + "</textarea></label>" +
      '<div class="automationConfig-scopeGrid"><label>过期时间<input name="expires_at" type="datetime-local" value="' +
      Workbench.escapeHTML(draft.expiresAt) + '"' + disabled(busy) +
      '></label><label>每小时上限<input name="per_hour_limit" type="number" min="1" max="1000" value="' +
      Workbench.escapeHTML(draft.perHourLimit) + '"' + disabled(busy) +
      '></label><label>单次收件人上限<input name="per_run_recipient_cap" type="number" min="1" max="100" value="' +
      Workbench.escapeHTML(draft.perRunRecipientCap) + '"' + disabled(busy) + "></label></div>" +
      '<label class="automationConfig-toggle"><input type="checkbox" name="audience_enabled"' +
      checked(draft.enabled) + disabled(busy) + '><span><strong>启用该受众</strong></span></label>' +
      '<div class="automationConfig-actions"><button class="primary" type="submit"' +
      disabled(busy || !state.config) + ">" + (state.audienceSaving ? "保存中..." :
        state.editingAudienceId ? "保存修改" : "添加受众") +
      '</button><button type="button" data-automation-config-action="cancel-audience"' +
      disabled(busy) + ">清空</button></div></form>";
  }

  function renderAudienceList(state) {
    var audiences = state.config && Array.isArray(state.config.audiences) ? state.config.audiences : [];
    if (!audiences.length) {
      return '<div class="automationConfig-empty" role="status">暂无外部受众。</div>';
    }
    return '<div class="automationConfig-audienceList" role="list">' + audiences.map(function (audience) {
      var intents = Array.isArray(audience.allowed_intents) ? audience.allowed_intents : [];
      return '<article class="automationConfig-audience" role="listitem"><div class="automationConfig-audienceTitle"><div><strong>' +
        Workbench.escapeHTML(audience.label) + '</strong><span>' +
        Workbench.escapeHTML(audience.chat_type === "group" ? "群聊" : "私聊") +
        '</span></div><span data-tone="' + (audience.enabled ? "active" : "muted") + '">' +
        Workbench.escapeHTML(audience.enabled ? "Enabled" : "Disabled") + "</span></div>" +
        '<code>' + Workbench.escapeHTML(audience.peer_id) + "</code><div class=\"automationConfig-intents\">" +
        (intents.length ? intents.map(function (intent) {
          return "<span>" + Workbench.escapeHTML(intent) + "</span>";
        }).join("") : "<span>无自动回复意图</span>") + '</div><div class="automationConfig-rowActions">' +
        '<small>' + Workbench.escapeHTML(audience.content_sha256 ?
          "作用域有效至 " + new Date(audience.expires_at).toLocaleString() +
          " · " + audience.per_hour_limit + "/小时 · 单次 " + audience.per_run_recipient_cap :
          "未配置自动审批内容作用域") + "</small>" +
        '<button type="button" data-automation-config-edit="' + Workbench.escapeHTML(audience.id) + '">编辑</button>' +
        '<button type="button" data-automation-config-delete="' + Workbench.escapeHTML(audience.id) + '"' +
        disabled(state.deletingAudienceId === audience.id) + ">" +
        (state.deletingAudienceId === audience.id ? "删除中..." : "删除") + "</button></div></article>";
    }).join("") + "</div>";
  }

  function renderPromptHistory(state) {
    var revisions = state.promptRevisions.slice().reverse();
    if (!revisions.length) {
      return '<section class="automationConfig-prompts"><h3>Prompt revisions</h3>' +
        '<div class="automationConfig-empty">暂无 prompt revision。</div></section>';
    }
    var options = state.promptRevisions.map(function (revision) {
      return '<option value="' + Workbench.escapeHTML(revision.id) + '">' +
        Workbench.escapeHTML("#" + revision.number + " · " + revision.reason) + "</option>";
    }).join("");
    var comparison = state.comparison ? '<div class="automationConfig-comparison"><pre>' +
      Workbench.escapeHTML(state.comparison.from.template) + '</pre><pre>' +
      Workbench.escapeHTML(state.comparison.to.template) + "</pre></div>" : "";
    var evaluation = state.evaluation ? '<div class="automationConfig-evaluation"><strong>' +
      Workbench.escapeHTML("Synthetic evaluation: " + state.evaluation.passed + " passed / " +
        state.evaluation.failed + " failed") + '</strong><div>' +
      state.evaluation.results.map(function (result) {
        return '<span data-tone="' + (result.passed ? "active" : "warning") + '">' +
          Workbench.escapeHTML(result.name) + "</span>";
      }).join("") + "</div></div>" : "";
    return '<section class="automationConfig-prompts"><div class="automationConfig-sectionHead"><div>' +
      '<h3>Prompt revisions</h3><p>Immutable history and synthetic policy checks</p></div></div>' +
      '<div class="automationConfig-compareControls"><label>From<select data-automation-prompt-compare="from">' +
      options.replace('value="' + state.compareFrom + '"', 'value="' + state.compareFrom + '" selected') +
      '</select></label><label>To<select data-automation-prompt-compare="to">' +
      options.replace('value="' + state.compareTo + '"', 'value="' + state.compareTo + '" selected') +
      '</select></label><button type="button" data-automation-prompt-action="compare">比较</button></div>' +
      comparison + evaluation + '<div class="automationConfig-revisionList">' + revisions.map(function (revision) {
        var active = state.config && state.config.active_prompt_revision_id === revision.id;
        var busy = state.promptBusyId === revision.id;
        return '<article class="automationConfig-revision"><div><strong>' +
          Workbench.escapeHTML("#" + revision.number + " · " + revision.reason) +
          '</strong><span>' + Workbench.escapeHTML(revision.created_by + " · " +
            new Date(revision.created_at).toLocaleString()) + '</span></div><code>' +
          Workbench.escapeHTML(revision.sha256.slice(0, 16)) + '</code><div class="automationConfig-rowActions">' +
          '<button type="button" data-automation-prompt-evaluate="' + Workbench.escapeHTML(revision.id) + '"' +
          disabled(busy) + ">评估</button>" +
          '<button type="button" data-automation-prompt-activate="' + Workbench.escapeHTML(revision.id) + '"' +
          disabled(busy || active) + ">" + (active ? "当前版本" : "激活") + "</button>" +
          '<button type="button" data-automation-prompt-rollback="' + Workbench.escapeHTML(revision.id) + '"' +
          disabled(busy || active) + ">回滚到此版本</button></div></article>";
      }).join("") + "</div></section>";
  }

  function render(state) {
    var version = state.config ? state.config.version : "-";
    return '<div class="automationConfig"><section class="topbar automationConfig-topbar"><div><h2>AI 配置</h2>' +
      '<p>Provider、planner prompt 与外部受众白名单</p></div><span>Version ' +
      Workbench.escapeHTML(version) + "</span></section>" +
      Workbench.renderStatus(state.error, state.success, state.loading ? "正在加载 AI 配置..." : "") +
      '<div class="automationConfig-layout"><section class="automationConfig-main">' +
      renderProvider(state) + renderPromptHistory(state) + '</section><aside class="automationConfig-audiences">' +
      renderAudienceForm(state) + renderAudienceList(state) + "</aside></div></div>";
  }

  function load(state, message, preserveDraft) {
    if (state.loading) {
      return null;
    }
    var providerDraft = state.draft;
    var keyDraft = state.keyDraft;
    var clearKey = state.clearKey;
    var audienceDraft = state.audienceDraft;
    var editingAudienceId = state.editingAudienceId;
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Promise.all([
      Workbench.automationAPI(PROVIDER_PATH),
      Workbench.automationAPI(PROMPTS_PATH)
    ]).then(function (responses) {
      applyConfig(state, responses[0]);
      applyPromptHistory(state, responses[1]);
      if (preserveDraft) {
        state.draft = providerDraft;
        state.keyDraft = keyDraft;
        state.clearKey = clearKey;
        state.audienceDraft = audienceDraft;
        state.editingAudienceId = editingAudienceId;
      }
      if (message) {
        state.success = message;
      }
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function staleReload(state, err) {
    if (err && err.status === 409) {
      return load(state, "配置已重新加载，未保存输入已保留。", true);
    }
    state.error = err.message;
    return null;
  }

  function saveProvider(state, form) {
    readProviderForm(state, form);
    var body;
    try {
      body = providerPayload(state, true);
    } catch (err) {
      state.error = err.message;
      Workbench.requestRender();
      return;
    }
    state.saving = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(PROVIDER_PATH, { method: "PUT", body: body }).then(function (config) {
      applyConfig(state, config);
      state.promptReason = "";
      return Workbench.automationAPI(PROMPTS_PATH).then(function (history) {
        applyPromptHistory(state, history);
        state.success = "Provider 配置已保存并热重载。";
      });
    }).catch(function (err) {
      return staleReload(state, err);
    }).finally(function () {
      state.saving = false;
      Workbench.requestRender();
    });
  }

  function testProvider(state, form) {
    readProviderForm(state, form);
    var body;
    try {
      body = providerPayload(state, false);
    } catch (err) {
      state.error = err.message;
      Workbench.requestRender();
      return;
    }
    state.testing = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(PROVIDER_TEST_PATH, { method: "POST", body: body }).then(function () {
      state.keyDraft = "";
      return load(state, "连接测试成功。", true);
    }).catch(function (err) {
      state.keyDraft = "";
      return load(state, "连接测试失败，已刷新 readiness。", true).then(function () {
        state.error = err.message;
      });
    }).finally(function () {
      state.testing = false;
      Workbench.requestRender();
    });
  }

  function readAudienceForm(state, form) {
    state.audienceDraft.label = form.elements.label.value;
    state.audienceDraft.chatType = form.elements.chat_type.value;
    state.audienceDraft.peerId = form.elements.peer_id.value;
    state.audienceDraft.allowedIntents = form.elements.allowed_intents.value;
    state.audienceDraft.approvedContent = form.elements.approved_content.value;
    state.audienceDraft.expiresAt = form.elements.expires_at.value;
    state.audienceDraft.perHourLimit = form.elements.per_hour_limit.value;
    state.audienceDraft.perRunRecipientCap = form.elements.per_run_recipient_cap.value;
    state.audienceDraft.enabled = form.elements.audience_enabled.checked;
  }

  function resetAudience(state) {
    state.editingAudienceId = "";
    state.audienceDraft = emptyAudienceDraft();
  }

  function saveAudience(state, form) {
    readAudienceForm(state, form);
    var expiresAt;
    var perHourLimit = Number(state.audienceDraft.perHourLimit);
    var perRunRecipientCap = Number(state.audienceDraft.perRunRecipientCap);
    try {
      expiresAt = isoDateTimeValue(state.audienceDraft.expiresAt);
    } catch (err) {
      state.error = err.message;
      Workbench.requestRender();
      return;
    }
    if (state.audienceDraft.approvedContent.trim() &&
      (!Number.isSafeInteger(perHourLimit) || !Number.isSafeInteger(perRunRecipientCap))) {
      state.error = "受众作用域上限必须是整数";
      Workbench.requestRender();
      return;
    }
    var body = {
      label: state.audienceDraft.label.trim(),
      chat_type: state.audienceDraft.chatType,
      peer_id: state.audienceDraft.peerId.trim(),
      allowed_intents: splitIntents(state.audienceDraft.allowedIntents),
      approved_content: state.audienceDraft.approvedContent.trim(),
      expires_at: expiresAt,
      per_hour_limit: state.audienceDraft.approvedContent.trim() ? perHourLimit : 0,
      per_run_recipient_cap: state.audienceDraft.approvedContent.trim() ? perRunRecipientCap : 0,
      enabled: state.audienceDraft.enabled
    };
    var path = AUDIENCES_PATH;
    var method = "POST";
    if (state.editingAudienceId) {
      path += "/" + encodeURIComponent(state.editingAudienceId);
      method = "PATCH";
      body.expected_version = Number(state.audienceDraft.version);
    } else {
      body.expected_version = Number(state.config.version);
    }
    state.audienceSaving = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(path, { method: method, body: body }).then(function (envelope) {
      applyConfig(state, envelope.provider);
      resetAudience(state);
      state.success = method === "POST" ? "受众已添加。" : "受众已更新。";
    }).catch(function (err) {
      return staleReload(state, err);
    }).finally(function () {
      state.audienceSaving = false;
      Workbench.requestRender();
    });
  }

  function editAudience(state, id) {
    var audience = state.config.audiences.find(function (item) {
      return item.id === id;
    });
    if (!audience) {
      return;
    }
    state.editingAudienceId = audience.id;
    state.audienceDraft = {
      label: audience.label || "",
      chatType: audience.chat_type || "private",
      peerId: audience.peer_id || "",
      allowedIntents: Array.isArray(audience.allowed_intents) ? audience.allowed_intents.join("\n") : "",
      approvedContent: audience.approved_content || "",
      expiresAt: localDateTimeValue(audience.expires_at),
      perHourLimit: String(audience.per_hour_limit || 1),
      perRunRecipientCap: String(audience.per_run_recipient_cap || 1),
      enabled: !!audience.enabled,
      version: audience.version
    };
    state.error = "";
    state.success = "";
    Workbench.requestRender();
  }

  function deleteAudience(state, id) {
    var audience = state.config.audiences.find(function (item) {
      return item.id === id;
    });
    if (!audience || !window.confirm("确认删除受众“" + audience.label + "”？")) {
      return;
    }
    state.deletingAudienceId = id;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(AUDIENCES_PATH + "/" + encodeURIComponent(id), {
      method: "DELETE",
      body: { expected_version: Number(audience.version) }
    }).then(function (envelope) {
      applyConfig(state, envelope.provider);
      if (state.editingAudienceId === id) {
        resetAudience(state);
      }
      state.success = "受众已删除。";
    }).catch(function (err) {
      return staleReload(state, err);
    }).finally(function () {
      state.deletingAudienceId = "";
      Workbench.requestRender();
    });
  }

  function activatePrompt(state, id, rollback) {
    if (!state.config || state.promptBusyId) {
      return;
    }
    if (rollback && !window.confirm("确认回滚并激活该 prompt revision？")) {
      return;
    }
    state.promptBusyId = id;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(PROMPTS_PATH + "/" + encodeURIComponent(id) + "/" +
      (rollback ? "rollback" : "activate"), {
      method: "POST",
      body: {
        expected_version: Number(state.config.version),
        created_by: "operator",
        reason: rollback ? "prompt rollback" : "prompt activation"
      }
    }).then(function (envelope) {
      applyConfig(state, envelope.provider);
      state.success = rollback ? "Prompt 已回滚并激活。" : "Prompt revision 已激活。";
    }).catch(function (err) {
      return staleReload(state, err);
    }).finally(function () {
      state.promptBusyId = "";
      Workbench.requestRender();
    });
  }

  function evaluatePrompt(state, id) {
    if (state.promptBusyId) {
      return;
    }
    state.promptBusyId = id;
    state.error = "";
    state.success = "";
    state.evaluation = null;
    Workbench.requestRender();
    Workbench.automationAPI(PROMPTS_PATH + "/" + encodeURIComponent(id) + "/evaluate", {
      method: "POST",
      body: { created_by: "operator" }
    }).then(function (evaluation) {
      state.evaluation = evaluation;
      state.success = "合成评估完成，未读取 CRM 或微信数据。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.promptBusyId = "";
      Workbench.requestRender();
    });
  }

  function comparePrompts(state) {
    if (!state.compareFrom || !state.compareTo) {
      return;
    }
    state.error = "";
    state.comparison = null;
    Workbench.requestRender();
    Workbench.automationAPI(PROMPTS_PATH + "/compare?from=" +
      encodeURIComponent(state.compareFrom) + "&to=" + encodeURIComponent(state.compareTo)
    ).then(function (comparison) {
      state.comparison = comparison;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      Workbench.requestRender();
    });
  }

  function bind(root, state) {
    var providerForm = root.querySelector('[data-automation-config-form="provider"]');
    if (providerForm) {
      providerForm.addEventListener("input", function () {
        readProviderForm(state, providerForm);
      });
      providerForm.addEventListener("submit", function (event) {
        event.preventDefault();
        saveProvider(state, providerForm);
      });
    }
    var audienceForm = root.querySelector('[data-automation-config-form="audience"]');
    if (audienceForm) {
      audienceForm.addEventListener("input", function () {
        readAudienceForm(state, audienceForm);
      });
      audienceForm.addEventListener("submit", function (event) {
        event.preventDefault();
        saveAudience(state, audienceForm);
      });
    }
    var test = root.querySelector('[data-automation-config-action="test"]');
    if (test && providerForm) {
      test.addEventListener("click", function () {
        testProvider(state, providerForm);
      });
    }
    var reload = root.querySelector('[data-automation-config-action="reload"]');
    if (reload) {
      reload.addEventListener("click", function () {
        load(state);
      });
    }
    var cancel = root.querySelector('[data-automation-config-action="cancel-audience"]');
    if (cancel) {
      cancel.addEventListener("click", function () {
        resetAudience(state);
        Workbench.requestRender();
      });
    }
    Array.prototype.forEach.call(root.querySelectorAll("[data-automation-config-edit]"), function (button) {
      button.addEventListener("click", function () {
        editAudience(state, button.getAttribute("data-automation-config-edit"));
      });
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-automation-config-delete]"), function (button) {
      button.addEventListener("click", function () {
        deleteAudience(state, button.getAttribute("data-automation-config-delete"));
      });
    });
    var compareFrom = root.querySelector('[data-automation-prompt-compare="from"]');
    if (compareFrom) {
      compareFrom.addEventListener("change", function () {
        state.compareFrom = compareFrom.value;
      });
    }
    var compareTo = root.querySelector('[data-automation-prompt-compare="to"]');
    if (compareTo) {
      compareTo.addEventListener("change", function () {
        state.compareTo = compareTo.value;
      });
    }
    var compare = root.querySelector('[data-automation-prompt-action="compare"]');
    if (compare) {
      compare.addEventListener("click", function () {
        comparePrompts(state);
      });
    }
    Array.prototype.forEach.call(root.querySelectorAll("[data-automation-prompt-evaluate]"), function (button) {
      button.addEventListener("click", function () {
        evaluatePrompt(state, button.getAttribute("data-automation-prompt-evaluate"));
      });
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-automation-prompt-activate]"), function (button) {
      button.addEventListener("click", function () {
        activatePrompt(state, button.getAttribute("data-automation-prompt-activate"), false);
      });
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-automation-prompt-rollback]"), function (button) {
      button.addEventListener("click", function () {
        activatePrompt(state, button.getAttribute("data-automation-prompt-rollback"), true);
      });
    });
  }

  function onEnter(state) {
    if (state.entered) {
      return null;
    }
    state.entered = true;
    return load(state);
  }

  Workbench.registerView({
    id: "automation-config",
    label: "AI 配置",
    section: "AI 中心",
    order: 100,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  });
})();
