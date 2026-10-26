(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  var PAGE_SIZE = 30;
  var STATUS_OPTIONS = [
    ["", "全部状态"],
    ["draft", "草稿"],
    ["awaiting_commitment", "待承诺确认"],
    ["committed", "已承诺"],
    ["callback_due", "待回访"],
    ["awaiting_final", "待最终确认"],
    ["completed", "已完成"],
    ["overdue", "已逾期"],
    ["disputed", "有争议"],
    ["cancelled", "已取消"]
  ];
  var EDITABLE_STATUSES = [
    ["draft", "草稿"],
    ["awaiting_commitment", "提交承诺确认"],
    ["callback_due", "待回访"],
    ["awaiting_final", "提交最终确认"],
    ["overdue", "已逾期"],
    ["disputed", "有争议"],
    ["cancelled", "取消"]
  ];

  function createState() {
    return {
      tab: "funds",
      filters: { q: "", status: "", candidate_id: "", currency: "" },
      page: 0,
      total: 0,
      callbacks: [],
      selectedId: "",
      detail: null,
      ledger: [],
      formMode: "",
      form: blankFundForm(),
      finalResult: "",
      loaded: false,
      loading: false,
      detailLoading: false,
      ledgerLoading: false,
      saving: false,
      protecting: "",
      error: "",
      success: "",
      listRequest: 0,
      detailRequest: 0,
      audit: {
        filters: { actor: "", operation: "", entity_type: "", entity_id: "" },
        total: 0,
        events: [],
        loading: false,
        loaded: false
      },
      report: {
        summary: null,
        loading: false,
        exporting: false
      },
      governance: {
        candidateId: "",
        candidate: null,
        loading: false,
        exporting: false,
        anonymizing: false
      }
    };
  }

  function blankFundForm() {
    return {
      candidate_id: "",
      job_id: "",
      lead_id: "",
      onboarding_case_id: "",
      amount_minor: "",
      currency: "CNY",
      condition: "",
      promised_by: "",
      due_at: "",
      evidence: "",
      status: "draft",
      version: 0
    };
  }

  function escapeHTML(value) {
    return Workbench.escapeHTML(value);
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function selected(value, current) {
    return String(value) === String(current) ? " selected" : "";
  }

  function optionHTML(options, current) {
    return options.map(function (entry) {
      return '<option value="' + escapeHTML(entry[0]) + '"' + selected(entry[0], current) + ">" +
        escapeHTML(entry[1]) + "</option>";
    }).join("");
  }

  function statusLabel(status) {
    var entry = STATUS_OPTIONS.find(function (item) { return item[0] === status; });
    return entry ? entry[1] : status || "未知";
  }

  function statusTone(status) {
    if (status === "completed" || status === "committed") {
      return "success";
    }
    if (status === "overdue" || status === "disputed" || status === "cancelled") {
      return "danger";
    }
    if (status === "awaiting_commitment" || status === "awaiting_final" || status === "callback_due") {
      return "accent";
    }
    return "neutral";
  }

  function formatTime(value) {
    if (!value) {
      return "无";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function inputTime(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return "";
    }
    var pad = function (number) { return String(number).padStart(2, "0"); };
    return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate()) +
      "T" + pad(date.getHours()) + ":" + pad(date.getMinutes());
  }

  function apiTime(value) {
    if (!value) {
      return null;
    }
    var date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date.toISOString();
  }

  function fundForm(callback) {
    return {
      candidate_id: callback.candidate_id || "",
      job_id: callback.job_id || "",
      lead_id: callback.lead_id || "",
      onboarding_case_id: callback.onboarding_case_id || "",
      amount_minor: String(callback.amount_minor || ""),
      currency: callback.currency || "CNY",
      condition: callback.condition || "",
      promised_by: callback.promised_by || "",
      due_at: inputTime(callback.due_at),
      evidence: callback.evidence || "",
      status: callback.status || "draft",
      version: Number(callback.version) || 0
    };
  }

  function syncFundForm(form, state) {
    [
      "candidate_id", "job_id", "lead_id", "onboarding_case_id", "amount_minor",
      "currency", "condition", "promised_by", "due_at", "evidence", "status"
    ].forEach(function (name) {
      if (form.elements[name]) {
        state.form[name] = form.elements[name].value;
      }
    });
  }

  function errorMessage(err) {
    if (!err) {
      return "操作失败";
    }
    if (err.status === 409 || /version conflict|版本冲突/i.test(err.message || "")) {
      return "409 版本冲突：服务端记录已变化，当前草稿已保留。";
    }
    return err.message || String(err);
  }

  function loadFunds(state, page) {
    var requestedPage = Math.max(0, Number(page) || 0);
    var requestID = ++state.listRequest;
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/fund_callbacks" + Workbench.encodeQuery({
      q: state.filters.q.trim(),
      status: state.filters.status,
      candidate_id: state.filters.candidate_id.trim(),
      currency: state.filters.currency.trim().toUpperCase(),
      limit: PAGE_SIZE,
      offset: requestedPage * PAGE_SIZE
    })).then(function (data) {
      if (requestID !== state.listRequest) {
        return;
      }
      state.total = Number(data && data.total) || 0;
      state.callbacks = data && Array.isArray(data.fund_callbacks) ? data.fund_callbacks : [];
      state.page = requestedPage;
      state.loaded = true;
      if (state.selectedId && !state.callbacks.some(function (item) { return item.id === state.selectedId; })) {
        state.selectedId = "";
        state.detail = null;
        state.formMode = "";
      }
    }).catch(function (err) {
      if (requestID === state.listRequest) {
        state.error = errorMessage(err);
      }
    }).finally(function () {
      if (requestID === state.listRequest) {
        state.loading = false;
        Workbench.requestRender();
      }
    });
  }

  function loadFundDetail(state, id, preserveDraft) {
    var requestID = ++state.detailRequest;
    var draft = preserveDraft ? state.form : null;
    state.detailLoading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/fund_callbacks/" + encodeURIComponent(id)).then(function (callback) {
      if (requestID !== state.detailRequest) {
        return;
      }
      state.selectedId = callback.id;
      state.detail = callback;
      if (preserveDraft && draft) {
        draft.version = Number(callback.version) || 0;
        state.form = draft;
        state.formMode = "edit";
        state.error = "当前草稿已保留，已加载服务端最新版本。";
      } else {
        state.form = fundForm(callback);
        state.formMode = "";
      }
      return loadFundLedger(state, callback.id);
    }).catch(function (err) {
      if (requestID === state.detailRequest) {
        state.error = errorMessage(err);
      }
    }).finally(function () {
      if (requestID === state.detailRequest) {
        state.detailLoading = false;
        Workbench.requestRender();
      }
    });
  }

  function loadFundLedger(state, id) {
    state.ledgerLoading = true;
    Workbench.requestRender();
    return Workbench.api("/api/ops/fund_callbacks/" + encodeURIComponent(id) + "/ledger").then(function (data) {
      state.ledger = data && Array.isArray(data.ledger) ? data.ledger : [];
    }).catch(function (err) {
      state.error = errorMessage(err);
    }).finally(function () {
      state.ledgerLoading = false;
      Workbench.requestRender();
    });
  }

  function saveFund(state, form) {
    syncFundForm(form, state);
    var amount = Number(state.form.amount_minor);
    if (!Number.isSafeInteger(amount) || amount <= 0) {
      state.error = "金额必须是大于 0 的整数最小货币单位。";
      Workbench.requestRender();
      return;
    }
    var editing = state.formMode === "edit";
    var dueAt = apiTime(state.form.due_at);
    var body = {
      candidate_id: state.form.candidate_id.trim(),
      job_id: state.form.job_id.trim(),
      lead_id: state.form.lead_id.trim(),
      onboarding_case_id: state.form.onboarding_case_id.trim(),
      amount_minor: amount,
      currency: state.form.currency.trim().toUpperCase(),
      condition: state.form.condition.trim(),
      promised_by: state.form.promised_by.trim(),
      evidence: state.form.evidence.trim()
    };
    if (dueAt) {
      body.due_at = dueAt;
    }
    var path = "/api/ops/fund_callbacks";
    var options = { method: "POST", body: body };
    if (editing) {
      path += "/" + encodeURIComponent(state.selectedId);
      body.expected_version = state.form.version;
      body.status = state.form.status;
      if (!dueAt) {
        body.clear_due_at = true;
      }
      options.method = "PATCH";
    }
    state.saving = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.api(path, options).then(function (callback) {
      state.selectedId = callback.id;
      state.detail = callback;
      state.form = fundForm(callback);
      state.formMode = "";
      state.success = editing ? "资金回调已更新。" : "资金回调草稿已创建。";
      return loadFunds(state, editing ? state.page : 0);
    }).catch(function (err) {
      state.error = errorMessage(err);
      if (editing && (err.status === 409 || /version conflict|版本冲突/i.test(err.message || ""))) {
        return loadFundDetail(state, state.selectedId, true);
      }
      return null;
    }).finally(function () {
      state.saving = false;
      Workbench.requestRender();
    });
  }

  function protectedFundAction(state, action) {
    var callback = state.detail;
    if (!callback) {
      state.error = "请先选择资金回调。";
      Workbench.requestRender();
      return;
    }
    var path;
    var body = {
      expected_version: callback.version,
      confirmed: true
    };
    var message;
    if (action === "commitment") {
      path = "/api/ops/fund_callbacks/" + encodeURIComponent(callback.id) + "/confirm_commitment";
      message = "确认由人工承诺这笔 " + callback.currency + " " + callback.amount_minor + " 的资金回调？";
    } else {
      if (!state.finalResult.trim()) {
        state.error = "最终确认需要填写回访结果。";
        Workbench.requestRender();
        return;
      }
      path = "/api/ops/fund_callbacks/" + encodeURIComponent(callback.id) + "/confirm_final";
      body.final_result = state.finalResult.trim();
      message = "确认最终回访结果并完成这笔资金回调？此操作不可由普通更新撤销。";
    }
    if (!window.confirm(message)) {
      return;
    }
    state.protecting = action;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(path, { method: "POST", body: body }).then(function (updated) {
      state.detail = updated;
      state.form = fundForm(updated);
      state.finalResult = "";
      state.success = action === "commitment" ? "人工承诺确认已记录。" : "最终回访确认已记录。";
      return Promise.all([loadFunds(state, state.page), loadFundLedger(state, updated.id)]);
    }).catch(function (err) {
      state.error = errorMessage(err);
      if (err.status === 409) {
        return loadFundDetail(state, callback.id, false);
      }
      return null;
    }).finally(function () {
      state.protecting = "";
      Workbench.requestRender();
    });
  }

  function loadAudit(state) {
    state.audit.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/governance/audit" + Workbench.encodeQuery({
      actor: state.audit.filters.actor.trim(),
      operation: state.audit.filters.operation.trim(),
      entity_type: state.audit.filters.entity_type.trim(),
      entity_id: state.audit.filters.entity_id.trim(),
      limit: 100,
      offset: 0
    })).then(function (data) {
      state.audit.total = Number(data && data.total) || 0;
      state.audit.events = data && Array.isArray(data.audit_events) ? data.audit_events : [];
      state.audit.loaded = true;
    }).catch(function (err) {
      state.error = errorMessage(err);
    }).finally(function () {
      state.audit.loading = false;
      Workbench.requestRender();
    });
  }

  function loadReport(state) {
    state.report.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/reports/summary").then(function (summary) {
      state.report.summary = summary;
    }).catch(function (err) {
      state.error = errorMessage(err);
    }).finally(function () {
      state.report.loading = false;
      Workbench.requestRender();
    });
  }

  function downloadText(filename, text, type) {
    var blob = new Blob([text], { type: type });
    var url = URL.createObjectURL(blob);
    var link = document.createElement("a");
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  }

  function exportReport(state) {
    state.report.exporting = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/reports/export.csv").then(function (text) {
      downloadText("crm-operations.csv", String(text || ""), "text/csv;charset=utf-8");
      state.success = "运营报表 CSV 已生成。";
    }).catch(function (err) {
      state.error = errorMessage(err);
    }).finally(function () {
      state.report.exporting = false;
      Workbench.requestRender();
    });
  }

  function loadGovernanceCandidate(state) {
    var id = state.governance.candidateId.trim();
    if (!id) {
      state.error = "请输入候选人 ID。";
      Workbench.requestRender();
      return;
    }
    state.governance.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/crm/candidates/" + encodeURIComponent(id)).then(function (detail) {
      state.governance.candidate = detail && detail.candidate ? detail.candidate : null;
      if (!state.governance.candidate) {
        throw new Error("候选人详情响应无效");
      }
    }).catch(function (err) {
      state.governance.candidate = null;
      state.error = errorMessage(err);
    }).finally(function () {
      state.governance.loading = false;
      Workbench.requestRender();
    });
  }

  function exportCandidate(state) {
    var candidate = state.governance.candidate;
    if (!candidate) {
      return;
    }
    state.governance.exporting = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api("/api/ops/governance/candidates/" + encodeURIComponent(candidate.id) + "/export").then(function (archive) {
      downloadText(
        "candidate-" + candidate.id + ".json",
        JSON.stringify(archive, null, 2),
        "application/json;charset=utf-8"
      );
      state.success = "候选人归档已导出。";
    }).catch(function (err) {
      state.error = errorMessage(err);
    }).finally(function () {
      state.governance.exporting = false;
      Workbench.requestRender();
    });
  }

  function anonymizeCandidate(state) {
    var candidate = state.governance.candidate;
    if (!candidate) {
      state.error = "匿名化需要先加载候选人详情。";
      Workbench.requestRender();
      return;
    }
    if (!window.confirm("确认匿名化候选人 " + candidate.id + "？直接身份字段将被清除，操作不可撤销。")) {
      return;
    }
    state.governance.anonymizing = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(
      "/api/ops/governance/candidates/" + encodeURIComponent(candidate.id) + "/anonymize",
      {
        method: "POST",
        body: { expected_version: candidate.version, confirmed: true }
      }
    ).then(function (updated) {
      state.governance.candidate = updated;
      state.success = "候选人已匿名化并归档。";
      return loadAudit(state);
    }).catch(function (err) {
      state.error = errorMessage(err);
      if (err.status === 409) {
        return loadGovernanceCandidate(state);
      }
      return null;
    }).finally(function () {
      state.governance.anonymizing = false;
      Workbench.requestRender();
    });
  }

  function renderTabs(state) {
    return '<div class="fundsGovernance__tabs" role="tablist" aria-label="资金与治理视图">' +
      [["funds", "资金回调"], ["audit", "审计"], ["reports", "报表与归档"]].map(function (entry) {
        return '<button type="button" role="tab" data-tab="' + entry[0] + '" aria-selected="' +
          (state.tab === entry[0] ? "true" : "false") + '">' + entry[1] + "</button>";
      }).join("") + "</div>";
  }

  function render(state) {
    var busy = state.loading || state.detailLoading || state.saving || !!state.protecting ||
      state.audit.loading || state.report.loading || state.governance.loading;
    var body = state.tab === "funds" ? renderFunds(state) :
      state.tab === "audit" ? renderAudit(state) : renderReports(state);
    return '<section class="fundsGovernance">' +
      '<header class="fundsGovernance__topbar"><div><h2>资金与治理</h2><p>资金承诺、最终回访、审计留痕和候选人数据治理。</p></div>' +
      '<button type="button" data-action="refresh" aria-label="刷新当前视图"' + disabled(busy) + '>刷新</button></header>' +
      renderTabs(state) +
      '<div class="fundsGovernance__status">' +
      Workbench.renderStatus(state.error, state.success, busy ? "正在处理..." : "") + "</div>" +
      body + "</section>";
  }

  function renderFunds(state) {
    return '<div class="fundsGovernance__workspace">' +
      '<section class="fundsGovernance__panel"><div class="fundsGovernance__panelHead"><h3>资金回调队列</h3>' +
      '<button class="primary" type="button" data-action="new-fund"' + disabled(state.saving) + '>新建草稿</button></div>' +
      '<div class="fundsGovernance__panelBody">' + renderFundFilters(state) + renderFundList(state) + renderPager(state) + "</div></section>" +
      '<section class="fundsGovernance__panel"><div class="fundsGovernance__panelHead"><h3>回调详情</h3>' +
      '<span>' + escapeHTML(state.detail ? state.detail.id : "未选择") + "</span></div>" +
      '<div class="fundsGovernance__panelBody">' + renderFundInspector(state) + "</div></section></div>";
  }

  function renderFundFilters(state) {
    return '<form class="fundsGovernance__filters" data-form="fund-filters">' +
      '<label>搜索<input name="q" value="' + escapeHTML(state.filters.q) + '" placeholder="条件、承诺方或 ID"></label>' +
      '<label>状态<select name="status">' + optionHTML(STATUS_OPTIONS, state.filters.status) + "</select></label>" +
      '<label>候选人 ID<input name="candidate_id" value="' + escapeHTML(state.filters.candidate_id) + '"></label>' +
      '<label>币种<input name="currency" maxlength="3" value="' + escapeHTML(state.filters.currency) + '" placeholder="CNY"></label>' +
      '<button type="submit"' + disabled(state.loading) + '>筛选</button></form>';
  }

  function renderFundList(state) {
    if (state.loading && !state.callbacks.length) {
      return '<div class="fundsGovernance__empty">正在加载资金回调...</div>';
    }
    if (!state.callbacks.length) {
      return '<div class="fundsGovernance__empty"><strong>暂无资金回调</strong><span>创建草稿或调整筛选条件。</span></div>';
    }
    return '<div class="fundsGovernance__list">' + state.callbacks.map(function (callback) {
      return '<button type="button" class="fundsGovernance__row" data-fund-id="' + escapeHTML(callback.id) +
        '" aria-current="' + (callback.id === state.selectedId ? "true" : "false") + '">' +
        '<span class="fundsGovernance__rowTop"><strong>' + escapeHTML(callback.currency + " " + callback.amount_minor) +
        '</strong><span class="fundsGovernance__badge fundsGovernance__badge--' + statusTone(callback.status) + '">' +
        escapeHTML(statusLabel(callback.status)) + "</span></span>" +
        '<span class="fundsGovernance__rowMeta">' + escapeHTML(callback.candidate_id) + " · " +
        escapeHTML(callback.promised_by) + "</span>" +
        '<span class="fundsGovernance__rowMeta">' + escapeHTML(formatTime(callback.due_at)) + "</span></button>";
    }).join("") + "</div>";
  }

  function renderPager(state) {
    var start = state.total ? state.page * PAGE_SIZE + 1 : 0;
    var end = Math.min(state.total, (state.page + 1) * PAGE_SIZE);
    return '<div class="fundsGovernance__pager"><span>' + start + "-" + end + " / " + state.total + '</span><div>' +
      '<button type="button" data-action="prev-page"' + disabled(state.loading || state.page === 0) + '>上一页</button>' +
      '<button type="button" data-action="next-page"' +
      disabled(state.loading || (state.page + 1) * PAGE_SIZE >= state.total) + '>下一页</button></div></div>';
  }

  function renderFundInspector(state) {
    if (state.formMode === "create" || state.formMode === "edit") {
      return renderFundForm(state);
    }
    if (state.detailLoading) {
      return '<div class="fundsGovernance__empty">正在加载详情...</div>';
    }
    if (!state.detail) {
      return '<div class="fundsGovernance__empty"><strong>选择一条资金回调</strong><span>查看明细、编辑或执行受保护确认。</span></div>';
    }
    var callback = state.detail;
    return '<article class="fundsGovernance__detail">' +
      '<div class="fundsGovernance__detailHead"><div><h3>' + escapeHTML(callback.currency + " " + callback.amount_minor) +
      '</h3><p>' + escapeHTML(callback.id) + '</p></div><span class="fundsGovernance__badge fundsGovernance__badge--' +
      statusTone(callback.status) + '">' + escapeHTML(statusLabel(callback.status)) + "</span></div>" +
      '<dl class="fundsGovernance__kv">' +
      detailValue("候选人", callback.candidate_id) + detailValue("职位", callback.job_id) +
      detailValue("Lead", callback.lead_id) + detailValue("入职案例", callback.onboarding_case_id) +
      detailValue("承诺方", callback.promised_by) + detailValue("到期时间", formatTime(callback.due_at)) +
      detailValue("承诺确认人", callback.commitment_by) + detailValue("承诺时间", formatTime(callback.commitment_at)) +
      detailValue("最终确认人", callback.final_confirmed_by) + detailValue("最终确认时间", formatTime(callback.final_confirmed_at)) +
      detailValue("版本", callback.version) + detailValue("更新时间", formatTime(callback.updated_at)) + "</dl>" +
      '<section class="fundsGovernance__textBlock"><h4>触发条件</h4><p>' + escapeHTML(callback.condition || "无") + "</p></section>" +
      '<section class="fundsGovernance__textBlock"><h4>证据</h4><p>' + escapeHTML(callback.evidence || "无") + "</p></section>" +
      (callback.final_result ? '<section class="fundsGovernance__textBlock"><h4>最终结果</h4><p>' +
        escapeHTML(callback.final_result) + "</p></section>" : "") +
      '<div class="fundsGovernance__actions"><button type="button" data-action="edit-fund"' +
      disabled(callback.status === "completed" || callback.status === "cancelled") + '>编辑</button></div>' +
      renderProtectedActions(state, callback) + renderFundLedger(state) + "</article>";
  }

  function detailValue(label, value) {
    return "<dt>" + escapeHTML(label) + "</dt><dd>" + escapeHTML(value === 0 || value ? value : "无") + "</dd>";
  }

  function renderProtectedActions(state, callback) {
    var commitment = callback.status === "awaiting_commitment";
    var final = callback.status === "awaiting_final";
    var actionButton = commitment ?
      '<button class="danger" type="button" data-action="confirm-commitment"' +
        disabled(!!state.protecting) + '>确认资金承诺</button>' :
      '<button class="danger" type="button" data-action="confirm-final"' +
        disabled(!!state.protecting) + '>确认最终回访</button>';
    if (!commitment && !final) {
      return "";
    }
    return '<section class="fundsGovernance__protected" aria-label="受保护人工确认"><h4>受保护人工确认</h4>' +
      '<p>确认身份来自当前已认证的操作员会话，并写入不可变资金账本。</p>' +
      (final ? '<label>最终回访结果<textarea data-field="final-result">' +
        escapeHTML(state.finalResult) + "</textarea></label>" : "") +
      actionButton + "</section>";
  }

  function renderFundLedger(state) {
    return '<section class="fundsGovernance__ledger"><div><h4>资金回调账本</h4><span>' +
      escapeHTML(state.ledgerLoading ? "加载中" : state.ledger.length + " 条") + '</span></div>' +
      (state.ledger.length ? state.ledger.map(function (entry) {
        return '<article><div><strong>' + escapeHTML(entry.type) + '</strong><time>' +
          escapeHTML(formatTime(entry.created_at)) + '</time></div><p>' +
          escapeHTML((entry.from_status || "create") + " -> " + entry.to_status) + '</p><small>' +
          escapeHTML(entry.operator_id + " · " + entry.currency + " " + entry.amount_minor) +
          '</small><code>' + escapeHTML(entry.evidence_sha256) + '</code></article>';
      }).join("") : '<p>暂无账本记录。</p>') + '</section>';
  }

  function renderFundForm(state) {
    var form = state.form;
    var editing = state.formMode === "edit";
    return '<form class="fundsGovernance__form" data-form="fund">' +
      '<div class="fundsGovernance__formHead"><div><h3>' + (editing ? "编辑资金回调" : "新建资金回调草稿") +
      '</h3><p>金额使用最小货币单位；确认承诺后核心字段冻结。</p></div></div>' +
      '<div class="fundsGovernance__formGrid">' +
      field("候选人 ID", "candidate_id", form.candidate_id, true) +
      field("职位 ID", "job_id", form.job_id) +
      field("Lead ID", "lead_id", form.lead_id) +
      field("入职案例 ID", "onboarding_case_id", form.onboarding_case_id) +
      field("金额（最小单位）", "amount_minor", form.amount_minor, true, "number", "1") +
      field("币种", "currency", form.currency, true, "text", "", "3") +
      field("承诺方", "promised_by", form.promised_by, true) +
      field("到期时间", "due_at", form.due_at, false, "datetime-local") +
      (editing ? '<label>状态<select name="status">' + optionHTML(EDITABLE_STATUSES, form.status) + "</select></label>" : "") +
      '</div><label class="fundsGovernance__wide">触发条件<textarea name="condition" required>' +
      escapeHTML(form.condition) + '</textarea></label><label class="fundsGovernance__wide">证据<textarea name="evidence">' +
      escapeHTML(form.evidence) + '</textarea></label><div class="fundsGovernance__actions">' +
      '<button class="primary" type="submit"' + disabled(state.saving) + '>' + (state.saving ? "保存中..." : "保存") + "</button>" +
      '<button type="button" data-action="cancel-edit"' + disabled(state.saving) + '>取消</button></div></form>';
  }

  function field(label, name, value, required, type, min, maxlength) {
    return '<label>' + escapeHTML(label) + '<input name="' + name + '" type="' + (type || "text") + '" value="' +
      escapeHTML(value) + '"' + (required ? " required" : "") + (min ? ' min="' + min + '"' : "") +
      (maxlength ? ' maxlength="' + maxlength + '"' : "") + "></label>";
  }

  function renderAudit(state) {
    return '<section class="fundsGovernance__panel"><div class="fundsGovernance__panelHead"><h3>CRM 审计事件</h3><span>' +
      escapeHTML(state.audit.total + " 条") + '</span></div><div class="fundsGovernance__panelBody">' +
      '<form class="fundsGovernance__filters fundsGovernance__filters--audit" data-form="audit-filters">' +
      field("操作员", "actor", state.audit.filters.actor) +
      field("操作", "operation", state.audit.filters.operation) +
      field("实体类型", "entity_type", state.audit.filters.entity_type) +
      field("实体 ID", "entity_id", state.audit.filters.entity_id) +
      '<button type="submit"' + disabled(state.audit.loading) + '>筛选</button></form>' +
      renderAuditEvents(state.audit) + "</div></section>";
  }

  function renderAuditEvents(audit) {
    if (audit.loading && !audit.events.length) {
      return '<div class="fundsGovernance__empty">正在加载审计事件...</div>';
    }
    if (!audit.events.length) {
      return '<div class="fundsGovernance__empty"><strong>暂无审计事件</strong><span>完成 CRM 变更后会在这里留痕。</span></div>';
    }
    return '<div class="fundsGovernance__tableWrap"><table class="fundsGovernance__table"><thead><tr>' +
      "<th>时间</th><th>操作员</th><th>操作</th><th>实体</th><th>来源事件</th></tr></thead><tbody>" +
      audit.events.map(function (event) {
        return "<tr><td>" + escapeHTML(formatTime(event.created_at)) + "</td><td>" + escapeHTML(event.actor) +
          "</td><td>" + escapeHTML(event.operation) + "</td><td>" +
          escapeHTML((event.entity_type || "无") + (event.entity_id ? " / " + event.entity_id : "")) +
          "</td><td>" + escapeHTML(event.source_event_id || "无") + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }

  function renderReports(state) {
    return '<div class="fundsGovernance__reports">' +
      '<section class="fundsGovernance__panel"><div class="fundsGovernance__panelHead"><h3>运营摘要</h3>' +
      '<button type="button" data-action="export-report"' + disabled(state.report.exporting) + '>导出 CSV</button></div>' +
      '<div class="fundsGovernance__panelBody">' + renderSummary(state.report.summary) + "</div></section>" +
      '<section class="fundsGovernance__panel"><div class="fundsGovernance__panelHead"><h3>候选人归档与匿名化</h3></div>' +
      '<div class="fundsGovernance__panelBody"><form class="fundsGovernance__governanceSearch" data-form="governance-candidate">' +
      '<label>候选人 ID<input name="candidate_id" value="' + escapeHTML(state.governance.candidateId) + '"></label>' +
      '<button type="submit"' + disabled(state.governance.loading) + '>加载</button></form>' +
      renderGovernanceCandidate(state) + "</div></section></div>";
  }

  function renderSummary(summary) {
    if (!summary) {
      return '<div class="fundsGovernance__empty">正在加载运营摘要...</div>';
    }
    var metrics = [
      ["开放服务请求", summary.open_service_requests],
      ["到期资金回调", summary.due_fund_callbacks],
      ["逾期资金回调", summary.overdue_fund_callbacks],
      ["审计事件", summary.audit_event_count]
    ];
    return '<div class="fundsGovernance__metrics">' + metrics.map(function (entry) {
      return '<div><span>' + escapeHTML(entry[0]) + '</span><strong>' + escapeHTML(entry[1] || 0) + "</strong></div>";
    }).join("") + '</div><div class="fundsGovernance__summaryGrid">' +
      summaryGroup("候选人", summary.candidate_counts) +
      summaryGroup("职位", summary.job_counts) +
      summaryGroup("招聘流程", summary.lead_counts) +
      summaryGroup("入职", summary.onboarding_counts) +
      summaryGroup("服务请求", summary.service_request_counts) +
      summaryGroup("资金回调", summary.fund_callback_counts) +
      summaryGroup("已承诺金额", summary.committed_amount_minor) + "</div>";
  }

  function summaryGroup(label, values) {
    var keys = Object.keys(values || {}).sort();
    return '<section class="fundsGovernance__summaryGroup"><h4>' + escapeHTML(label) + "</h4><dl>" +
      keys.map(function (key) {
        return "<dt>" + escapeHTML(key) + "</dt><dd>" + escapeHTML(values[key]) + "</dd>";
      }).join("") + "</dl></section>";
  }

  function renderGovernanceCandidate(state) {
    var candidate = state.governance.candidate;
    if (!candidate) {
      return '<div class="fundsGovernance__empty"><strong>未加载候选人</strong><span>先核对当前版本，再导出或匿名化。</span></div>';
    }
    return '<article class="fundsGovernance__candidate"><div><h4>' + escapeHTML(candidate.full_name) + '</h4><p>' +
      escapeHTML(candidate.id + " · v" + candidate.version + " · " + candidate.status) + "</p></div>" +
      '<dl class="fundsGovernance__kv">' + detailValue("微信 ID", candidate.wechat_id) +
      detailValue("手机", candidate.phone) + detailValue("邮箱", candidate.email) +
      detailValue("负责人", candidate.owner) + "</dl>" +
      '<div class="fundsGovernance__actions"><button type="button" data-action="export-candidate"' +
      disabled(state.governance.exporting) + '>导出归档</button><button class="danger" type="button" data-action="anonymize-candidate"' +
      disabled(state.governance.anonymizing) + '>匿名化并归档</button></div></article>';
  }

  function bind(root, state) {
    root.addEventListener("click", function (event) {
      var target = event.target.closest("button");
      if (!target) {
        return;
      }
      var tab = target.getAttribute("data-tab");
      if (tab) {
        state.tab = tab;
        state.error = "";
        state.success = "";
        if (tab === "audit" && !state.audit.loaded) {
          loadAudit(state);
        } else if (tab === "reports" && !state.report.summary) {
          loadReport(state);
        } else {
          Workbench.requestRender();
        }
        return;
      }
      var fundID = target.getAttribute("data-fund-id");
      if (fundID) {
        loadFundDetail(state, fundID, false);
        return;
      }
      var action = target.getAttribute("data-action");
      if (!action) {
        return;
      }
      if (action === "refresh") {
        if (state.tab === "funds") {
          loadFunds(state, state.page);
        } else if (state.tab === "audit") {
          loadAudit(state);
        } else {
          loadReport(state);
        }
      } else if (action === "new-fund") {
        state.formMode = "create";
        state.form = blankFundForm();
        state.detail = null;
        state.selectedId = "";
        Workbench.requestRender();
      } else if (action === "edit-fund") {
        state.formMode = "edit";
        state.form = fundForm(state.detail);
        Workbench.requestRender();
      } else if (action === "cancel-edit") {
        state.formMode = "";
        state.form = state.detail ? fundForm(state.detail) : blankFundForm();
        Workbench.requestRender();
      } else if (action === "prev-page") {
        loadFunds(state, state.page - 1);
      } else if (action === "next-page") {
        loadFunds(state, state.page + 1);
      } else if (action === "confirm-commitment") {
        protectedFundAction(state, "commitment");
      } else if (action === "confirm-final") {
        protectedFundAction(state, "final");
      } else if (action === "export-report") {
        exportReport(state);
      } else if (action === "export-candidate") {
        exportCandidate(state);
      } else if (action === "anonymize-candidate") {
        anonymizeCandidate(state);
      }
    });
    root.addEventListener("input", function (event) {
      if (event.target.matches('[data-field="final-result"]')) {
        state.finalResult = event.target.value;
      }
    });
    root.addEventListener("submit", function (event) {
      event.preventDefault();
      var form = event.target;
      if (form.matches('[data-form="fund-filters"]')) {
        state.filters.q = form.elements.q.value;
        state.filters.status = form.elements.status.value;
        state.filters.candidate_id = form.elements.candidate_id.value;
        state.filters.currency = form.elements.currency.value;
        loadFunds(state, 0);
      } else if (form.matches('[data-form="fund"]')) {
        saveFund(state, form);
      } else if (form.matches('[data-form="audit-filters"]')) {
        state.audit.filters.actor = form.elements.actor.value;
        state.audit.filters.operation = form.elements.operation.value;
        state.audit.filters.entity_type = form.elements.entity_type.value;
        state.audit.filters.entity_id = form.elements.entity_id.value;
        loadAudit(state);
      } else if (form.matches('[data-form="governance-candidate"]')) {
        state.governance.candidateId = form.elements.candidate_id.value;
        loadGovernanceCandidate(state);
      }
    });
  }

  var view = {
    id: "funds-governance",
    label: "资金与治理",
    section: "驻场履约",
    order: 56,
    createState: createState,
    render: render,
    onEnter: function (state) {
      return Promise.all([loadFunds(state, 0), loadReport(state)]);
    },
    bind: bind
  };

  Workbench.registerView(view);
}());
