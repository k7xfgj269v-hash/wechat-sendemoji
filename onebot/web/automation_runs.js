(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench) {
    return;
  }

  var AUTOMATION_RUNS_PATH = "/api/automation/runs";
  var AUTOMATION_RUNS_PAGE_SIZE = 50;
  var AUTOMATION_RUN_STATUS_LABELS = {
    queued: "排队中",
    running: "运行中",
    succeeded: "已完成",
    failed: "失败"
  };
  var AUTOMATION_ACTION_OUTCOME_LABELS = {
    proposed: "已提议",
    awaiting_review: "待审核",
    approved: "已批准",
    executing: "执行中",
    succeeded: "已完成",
    rejected: "已拒绝",
    stale: "数据过期",
    superseded: "已取代",
    failed: "失败",
    denied: "已禁止",
    sent: "已发送",
    unknown: "结果未知"
  };
  var AUTOMATION_ACTION_SUMMARY_STATES = {
    ready: "依赖就绪",
    pending: "等待依赖",
    awaiting_review: "等待审核",
    blocked: "依赖阻塞",
    executing: "执行中",
    succeeded: "已完成",
    failed: "失败"
  };

  function automationRunsCreateState() {
    return {
      filters: {
        status: "",
        sourceMessageEventID: ""
      },
      limit: AUTOMATION_RUNS_PAGE_SIZE,
      offset: 0,
      total: 0,
      runs: [],
      selectedRunID: "",
      detailEnvelope: null,
      loaded: false,
      loading: false,
      detailLoading: false,
      listRequestSequence: 0,
      detailRequestSequence: 0,
      listError: "",
      detailError: ""
    };
  }

  function automationRunsDisabled(value) {
    return value ? " disabled" : "";
  }

  function automationRunsSelected(value, current) {
    return String(value) === String(current) ? " selected" : "";
  }

  function automationRunsNumber(value, fallback) {
    var number = Number(value);
    return Number.isFinite(number) && number >= 0 ? number : fallback;
  }

  function automationRunsFormatTime(value) {
    if (value == null || value === "") {
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

  function automationRunsFormatJSON(value) {
    if (value == null || value === "") {
      return "无";
    }
    if (typeof value === "string") {
      try {
        return JSON.stringify(JSON.parse(value), null, 2);
      } catch (err) {
        return value;
      }
    }
    try {
      return JSON.stringify(value, null, 2);
    } catch (err) {
      return String(value);
    }
  }

  function automationRunsActionOutcome(action) {
    return String(action && action.status || "unknown");
  }

  function automationRunsSchedulerState(action) {
    return String(action && action.scheduler && action.scheduler.state || "pending");
  }

  function automationRunsSchedulerReason(action) {
    return String(action && action.scheduler && action.scheduler.not_ready_reason ||
      action && action.not_ready_reason || "");
  }

  function automationRunsDependencyLabel(state) {
    return {
      ready: "依赖就绪",
      blocked: "依赖阻塞",
      awaiting_review: "等待审核",
      completed: "已有结果",
      executing: "执行中",
      pending: "等待依赖"
    }[state] || state;
  }

  function automationRunsOutcomeLabel(outcome) {
    return AUTOMATION_ACTION_OUTCOME_LABELS[outcome] || outcome || "结果未知";
  }

  function automationRunsStatusLabel(status) {
    return AUTOMATION_RUN_STATUS_LABELS[status] || status || "未知状态";
  }

  function automationRunsAuditTime(value) {
    var timestamp = Date.parse(String(value || ""));
    return Number.isNaN(timestamp) ? 0 : timestamp;
  }

  function automationRunsOrderedAudit(events) {
    return (Array.isArray(events) ? events : []).slice().sort(function (left, right) {
      var timeDifference = automationRunsAuditTime(left && left.created_at) - automationRunsAuditTime(right && right.created_at);
      if (timeDifference !== 0) {
        return timeDifference;
      }
      return String(left && left.id || "").localeCompare(String(right && right.id || ""));
    });
  }

  function automationRunsLoadDetail(state, runID) {
    var requestID = state.detailRequestSequence + 1;
    state.detailRequestSequence = requestID;
    state.detailLoading = true;
    state.detailError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(AUTOMATION_RUNS_PATH + "/" + encodeURIComponent(runID)).then(function (data) {
      if (requestID !== state.detailRequestSequence || state.selectedRunID !== runID) {
        return;
      }
      state.detailEnvelope = {
        run: data && data.run || {},
        source_message: data && data.source_message || {},
        actions: data && Array.isArray(data.actions) ? data.actions : [],
        action_summary: data && data.action_summary || {},
        audit_events: data && Array.isArray(data.audit_events) ? data.audit_events : []
      };
    }).catch(function (err) {
      if (requestID === state.detailRequestSequence && state.selectedRunID === runID) {
        state.detailError = err.message;
      }
    }).finally(function () {
      if (requestID === state.detailRequestSequence) {
        state.detailLoading = false;
        Workbench.requestRender();
      }
    });
  }

  function automationRunsLoadList(state) {
    var requestID = state.listRequestSequence + 1;
    var firstRunID = "";
    state.listRequestSequence = requestID;
    state.loading = true;
    state.listError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(AUTOMATION_RUNS_PATH + Workbench.encodeQuery({
      status: state.filters.status,
      source_message_event_id: state.filters.sourceMessageEventID,
      limit: state.limit,
      offset: state.offset
    })).then(function (data) {
      if (requestID !== state.listRequestSequence) {
        return;
      }
      state.total = automationRunsNumber(data && data.total, 0);
      state.limit = automationRunsNumber(data && data.limit, state.limit) || AUTOMATION_RUNS_PAGE_SIZE;
      state.offset = automationRunsNumber(data && data.offset, state.offset);
      state.runs = data && Array.isArray(data.runs) ? data.runs : [];
      state.loaded = true;
      if (!state.selectedRunID && state.runs.length) {
        firstRunID = String(state.runs[0].id || "");
        state.selectedRunID = firstRunID;
      }
      if (firstRunID) {
        return automationRunsLoadDetail(state, firstRunID);
      }
      return null;
    }).catch(function (err) {
      if (requestID === state.listRequestSequence) {
        state.listError = err.message;
      }
      return null;
    }).finally(function () {
      if (requestID === state.listRequestSequence) {
        state.loading = false;
        Workbench.requestRender();
      }
    });
  }

  function automationRunsRenderFilters(state) {
    return '<form class="automationRuns-filterBar" data-automation-runs-form="filters">' +
      '<label class="automationRuns-statusField">运行状态<select name="status"' + automationRunsDisabled(state.loading) + ">" +
      '<option value="">全部状态</option>' +
      '<option value="queued"' + automationRunsSelected("queued", state.filters.status) + '>排队中</option>' +
      '<option value="running"' + automationRunsSelected("running", state.filters.status) + '>运行中</option>' +
      '<option value="succeeded"' + automationRunsSelected("succeeded", state.filters.status) + '>已完成</option>' +
      '<option value="failed"' + automationRunsSelected("failed", state.filters.status) + '>失败</option>' +
      "</select></label>" +
      '<label class="automationRuns-sourceField">来源消息 ID<input name="source_message_event_id" value="' + Workbench.escapeHTML(state.filters.sourceMessageEventID) + '" autocomplete="off" placeholder="event_00000001"' + automationRunsDisabled(state.loading) + "></label>" +
      '<div class="automationRuns-filterActions">' +
      '<button class="automationRuns-primaryButton" type="submit"' + automationRunsDisabled(state.loading) + '>查询</button>' +
      '<button type="button" data-automation-runs-action="reset-filters"' + automationRunsDisabled(state.loading || (!state.filters.status && !state.filters.sourceMessageEventID)) + '>清除</button>' +
      "</div></form>";
  }

  function automationRunsRenderRunRow(state, run) {
    var failure = run && (run.failure_code || run.failure_stage || run.failure_message);
    return '<button class="automationRuns-runRow" type="button" role="listitem" data-automation-runs-id="' + Workbench.escapeHTML(run && run.id) + '" aria-current="' + (state.selectedRunID === (run && run.id) ? "true" : "false") + '">' +
      '<span class="automationRuns-runRowTop"><strong>' + Workbench.escapeHTML(run && run.id || "无运行 ID") + '</strong><span class="automationRuns-badge automationRuns-tone-' + Workbench.escapeHTML(run && run.status || "unknown") + '">' + Workbench.escapeHTML(automationRunsStatusLabel(run && run.status)) + "</span></span>" +
      '<span class="automationRuns-runRowMeta"><span>' + Workbench.escapeHTML(run && run.provider_name || "未知 provider") + '</span><time>' + Workbench.escapeHTML(automationRunsFormatTime(run && run.created_at)) + "</time></span>" +
      '<span class="automationRuns-runRowSource">' + Workbench.escapeHTML(run && run.source_message_event_id || "无来源消息") + "</span>" +
      (failure ? '<span class="automationRuns-runRowFailure">' + Workbench.escapeHTML(run.failure_code || run.failure_stage || run.failure_message) + "</span>" : "") +
      "</button>";
  }

  function automationRunsRenderRunList(state) {
    if (!state.runs.length) {
      return '<div class="automationRuns-empty" role="status">' + (state.loading ? "正在加载运行记录..." : "当前筛选条件下没有运行记录。") + "</div>";
    }
    return '<div class="automationRuns-runList" role="list">' + state.runs.map(function (run) {
      return automationRunsRenderRunRow(state, run || {});
    }).join("") + "</div>";
  }

  function automationRunsRenderPager(state) {
    var start = state.total ? state.offset + 1 : 0;
    var end = Math.min(state.total, state.offset + state.runs.length);
    return '<div class="automationRuns-pager">' +
      '<span>' + Workbench.escapeHTML("显示 " + start + "-" + end + " / 共 " + state.total + " 条") + "</span>" +
      '<div class="automationRuns-pagerActions">' +
      '<button type="button" data-automation-runs-action="previous"' + automationRunsDisabled(state.loading || state.offset === 0) + '>上一页</button>' +
      '<button type="button" data-automation-runs-action="next"' + automationRunsDisabled(state.loading || state.offset + state.limit >= state.total) + '>下一页</button>' +
      "</div></div>";
  }

  function automationRunsRenderDetailRow(label, value) {
    var shown = value === 0 || value ? value : "无";
    return "<dt>" + Workbench.escapeHTML(label) + "</dt><dd>" + Workbench.escapeHTML(shown) + "</dd>";
  }

  function automationRunsRenderRunFailure(run) {
    if (!run || (!run.failure_stage && !run.failure_code && !run.failure_message)) {
      return "";
    }
    return '<section class="automationRuns-failure" aria-label="运行失败信息">' +
      '<h4>失败信息</h4><dl>' +
      automationRunsRenderDetailRow("阶段", run.failure_stage) +
      automationRunsRenderDetailRow("代码", run.failure_code) +
      automationRunsRenderDetailRow("消息", run.failure_message) +
      "</dl></section>";
  }

  function automationRunsRenderSourceMessage(sourceMessage) {
    var message = sourceMessage || {};
    return '<section class="automationRuns-detailSection" aria-label="来源消息">' +
      '<div class="automationRuns-sectionHeading"><h4>来源消息</h4><span>' + Workbench.escapeHTML(message.id || "无消息 ID") + "</span></div>" +
      '<p class="automationRuns-sourceText">' + Workbench.escapeHTML(message.text || "无文本内容") + "</p>" +
      '<dl class="automationRuns-detailGrid">' +
      automationRunsRenderDetailRow("消息键", message.message_key) +
      automationRunsRenderDetailRow("聊天类型", message.chat_type) +
      automationRunsRenderDetailRow("会话", message.peer_id) +
      automationRunsRenderDetailRow("发送者", message.author_id) +
      automationRunsRenderDetailRow("版本", message.version) +
      automationRunsRenderDetailRow("消息时间", automationRunsFormatTime(message.timestamp)) +
      automationRunsRenderDetailRow("创建时间", automationRunsFormatTime(message.created_at)) +
      automationRunsRenderDetailRow("可疑标记", message.suspicious ? "是" : "否") +
      "</dl></section>";
  }

  function automationRunsRenderActionCounts(summary) {
    var counts = summary || {};
    return '<div class="automationRuns-counts" aria-label="动作结果计数">' +
      '<span><strong>' + Workbench.escapeHTML(counts.total) + "</strong> 总计</span>" +
      Object.keys(AUTOMATION_ACTION_SUMMARY_STATES).map(function (state) {
        return '<span class="automationRuns-count automationRuns-tone-' + Workbench.escapeHTML(state) + '"><strong>' +
          Workbench.escapeHTML(counts[state] || 0) + "</strong> " +
          Workbench.escapeHTML(AUTOMATION_ACTION_SUMMARY_STATES[state]) + "</span>";
      }).join("") + "</div>";
  }

  function automationRunsRenderStringList(values) {
    if (!Array.isArray(values) || !values.length) {
      return '<span class="automationRuns-muted">无</span>';
    }
    return '<span class="automationRuns-inlineList">' + values.map(function (value) {
      return '<span>' + Workbench.escapeHTML(value) + "</span>";
    }).join("") + "</span>";
  }

  function automationRunsRenderAction(action) {
    var outcome = automationRunsActionOutcome(action);
    var dependencyState = automationRunsSchedulerState(action);
    var hasFailure = action && (action.error_code || action.error_message);
    return '<article class="automationRuns-action">' +
      '<div class="automationRuns-actionHeading"><div><strong>' + Workbench.escapeHTML(action && action.tool || "未知工具") + '</strong><span>' + Workbench.escapeHTML(action && action.id || "无动作 ID") + '</span></div><div class="automationRuns-actionBadges"><span class="automationRuns-badge automationRuns-tone-' +
      Workbench.escapeHTML(dependencyState) + '">' + Workbench.escapeHTML(automationRunsDependencyLabel(dependencyState)) +
      '</span><span class="automationRuns-badge automationRuns-tone-' + Workbench.escapeHTML(outcome) + '">' +
      Workbench.escapeHTML(automationRunsOutcomeLabel(outcome)) + "</span></div></div>" +
      '<dl class="automationRuns-actionMeta">' +
      automationRunsRenderDetailRow("动作状态", automationRunsOutcomeLabel(action && action.status)) +
      automationRunsRenderDetailRow("依赖状态", automationRunsDependencyLabel(dependencyState)) +
      automationRunsRenderDetailRow("阻塞原因", automationRunsSchedulerReason(action)) +
      automationRunsRenderDetailRow("风险", action && action.risk) +
      automationRunsRenderDetailRow("策略决定", action && action.policy_decision) +
      automationRunsRenderDetailRow("置信度", action && action.confidence) +
      automationRunsRenderDetailRow("尝试次数", action && action.attempt) +
      automationRunsRenderDetailRow("更新时间", automationRunsFormatTime(action && action.updated_at)) +
      "</dl>" +
      '<div class="automationRuns-actionDependencies"><span>依赖动作</span>' +
      automationRunsRenderStringList(action && action.depends_on_action_ids) + "</div>" +
      '<div class="automationRuns-actionReasons"><span>规则</span>' + automationRunsRenderStringList(action && action.risk_reasons) + "</div>" +
      (hasFailure ? '<div class="automationRuns-actionFailure"><strong>' + Workbench.escapeHTML(action.error_code || "action_failed") + '</strong><span>' + Workbench.escapeHTML(action.error_message || "无错误详情") + "</span></div>" : "") +
      '<details class="automationRuns-raw"><summary>提议与结果实体</summary><div><span>Payload</span><pre>' + Workbench.escapeHTML(automationRunsFormatJSON(action && action.payload)) + '</pre><span>Result entity</span><pre>' + Workbench.escapeHTML(automationRunsFormatJSON(action && action.result_entity)) + "</pre></div></details>" +
      "</article>";
  }

  function automationRunsRenderActions(actions, summary) {
    var items = Array.isArray(actions) ? actions : [];
    return '<section class="automationRuns-detailSection" aria-label="动作摘要">' +
      '<div class="automationRuns-sectionHeading"><h4>动作</h4><span>' + Workbench.escapeHTML(items.length) + " 项</span></div>" +
      automationRunsRenderActionCounts(summary) +
      (items.length ? '<div class="automationRuns-actionList">' + items.map(function (action) {
        return automationRunsRenderAction(action || {});
      }).join("") + "</div>" : '<div class="automationRuns-empty">该运行没有动作。</div>') +
      "</section>";
  }

  function automationRunsRenderAuditEvent(event) {
    var actor = [event && event.actor_type, event && event.actor_id].filter(Boolean).join(" / ");
    var references = [event && event.action_id, event && event.outbox_id].filter(Boolean).join(" / ");
    return '<li class="automationRuns-auditEvent">' +
      '<div><strong>' + Workbench.escapeHTML(event && event.event_type || "未知事件") + '</strong><time>' + Workbench.escapeHTML(automationRunsFormatTime(event && event.created_at)) + "</time></div>" +
      '<span>' + Workbench.escapeHTML(actor || "未知执行者") + "</span>" +
      (references ? '<span>' + Workbench.escapeHTML(references) + "</span>" : "") +
      '<pre>' + Workbench.escapeHTML(automationRunsFormatJSON(event && event.data)) + "</pre>" +
      "</li>";
  }

  function automationRunsRenderAudit(events) {
    var ordered = automationRunsOrderedAudit(events);
    return '<section class="automationRuns-detailSection" aria-label="审计事件">' +
      '<div class="automationRuns-sectionHeading"><h4>审计事件</h4><span>' + Workbench.escapeHTML(ordered.length) + " 条</span></div>" +
      (ordered.length ? '<ol class="automationRuns-auditList">' + ordered.map(function (event) {
        return automationRunsRenderAuditEvent(event || {});
      }).join("") + "</ol>" : '<div class="automationRuns-empty">没有审计事件。</div>') +
      "</section>";
  }

  function automationRunsRenderDetail(state) {
    var envelope = state.detailEnvelope;
    if (!envelope || !envelope.run) {
      return '<div class="automationRuns-detailEmpty">' +
        Workbench.renderStatus(state.detailError, "", state.detailLoading ? "正在加载运行详情..." : "") +
        '<strong>未选择运行</strong><span>暂无运行详情。</span></div>';
    }
    var run = envelope.run;
    var detailMatchesSelection = String(run.id || "") === state.selectedRunID;
    var retainedNotice = detailMatchesSelection ? "" : '<div class="automationRuns-retainedNotice" role="status">所选运行的详情尚未更新，当前保留上次成功加载的详情。</div>';
    return '<div class="automationRuns-detailContent">' +
      Workbench.renderStatus(state.detailError, "", state.detailLoading ? "正在加载运行详情..." : "") +
      retainedNotice +
      '<header class="automationRuns-detailHeader"><div><h3>' + Workbench.escapeHTML(run.id || "无运行 ID") + '<span class="automationRuns-badge automationRuns-tone-' + Workbench.escapeHTML(run.status || "unknown") + '">' + Workbench.escapeHTML(automationRunsStatusLabel(run.status)) + '</span></h3><p>' + Workbench.escapeHTML(run.provider_name || "未知 provider") + "</p></div></header>" +
      '<dl class="automationRuns-detailGrid automationRuns-runMeta">' +
      automationRunsRenderDetailRow("Provider", run.provider_name) +
      automationRunsRenderDetailRow("Engine", run.engine_version) +
      automationRunsRenderDetailRow("来源消息", run.source_message_event_id) +
      automationRunsRenderDetailRow("来源版本", run.source_message_version) +
      automationRunsRenderDetailRow("运行版本", run.version) +
      automationRunsRenderDetailRow("尝试次数", run.attempt) +
      automationRunsRenderDetailRow("重试来源", run.retry_of_run_id) +
      automationRunsRenderDetailRow("创建时间", automationRunsFormatTime(run.created_at)) +
      automationRunsRenderDetailRow("开始时间", automationRunsFormatTime(run.started_at)) +
      automationRunsRenderDetailRow("完成时间", automationRunsFormatTime(run.completed_at)) +
      automationRunsRenderDetailRow("响应摘要", run.provider_response_sha256) +
      "</dl>" +
      automationRunsRenderRunFailure(run) +
      automationRunsRenderSourceMessage(envelope.source_message) +
      automationRunsRenderActions(envelope.actions, envelope.action_summary) +
      automationRunsRenderAudit(envelope.audit_events) +
      "</div>";
  }

  function automationRunsSelectionAbsentFromPage(state) {
    return !!state.selectedRunID && !state.runs.some(function (run) {
      return run && run.id === state.selectedRunID;
    });
  }

  function automationRunsRender(state) {
    var selectionNotice = automationRunsSelectionAbsentFromPage(state) ?
      '<div class="automationRuns-selectionNotice" role="status">所选运行不在当前页，详情选择已保留。</div>' : "";
    return '<div class="automationRuns">' +
      '<header class="automationRuns-header"><div><h2>运行历史</h2><span>' + Workbench.escapeHTML(state.total) + ' 条记录</span></div><button type="button" data-automation-runs-action="refresh"' + automationRunsDisabled(state.loading) + '>刷新</button></header>' +
      automationRunsRenderFilters(state) +
      Workbench.renderStatus(state.listError, "", state.loading ? "正在加载运行记录..." : "") +
      selectionNotice +
      '<div class="automationRuns-layout">' +
      '<section class="automationRuns-listPane" aria-label="运行记录">' +
      automationRunsRenderRunList(state) +
      automationRunsRenderPager(state) +
      "</section>" +
      '<section class="automationRuns-detailPane" aria-label="运行详情">' +
      automationRunsRenderDetail(state) +
      "</section></div></div>";
  }

  function automationRunsSyncFilter(state, target) {
    if (!target || !target.name) {
      return;
    }
    if (target.name === "status") {
      state.filters.status = target.value;
    } else if (target.name === "source_message_event_id") {
      state.filters.sourceMessageEventID = target.value;
    }
  }

  function automationRunsBind(root, state) {
    root.addEventListener("input", function (event) {
      automationRunsSyncFilter(state, event.target);
    });
    root.addEventListener("change", function (event) {
      automationRunsSyncFilter(state, event.target);
    });
    root.addEventListener("submit", function (event) {
      var form = event.target.closest('form[data-automation-runs-form="filters"]');
      if (!form || !root.contains(form)) {
        return;
      }
      event.preventDefault();
      Array.prototype.forEach.call(form.elements, function (element) {
        automationRunsSyncFilter(state, element);
      });
      state.filters.sourceMessageEventID = state.filters.sourceMessageEventID.trim();
      state.offset = 0;
      automationRunsLoadList(state);
    });
    root.addEventListener("click", function (event) {
      var button = event.target.closest("button");
      if (!button || !root.contains(button)) {
        return;
      }
      var runID = button.getAttribute("data-automation-runs-id");
      var action = button.getAttribute("data-automation-runs-action");
      if (runID) {
        state.selectedRunID = runID;
        state.detailError = "";
        automationRunsLoadDetail(state, runID);
      } else if (action === "refresh") {
        automationRunsLoadList(state);
        if (state.selectedRunID) {
          automationRunsLoadDetail(state, state.selectedRunID);
        }
      } else if (action === "reset-filters") {
        state.filters.status = "";
        state.filters.sourceMessageEventID = "";
        state.offset = 0;
        automationRunsLoadList(state);
      } else if (action === "previous" && state.offset > 0) {
        state.offset = Math.max(0, state.offset - state.limit);
        automationRunsLoadList(state);
      } else if (action === "next" && state.offset + state.limit < state.total) {
        state.offset += state.limit;
        automationRunsLoadList(state);
      }
    });
  }

  function automationRunsOnEnter(state) {
    if (!state.loaded && !state.loading) {
      return automationRunsLoadList(state);
    }
    return null;
  }

  Workbench.registerView({
    id: "automation-runs",
    label: "运行历史",
    section: "AI 中心",
    order: 90,
    createState: automationRunsCreateState,
    render: automationRunsRender,
    onEnter: automationRunsOnEnter,
    bind: automationRunsBind
  });
})();
