(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  var ACTIONS_PATH = "/api/automation/actions";
  var PAGE_SIZE = 50;
  var STATUS_OPTIONS = [
    ["", "全部状态"],
    ["proposed", "已提议"],
    ["awaiting_review", "待审核"],
    ["approved", "已批准"],
    ["executing", "执行中"],
    ["succeeded", "已完成"],
    ["rejected", "已拒绝"],
    ["failed", "失败"],
    ["stale", "已过期"],
    ["superseded", "已替代"],
    ["denied", "策略拒绝"],
    ["unknown", "结果未知"]
  ];
  var TOOL_OPTIONS = [
    ["", "全部工具"],
    ["message.classify", "消息分类"],
    ["candidate.create", "创建候选人"],
    ["candidate.update", "更新候选人"],
    ["candidate.link", "关联候选人"],
    ["job.match", "职位匹配"],
    ["lead.create", "创建 Lead"],
    ["lead.transition", "推进 Lead"],
    ["task.create", "创建任务"],
    ["note.create", "创建备注"],
    ["job.update", "更新现场需求"],
    ["onboarding.create", "创建入职协同"],
    ["onboarding.update", "更新入职协同"],
    ["onboarding.fleet_update", "更新车队协同"],
    ["service_request.create", "创建服务请求"],
    ["service_request.update", "更新服务请求"],
    ["fund_callback.create", "创建资金回访草稿"],
    ["reply.private_text", "原私聊回复"],
    ["reply.audience_text", "白名单受众回复"]
  ];
  var RISK_OPTIONS = [
    ["", "全部风险"],
    ["R0", "R0"],
    ["R1", "R1"],
    ["R2", "R2"],
    ["R3", "R3"],
    ["R4", "R4"]
  ];
  var FORBIDDEN_RETRY_STATUSES = {
    unknown: true,
    sent: true,
    denied: true,
    stale: true,
    superseded: true
  };
  var OPERATION_PATHS = {
    approve: "/approve",
    edit_approve: "/edit_approve",
    reject: "/reject",
    retry: "/retry"
  };

  function createState() {
    return {
      filters: {
        status: "",
        tool: "",
        risk: "",
        source_message_event_id: ""
      },
      limit: PAGE_SIZE,
      offset: 0,
      total: 0,
      actions: [],
      selectedId: "",
      detail: null,
      loaded: false,
      loading: false,
      detailLoading: false,
      mutating: "",
      error: "",
      success: "",
      operatorId: "",
      reason: "",
      editDraft: "{}",
      editDraftActionId: "",
      editDrafts: Object.create(null),
      listRequestSeq: 0,
      detailRequestSeq: 0,
      mutationRequestSeq: 0
    };
  }

  function escapeHTML(value) {
    return Workbench.escapeHTML(value);
  }

  function requestRender() {
    Workbench.requestRender();
  }

  function isObject(value) {
    return !!value && typeof value === "object" && !Array.isArray(value);
  }

  function asArray(value) {
    return Array.isArray(value) ? value : [];
  }

  function hasOwn(object, key) {
    return Object.prototype.hasOwnProperty.call(object, key);
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function selected(value, current) {
    return String(value) === String(current) ? " selected" : "";
  }

  function actionPath(id, suffix) {
    return ACTIONS_PATH + "/" + encodeURIComponent(String(id || "")) + (suffix || "");
  }

  function selectedListAction(state) {
    return state.actions.find(function (action) {
      return action && action.id === state.selectedId;
    }) || null;
  }

  function selectedEnvelope(state) {
    if (
      state.detail &&
      state.detail.action &&
      state.detail.action.id === state.selectedId
    ) {
      return state.detail;
    }
    return null;
  }

  function selectedAction(state) {
    var envelope = selectedEnvelope(state);
    return envelope ? envelope.action : selectedListAction(state);
  }

  function formatTime(value) {
    if (!value) {
      return "无";
    }
    var numeric = Number(value);
    var date;
    if (Number.isFinite(numeric)) {
      if (Math.abs(numeric) < 1000000000000) {
        numeric *= 1000;
      }
      date = new Date(numeric);
    } else {
      date = new Date(String(value));
    }
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function formatConfidence(value) {
    var confidence = Number(value);
    if (!Number.isFinite(confidence)) {
      return String(value == null ? "无" : value);
    }
    return (confidence * 100).toFixed(confidence === 0 || confidence === 1 ? 0 : 1) + "%";
  }

  function jsonText(value) {
    if (typeof value === "string") {
      try {
        return JSON.stringify(JSON.parse(value), null, 2);
      } catch (err) {
        return value;
      }
    }
    try {
      return JSON.stringify(value == null ? {} : value, null, 2);
    } catch (err) {
      return String(value == null ? "" : value);
    }
  }

  function shownValue(value) {
    if (value === null || value === undefined || value === "") {
      return "无";
    }
    if (value === true) {
      return "是";
    }
    if (value === false) {
      return "否";
    }
    if (typeof value === "object") {
      return jsonText(value);
    }
    return String(value);
  }

  function statusLabel(status) {
    var labels = {
      proposed: "已提议",
      awaiting_review: "待审核",
      approved: "已批准",
      executing: "执行中",
      succeeded: "已完成",
      rejected: "已拒绝",
      failed: "失败",
      stale: "已过期",
      superseded: "已替代",
      denied: "策略拒绝",
      unknown: "结果未知"
    };
    return labels[status] || status || "未知状态";
  }

  function outboxStatusLabel(status) {
    var labels = {
      pending: "待投递",
      dispatching: "投递中",
      sent: "已发送",
      failed: "投递失败",
      unknown: "投递结果未知"
    };
    return labels[status] || status || "无";
  }

  function statusTone(status) {
    if (status === "succeeded" || status === "sent") {
      return "success";
    }
    if (
      status === "failed" ||
      status === "denied" ||
      status === "rejected" ||
      status === "unknown"
    ) {
      return "danger";
    }
    if (status === "awaiting_review" || status === "approved" || status === "executing") {
      return "accent";
    }
    return "neutral";
  }

  function actionSchedulerState(action) {
    return String(action && action.scheduler && action.scheduler.state || "pending");
  }

  function actionSchedulerReason(action) {
    return String(action && action.scheduler && action.scheduler.not_ready_reason ||
      action && action.not_ready_reason || "");
  }

  function executionStateLabel(state) {
    return {
      ready: "依赖就绪",
      blocked: "依赖阻塞",
      awaiting_review: "等待审核",
      completed: "已有结果",
      executing: "执行中",
      pending: "等待依赖"
    }[state] || state;
  }

  function executionStateTone(state) {
    if (state === "ready" || state === "completed") {
      return "success";
    }
    if (state === "blocked") {
      return "danger";
    }
    if (state === "awaiting_review" || state === "executing") {
      return "accent";
    }
    return "neutral";
  }

  function renderOptions(options, current) {
    return options.map(function (option) {
      return '<option value="' + escapeHTML(option[0]) + '"' + selected(option[0], current) + ">" +
        escapeHTML(option[1]) + "</option>";
    }).join("");
  }

  function renderBadge(value, tone) {
    return '<span class="automationActions__badge automationActions__badge--' + tone + '">' +
      escapeHTML(value) + "</span>";
  }

  function renderEmpty(message) {
    return '<div class="automationActions__empty" role="status">' + escapeHTML(message) + "</div>";
  }

  function renderFilters(state) {
    var filters = state.filters;
    return '<form class="automationActions__filters" data-automation-actions-form="filters">' +
      '<label>状态<select name="status"' + disabled(state.loading) + ">" +
      renderOptions(STATUS_OPTIONS, filters.status) + "</select></label>" +
      '<label>工具<select name="tool"' + disabled(state.loading) + ">" +
      renderOptions(TOOL_OPTIONS, filters.tool) + "</select></label>" +
      '<label>风险<select name="risk"' + disabled(state.loading) + ">" +
      renderOptions(RISK_OPTIONS, filters.risk) + "</select></label>" +
      '<label class="automationActions__sourceFilter">来源消息 ID<input name="source_message_event_id" autocomplete="off" value="' +
      escapeHTML(filters.source_message_event_id) + '" placeholder="event_..."></label>' +
      '<button class="automationActions__primary" type="submit"' + disabled(state.loading) + '>筛选</button>' +
      "</form>";
  }

  function renderActionList(state) {
    if (state.loading && !state.actions.length) {
      return renderEmpty("正在加载 AI 动作...");
    }
    if (!state.actions.length) {
      return renderEmpty("当前筛选条件下没有 AI 动作。");
    }
    return '<div class="automationActions__list" role="list">' + state.actions.map(function (action) {
      var status = action && action.status || "";
      var executionState = actionSchedulerState(action);
      return '<button class="automationActions__row" type="button" role="listitem" data-automation-actions-id="' +
        escapeHTML(action && action.id || "") + '" aria-current="' + (state.selectedId === (action && action.id) ? "true" : "false") + '">' +
        '<span class="automationActions__rowTop"><strong>' + escapeHTML(action && action.tool || "未知工具") + "</strong>" +
        renderBadge(statusLabel(status), statusTone(status)) + "</span>" +
        '<span class="automationActions__rowMeta"><span>' + escapeHTML(action && action.risk || "无风险级别") +
        '</span><span>' + escapeHTML(executionStateLabel(executionState)) +
        '</span><span>置信度 ' + escapeHTML(formatConfidence(action && action.confidence)) +
        '</span><span>版本 ' + escapeHTML(action && action.version || 0) + "</span></span>" +
        '<span class="automationActions__rowBottom"><span>' +
        escapeHTML(formatTime(action && (action.updated_at || action.created_at))) + '</span><span class="automationActions__mono">' +
        escapeHTML(action && action.id || "") + "</span></span></button>";
    }).join("") + "</div>";
  }

  function renderPager(state) {
    var start = state.total ? state.offset + 1 : 0;
    var end = Math.min(state.total, state.offset + state.actions.length);
    return '<div class="automationActions__pager"><span>' +
      escapeHTML("显示 " + start + "-" + end + " / 共 " + state.total + " 条") +
      '</span><div><button type="button" data-automation-actions-action="previous"' +
      disabled(state.loading || state.offset === 0) + '>上一页</button><button type="button" data-automation-actions-action="next"' +
      disabled(state.loading || state.offset + state.limit >= state.total) + ">下一页</button></div></div>";
  }

  function renderDefinitionList(rows) {
    return '<dl class="automationActions__kv">' + rows.map(function (row) {
      return "<dt>" + escapeHTML(row[0]) + "</dt><dd>" + escapeHTML(shownValue(row[1])) + "</dd>";
    }).join("") + "</dl>";
  }

  function renderSource(envelope, action) {
    var source = isObject(envelope.source_message) ? envelope.source_message : {};
    var reference = isObject(action.source_message) ? action.source_message : {};
    return '<section class="automationActions__section" aria-label="来源消息">' +
      '<div class="automationActions__sectionHead"><h4>来源消息</h4>' +
      renderBadge(source.suspicious || reference.suspicious ? "可疑输入" : "普通输入", source.suspicious || reference.suspicious ? "danger" : "neutral") +
      '</div><div class="automationActions__sourceText">' + escapeHTML(source.text || "无文本内容") + "</div>" +
      renderDefinitionList([
        ["事件 ID", source.id || reference.event_id],
        ["消息键", source.message_key || reference.message_key],
        ["来源", source.source],
        ["聊天类型", source.chat_type || reference.chat_type],
        ["会话对象", source.peer_id || reference.peer_id],
        ["作者", source.author_id],
        ["期望消息版本", reference.expected_version],
        ["当前消息版本", source.version],
        ["消息时间", formatTime(source.timestamp || source.created_at)]
      ]) + "</section>";
  }

  function renderEvidence(action) {
    var evidence = asArray(action.evidence);
    var body = evidence.length ? '<div class="automationActions__evidence">' + evidence.map(function (item) {
      var source = item && item.source || "未知来源";
      var context = [item && item.entity_id, item && item.field].filter(function (value) {
        return value !== null && value !== undefined && value !== "";
      }).join(" / ");
      return '<div class="automationActions__evidenceRow"><div><strong>' + escapeHTML(source) +
        '</strong><span>' + escapeHTML(context || "无附加定位") + '</span></div><blockquote>' +
        escapeHTML(item && item.excerpt || "") + "</blockquote></div>";
    }).join("") + "</div>" : renderEmpty("没有证据条目。");
    return '<section class="automationActions__section" aria-label="证据">' +
      '<div class="automationActions__sectionHead"><h4>证据与置信度</h4><span>' +
      escapeHTML(formatConfidence(action.confidence)) + "</span></div>" + body + "</section>";
  }

  function ruleIDs(action) {
    if (Array.isArray(action.rule_ids)) {
      return action.rule_ids;
    }
    if (Array.isArray(action.policy_rule_ids)) {
      return action.policy_rule_ids;
    }
    return [];
  }

  function renderStringList(items, emptyText) {
    var values = asArray(items);
    if (!values.length) {
      return '<span class="automationActions__muted">' + escapeHTML(emptyText) + "</span>";
    }
    return '<span class="automationActions__chips">' + values.map(function (value) {
      return '<span class="automationActions__chip">' + escapeHTML(shownValue(value)) + "</span>";
    }).join("") + "</span>";
  }

  function renderRisk(action) {
    return '<section class="automationActions__section" aria-label="服务端风险与规则">' +
      '<div class="automationActions__sectionHead"><h4>服务端风险与规则</h4>' +
      renderBadge(action.risk || "无", action.risk === "R3" || action.risk === "R4" ? "danger" : "neutral") +
      "</div>" +
      renderDefinitionList([
        ["策略决策", action.policy_decision],
        ["策略版本", action.policy_version],
        ["动作版本", action.version],
        ["尝试次数", action.attempt],
        ["可重试", action.retryable]
      ]) +
      '<div class="automationActions__labeled"><strong>规则 ID</strong>' +
      renderStringList(ruleIDs(action), "无规则 ID") + "</div>" +
      '<div class="automationActions__labeled"><strong>风险原因</strong>' +
      renderStringList(action.risk_reasons, "无风险原因") + "</div></section>";
  }

  function renderPreconditions(envelope) {
    var checks = asArray(envelope.precondition_checks);
    var body = checks.length ? '<div class="automationActions__tableWrap"><table class="automationActions__table">' +
      "<thead><tr><th>实体</th><th>期望版本</th><th>当前版本</th><th>状态</th></tr></thead><tbody>" +
      checks.map(function (check) {
        return "<tr><td>" + escapeHTML((check && check.entity_type || "未知") + " / " + (check && check.entity_id || "无")) +
          "</td><td>" + escapeHTML(check && check.expected_version) +
          "</td><td>" + escapeHTML(check && check.current_version == null ? "无" : check.current_version) +
          "</td><td>" + renderBadge(check && check.state || "未知", check && check.state === "current" ? "success" : "danger") +
          "</td></tr>";
      }).join("") + "</tbody></table></div>" : renderEmpty("没有实体前置条件。");
    return '<section class="automationActions__section" aria-label="前置条件">' +
      '<div class="automationActions__sectionHead"><h4>前置条件版本</h4><span>' +
      escapeHTML(checks.length + " 项") + "</span></div>" + body + "</section>";
  }

  function renderDependencies(envelope) {
    var dependencies = asArray(envelope.dependencies);
    var body = dependencies.length ? '<div class="automationActions__records">' + dependencies.map(function (dependency) {
      var satisfied = !!(dependency && dependency.satisfied);
      return '<div class="automationActions__record"><div><strong>' +
        escapeHTML(dependency && dependency.tool || "未知工具") + '</strong><span class="automationActions__mono">' +
        escapeHTML(dependency && dependency.action_id || "") + "</span></div>" +
        renderBadge(statusLabel(dependency && dependency.status), statusTone(dependency && dependency.status)) +
        renderBadge(satisfied ? "依赖已满足" : "依赖未满足", satisfied ? "success" : "neutral") +
        (dependency && dependency.not_ready_reason ? '<p class="automationActions__blockedReason">' +
          escapeHTML(dependency.not_ready_reason) + "</p>" : "") +
        '<pre>' + escapeHTML(jsonText(dependency && dependency.result_entity)) + "</pre></div>";
    }).join("") + "</div>" : renderEmpty("没有依赖动作。");
    return '<section class="automationActions__section" aria-label="动作依赖">' +
      '<div class="automationActions__sectionHead"><h4>动作依赖</h4><span>' +
      escapeHTML(dependencies.length + " 项") + "</span></div>" + body + "</section>";
  }

  function renderDiff(envelope) {
    var diff = isObject(envelope.diff) ? envelope.diff : {};
    var changes = asArray(diff.changes);
    var body = changes.length ? '<div class="automationActions__diff">' + changes.map(function (change) {
      return '<div class="automationActions__diffRow"><strong>' + escapeHTML(change && change.field || "未知字段") +
        '</strong><div><span>修改前</span><pre>' + escapeHTML(jsonText(change && change.before)) +
        '</pre></div><div><span>修改后</span><pre>' + escapeHTML(jsonText(change && change.after)) +
        "</pre></div></div>";
    }).join("") + "</div>" : renderEmpty("没有实体或消息差异。");
    return '<section class="automationActions__section" aria-label="动作差异">' +
      '<div class="automationActions__sectionHead"><h4>实体 / 消息差异</h4><span>' +
      escapeHTML([diff.kind, diff.subject_type, diff.subject_id].filter(Boolean).join(" / ") || "无差异主题") +
      "</span></div>" + body + "</section>";
  }

  function renderApprovals(envelope) {
    var approvals = asArray(envelope.approvals);
    var body = approvals.length ? '<div class="automationActions__records">' + approvals.map(function (approval) {
      return '<div class="automationActions__record"><div><strong>' +
        escapeHTML(approval && approval.decision || "未知决定") + '</strong><span class="automationActions__mono">' +
        escapeHTML(approval && approval.id || "") + "</span></div>" +
        '<span>操作员 ' + escapeHTML(approval && approval.operator_id || "无") + "</span>" +
        '<span>动作版本 ' + escapeHTML(approval && approval.action_version || 0) + "</span>" +
        '<p>' + escapeHTML(approval && approval.reason || "无原因") + "</p>" +
        '<time>' + escapeHTML(formatTime(approval && approval.created_at)) + "</time>" +
        (approval && approval.replacement_action_id ? '<span>替代动作 ' +
          escapeHTML(approval.replacement_action_id) + "</span>" : "") + "</div>";
    }).join("") + "</div>" : renderEmpty("没有人工审批记录。");
    return '<section class="automationActions__section" aria-label="审批记录">' +
      '<div class="automationActions__sectionHead"><h4>审批记录</h4><span>' +
      escapeHTML(approvals.length + " 条") + "</span></div>" + body + "</section>";
  }

  function renderOutbox(envelope) {
    var outbox = isObject(envelope.outbox) ? envelope.outbox : null;
    var body = outbox ? renderDefinitionList([
      ["Outbox ID", outbox.id],
      ["状态", outboxStatusLabel(outbox.status)],
      ["聊天类型", outbox.chat_type],
      ["会话对象", outbox.peer_id],
      ["尝试次数", outbox.attempt],
      ["已尝试投递", outbox.dispatch_attempted],
      ["错误代码", outbox.error_code],
      ["错误信息", outbox.error_message],
      ["创建时间", formatTime(outbox.created_at)],
      ["更新时间", formatTime(outbox.updated_at)],
      ["投递开始", formatTime(outbox.dispatch_started_at)],
      ["完成时间", formatTime(outbox.completed_at)]
    ]) + '<div class="automationActions__messagePreview"><strong>发送正文</strong><p>' +
      escapeHTML(outbox.text || "无文本") + "</p></div>" : renderEmpty("该动作没有 Outbox 记录。");
    return '<section class="automationActions__section" aria-label="投递状态">' +
      '<div class="automationActions__sectionHead"><h4>Outbox 投递</h4>' +
      (outbox ? renderBadge(outboxStatusLabel(outbox.status), statusTone(outbox.status)) : "") +
      "</div>" + body + "</section>";
  }

  function renderAudit(envelope) {
    var events = asArray(envelope.audit_events);
    var body = events.length ? '<ol class="automationActions__audit">' + events.map(function (audit) {
      return '<li><div><strong>' + escapeHTML(audit && audit.event_type || "未知事件") +
        '</strong><time>' + escapeHTML(formatTime(audit && audit.created_at)) + "</time></div>" +
        '<span>' + escapeHTML((audit && audit.actor_type || "未知主体") + " / " + (audit && audit.actor_id || "无 ID")) +
        '</span><pre>' + escapeHTML(jsonText(audit && audit.data)) + "</pre></li>";
    }).join("") + "</ol>" : renderEmpty("没有审计事件。");
    return '<section class="automationActions__section" aria-label="审计事件">' +
      '<div class="automationActions__sectionHead"><h4>审计事件</h4><span>' +
      escapeHTML(events.length + " 条") + "</span></div>" + body + "</section>";
  }

  function ensureDraft(state, action) {
    if (!action || !action.id) {
      return;
    }
    if (!hasOwn(state.editDrafts, action.id)) {
      state.editDrafts[action.id] = jsonText(action.payload);
    }
    if (state.editDraftActionId === action.id || state.selectedId === action.id) {
      state.editDraftActionId = action.id;
      state.editDraft = state.editDrafts[action.id];
    }
  }

  function retryEligibility(action, outbox) {
    if (!action || !action.retryable) {
      return { allowed: false, reason: "服务端未标记为可重试。" };
    }
    if (FORBIDDEN_RETRY_STATUSES[action.status]) {
      return { allowed: false, reason: "当前动作状态禁止重试。" };
    }
    if (outbox && outbox.dispatch_attempted) {
      return { allowed: false, reason: "已尝试投递，不能自动重试。" };
    }
    if (outbox && (outbox.status === "sent" || outbox.status === "unknown" || outbox.status === "dispatching")) {
      return { allowed: false, reason: "当前投递结果禁止重试。" };
    }
    return { allowed: true, reason: "" };
  }

  function renderPayloadEditor(state, action) {
    var editable = action.status === "awaiting_review";
    ensureDraft(state, action);
    return '<section class="automationActions__section" aria-label="动作参数">' +
      '<div class="automationActions__sectionHead"><h4>动作参数</h4><span class="automationActions__mono">' +
      escapeHTML(action.tool || "未知工具") + "</span></div>" +
      '<label>编辑 JSON<textarea class="automationActions__editor" name="edit_draft" data-automation-actions-field="editDraft" spellcheck="false"' +
      disabled(!editable || !!state.mutating) + ">" + escapeHTML(state.editDraft) + "</textarea></label>" +
      '<div class="automationActions__jsonGrid"><div><strong>结果实体</strong><pre>' +
      escapeHTML(jsonText(action.result_entity)) + '</pre></div><div><strong>错误</strong><pre>' +
      escapeHTML(jsonText({ code: action.error_code || "", message: action.error_message || "" })) +
      "</pre></div></div></section>";
  }

  function renderOperations(state, envelope, action) {
    var canReview = action.status === "awaiting_review";
    var retry = retryEligibility(action, isObject(envelope.outbox) ? envelope.outbox : null);
    if (!canReview && !retry.allowed) {
      return '<section class="automationActions__section" aria-label="人工操作">' +
        '<div class="automationActions__sectionHead"><h4>人工操作</h4><span>当前无可用操作</span></div>' +
        '<p class="automationActions__muted">' + escapeHTML(retry.reason) + "</p></section>";
    }
    return '<section class="automationActions__section automationActions__operation" aria-label="人工操作">' +
      '<div class="automationActions__sectionHead"><h4>人工操作</h4><span>提交时使用当前动作版本</span></div>' +
      '<div class="automationActions__operatorFields"><label>操作员 ID<input name="operator_id" data-automation-actions-field="operatorId" autocomplete="off" value="' +
      escapeHTML(state.operatorId) + '" placeholder="operator-..."></label>' +
      '<label>原因<textarea name="reason" data-automation-actions-field="reason" placeholder="记录审批或重试依据">' +
      escapeHTML(state.reason) + "</textarea></label></div>" +
      '<div class="automationActions__actionRow">' +
      (canReview ? '<button class="automationActions__primary" type="button" data-automation-actions-operation="approve"' +
        disabled(!!state.mutating) + '>批准</button>' +
        '<button type="button" data-automation-actions-operation="edit_approve"' +
        disabled(!!state.mutating) + '>编辑后批准</button>' +
        '<button class="automationActions__danger" type="button" data-automation-actions-operation="reject"' +
        disabled(!!state.mutating) + '>拒绝</button>' : "") +
      (retry.allowed ? '<button type="button" data-automation-actions-operation="retry"' +
        disabled(!!state.mutating) + '>重试</button>' : "") +
      "</div></section>";
  }

  function renderDetail(state) {
    var envelope = selectedEnvelope(state);
    if (!state.selectedId) {
      return renderEmpty("选择一个 AI 动作后查看证据、差异与审批状态。");
    }
    if (!envelope) {
      return renderEmpty(state.detailLoading ? "正在加载动作详情..." : "当前动作详情尚未加载。");
    }
    var action = envelope.action || {};
    var executionState = actionSchedulerState(action);
    return '<article class="automationActions__detail">' +
      '<header class="automationActions__detailHead"><div><h3>' + escapeHTML(action.tool || "未知工具") +
      '</h3><span class="automationActions__mono">' + escapeHTML(action.id || "") +
      "</span></div><div>" + renderBadge(statusLabel(action.status), statusTone(action.status)) +
      renderBadge(executionStateLabel(executionState), executionStateTone(executionState)) +
      renderBadge(action.risk || "无风险级别", action.risk === "R3" || action.risk === "R4" ? "danger" : "neutral") +
      "</div></header>" +
      renderDefinitionList([
        ["Run ID", action.run_id],
        ["Provider Call ID", action.provider_call_id],
        ["依赖动作", asArray(action.depends_on_action_ids).join(", ") || "无"],
        ["调度状态", executionStateLabel(executionState)],
        ["阻塞原因", actionSchedulerReason(action)],
        ["当前动作版本", action.version],
        ["置信度", formatConfidence(action.confidence)],
        ["创建时间", formatTime(action.created_at)],
        ["更新时间", formatTime(action.updated_at)],
        ["重试来源", action.retry_of_action_id],
        ["替代来源", action.replaces_action_id],
        ["已被替代为", action.replaced_by_action_id]
      ]) +
      renderSource(envelope, action) +
      renderPayloadEditor(state, action) +
      renderDiff(envelope) +
      renderEvidence(action) +
      renderRisk(action) +
      renderPreconditions(envelope) +
      renderDependencies(envelope) +
      renderApprovals(envelope) +
      renderOutbox(envelope) +
      renderAudit(envelope) +
      renderOperations(state, envelope, action) +
      "</article>";
  }

  function render(state) {
    var busyText = "";
    if (state.mutating) {
      busyText = "正在提交人工操作...";
    } else if (state.loading) {
      busyText = "正在刷新动作列表...";
    } else if (state.detailLoading) {
      busyText = "正在刷新动作详情...";
    }
    return '<section class="automationActions">' +
      '<header class="automationActions__topbar"><div><h2>AI 工作台</h2><p>审核 AI 提议，并依据服务端风险、证据和版本前置条件执行操作。</p></div>' +
      '<button type="button" data-automation-actions-action="reload"' +
      disabled(state.loading || state.detailLoading || !!state.mutating) + '>权威刷新</button></header>' +
      '<div class="automationActions__status">' +
      Workbench.renderStatus(state.error, state.success, busyText) + "</div>" +
      '<div class="automationActions__workspace">' +
      '<section class="automationActions__panel automationActions__queue" aria-label="AI 动作队列">' +
      '<div class="automationActions__panelHead"><h3>动作队列</h3><span>' +
      escapeHTML(state.total + " 条") + "</span></div><div class=\"automationActions__panelBody\">" +
      renderFilters(state) + renderActionList(state) + renderPager(state) + "</div></section>" +
      '<section class="automationActions__panel automationActions__review" aria-label="AI 动作详情">' +
      '<div class="automationActions__panelBody">' + renderDetail(state) + "</div></section>" +
      "</div></section>";
  }

  function loadActionDetail(state, id) {
    var requestID = ++state.detailRequestSeq;
    state.detailLoading = true;
    requestRender();
    return Workbench.automationAPI(actionPath(id)).then(function (data) {
      if (requestID !== state.detailRequestSeq || state.selectedId !== id) {
        return;
      }
      if (!data || !isObject(data.action)) {
        throw new Error("动作详情响应无效");
      }
      state.detail = data;
      ensureDraft(state, data.action);
    }).catch(function (err) {
      if (requestID === state.detailRequestSeq && state.selectedId === id) {
        state.error = err.message;
      }
    }).finally(function () {
      if (requestID === state.detailRequestSeq) {
        state.detailLoading = false;
        requestRender();
      }
    });
  }

  function loadActions(state) {
    var requestID = ++state.listRequestSeq;
    var selectedBefore = state.selectedId;
    state.loading = true;
    requestRender();
    return Workbench.automationAPI(ACTIONS_PATH + Workbench.encodeQuery({
      status: state.filters.status,
      tool: state.filters.tool,
      risk: state.filters.risk,
      source_message_event_id: state.filters.source_message_event_id,
      limit: state.limit,
      offset: state.offset
    })).then(function (data) {
      if (requestID !== state.listRequestSeq) {
        return;
      }
      state.total = Number(data && data.total || 0);
      state.limit = Number(data && data.limit || state.limit);
      state.offset = Number(data && data.offset || 0);
      state.actions = data && Array.isArray(data.actions) ? data.actions : [];
      state.loaded = true;
      if (!state.selectedId && state.actions.length) {
        state.selectedId = state.actions[0].id || "";
        ensureDraft(state, state.actions[0]);
        if (state.selectedId) {
          loadActionDetail(state, state.selectedId);
        }
      } else if (selectedBefore && state.selectedId === selectedBefore) {
        var current = selectedListAction(state);
        if (current) {
          ensureDraft(state, current);
        }
      }
    }).catch(function (err) {
      if (requestID === state.listRequestSeq) {
        state.error = err.message;
      }
    }).finally(function () {
      if (requestID === state.listRequestSeq) {
        state.loading = false;
        requestRender();
      }
    });
  }

  function authoritativeReload(state, preserveStatus) {
    if (!preserveStatus) {
      state.error = "";
      state.success = "";
    }
    var requests = [loadActions(state)];
    if (state.selectedId) {
      requests.push(loadActionDetail(state, state.selectedId));
    }
    return Promise.all(requests);
  }

  function selectAction(state, id) {
    if (!id || id === state.selectedId || state.mutating) {
      return;
    }
    if (state.editDraftActionId) {
      state.editDrafts[state.editDraftActionId] = state.editDraft;
    }
    state.selectedId = id;
    state.error = "";
    state.success = "";
    if (hasOwn(state.editDrafts, id)) {
      state.editDraft = state.editDrafts[id];
      state.editDraftActionId = id;
    } else {
      var action = selectedListAction(state);
      state.editDraft = jsonText(action && action.payload);
      state.editDraftActionId = id;
      state.editDrafts[id] = state.editDraft;
    }
    requestRender();
    loadActionDetail(state, id);
  }

  function replaceAction(state, updated) {
    if (!updated || !updated.id) {
      return;
    }
    var found = false;
    state.actions = state.actions.map(function (action) {
      if (action && action.id === updated.id) {
        found = true;
        return updated;
      }
      return action;
    });
    if (!found) {
      state.actions.unshift(updated);
    }
    if (state.detail && state.detail.action && state.detail.action.id === updated.id) {
      state.detail.action = updated;
    }
  }

  function parseEditDraft(state) {
    var parsed;
    try {
      parsed = JSON.parse(state.editDraft);
    } catch (err) {
      throw new Error("编辑 JSON 不是有效 JSON");
    }
    if (!isObject(parsed)) {
      throw new Error("编辑 JSON 必须是一个对象");
    }
    return parsed;
  }

  function operationSuccess(operation) {
    var labels = {
      approve: "动作已批准。",
      edit_approve: "编辑后的动作已批准。",
      reject: "动作已拒绝。",
      retry: "已创建重试动作。"
    };
    return labels[operation] || "操作已完成。";
  }

  function performOperation(state, operation) {
    var envelope = selectedEnvelope(state);
    var action = envelope && envelope.action;
    var operationPath = OPERATION_PATHS[operation];
    if (!action || !operationPath || state.mutating) {
      return null;
    }
    var operatorID = state.operatorId.trim();
    if (!operatorID) {
      state.error = "请填写操作员 ID。";
      state.success = "";
      requestRender();
      return null;
    }
    if (operation === "retry" && !retryEligibility(action, envelope.outbox).allowed) {
      state.error = "当前动作不允许重试。";
      state.success = "";
      requestRender();
      return null;
    }
    var body = {
      expected_version: action.version,
      operator_id: operatorID,
      reason: state.reason.trim()
    };
    if (operation === "edit_approve") {
      try {
        body.arguments = parseEditDraft(state);
      } catch (err) {
        state.error = err.message;
        state.success = "";
        requestRender();
        return null;
      }
    }
    var requestID = ++state.mutationRequestSeq;
    var actionID = action.id;
    state.mutating = operation;
    state.error = "";
    state.success = "";
    requestRender();
    return Workbench.automationAPI(actionPath(actionID, operationPath), {
      method: "POST",
      body: body
    }).then(function (data) {
      if (requestID !== state.mutationRequestSeq) {
        return;
      }
      var updated = data && data.action;
      if (updated && updated.id) {
        replaceAction(state, updated);
        if (operation === "edit_approve") {
          state.editDrafts[updated.id] = state.editDraft;
        }
        state.selectedId = updated.id;
        state.editDraftActionId = updated.id;
        ensureDraft(state, updated);
      }
      state.success = operationSuccess(operation);
      state.mutating = "";
      requestRender();
      return authoritativeReload(state, true);
    }).catch(function (err) {
      if (requestID === state.mutationRequestSeq) {
        state.error = err.message;
      }
    }).finally(function () {
      if (requestID === state.mutationRequestSeq) {
        state.mutating = "";
        requestRender();
      }
    });
  }

  function syncField(state, target) {
    if (!target || !target.name) {
      return;
    }
    var form = target.form;
    if (
      form &&
      form.getAttribute("data-automation-actions-form") === "filters" &&
      hasOwn(state.filters, target.name)
    ) {
      state.filters[target.name] = target.value;
      return;
    }
    var field = target.getAttribute("data-automation-actions-field");
    if (field === "operatorId") {
      state.operatorId = target.value;
    } else if (field === "reason") {
      state.reason = target.value;
    } else if (field === "editDraft") {
      state.editDraft = target.value;
      state.editDraftActionId = state.selectedId;
      if (state.selectedId) {
        state.editDrafts[state.selectedId] = target.value;
      }
    }
  }

  function bind(root, state) {
    root.addEventListener("input", function (event) {
      syncField(state, event.target);
    });
    root.addEventListener("change", function (event) {
      syncField(state, event.target);
    });
    root.addEventListener("submit", function (event) {
      var form = event.target.closest("form[data-automation-actions-form]");
      if (!form) {
        return;
      }
      event.preventDefault();
      Array.prototype.forEach.call(form.elements, function (element) {
        syncField(state, element);
      });
      state.offset = 0;
      state.error = "";
      state.success = "";
      loadActions(state);
    });
    root.addEventListener("click", function (event) {
      var button = event.target.closest("button");
      if (!button || !root.contains(button)) {
        return;
      }
      var actionID = button.getAttribute("data-automation-actions-id");
      var action = button.getAttribute("data-automation-actions-action");
      var operation = button.getAttribute("data-automation-actions-operation");
      if (actionID) {
        selectAction(state, actionID);
      } else if (operation) {
        performOperation(state, operation);
      } else if (action === "reload") {
        authoritativeReload(state, false);
      } else if (action === "previous" && state.offset > 0) {
        state.offset = Math.max(0, state.offset - state.limit);
        state.error = "";
        state.success = "";
        loadActions(state);
      } else if (action === "next" && state.offset + state.limit < state.total) {
        state.offset += state.limit;
        state.error = "";
        state.success = "";
        loadActions(state);
      }
    });
  }

  function onEnter(state) {
    if (!state.loaded && !state.loading) {
      return authoritativeReload(state, false);
    }
    return null;
  }

  var view = {
    id: "ai-actions",
    label: "AI 工作台",
    section: "AI 中心",
    order: 65,
    navigation: false,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  };

  window.OneBotWorkbench.registerView(view);
})();
