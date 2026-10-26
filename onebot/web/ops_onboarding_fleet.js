(function () {
  "use strict";

  var opsOnboardingFleetAPI = window.OneBotWorkbench;
  if (!opsOnboardingFleetAPI) {
    return;
  }

  var onboardingLabels = {
    pending: "待启动",
    collecting: "资料收集中",
    ready: "已就绪",
    transport_scheduled: "接送已安排",
    en_route: "途中",
    arrived: "已到达",
    checked_in: "已报到",
    completed: "已完成",
    blocked: "受阻",
    cancelled: "已取消"
  };

  var fleetLabels = {
    not_required: "无需车辆",
    pending: "待协调",
    requested: "已请求",
    confirmed: "已确认",
    dispatched: "已派车",
    picked_up: "已接到",
    delivered: "已送达",
    cancelled: "已取消"
  };

  var onboardingTransitions = {
    pending: ["collecting", "ready", "blocked", "cancelled"],
    collecting: ["pending", "ready", "blocked", "cancelled"],
    ready: ["collecting", "transport_scheduled", "en_route", "arrived", "checked_in", "blocked", "cancelled"],
    transport_scheduled: ["ready", "en_route", "blocked", "cancelled"],
    en_route: ["arrived", "blocked", "cancelled"],
    arrived: ["checked_in", "blocked", "cancelled"],
    checked_in: ["completed", "blocked", "cancelled"],
    completed: [],
    blocked: ["pending", "collecting", "ready", "transport_scheduled", "cancelled"],
    cancelled: []
  };

  var fleetTransitions = {
    not_required: [],
    pending: ["requested", "cancelled"],
    requested: ["confirmed", "cancelled"],
    confirmed: ["dispatched", "cancelled"],
    dispatched: ["picked_up", "cancelled"],
    picked_up: ["delivered", "cancelled"],
    delivered: [],
    cancelled: ["pending"]
  };

  function createState() {
    return {
      mode: "cases",
      items: [],
      total: 0,
      selectedID: "",
      detail: null,
      candidates: Object.create(null),
      jobs: Object.create(null),
      hiredLeads: [],
      createLeadID: "",
      filters: { status: "", fleet_status: "", fleet_required: "" },
      onboardingDraft: emptyOnboardingDraft(),
      fleetDraft: emptyFleetDraft(),
      loaded: false,
      referencesLoaded: false,
      loading: false,
      detailLoading: false,
      referencesLoading: false,
      creating: false,
      saving: "",
      transitioning: "",
      fleetTransitioning: "",
      listRequest: 0,
      detailRequest: 0,
      error: "",
      success: "",
      detailError: "",
      batches: {
        items: [],
        selected: null,
        preview: null,
        kind: "onboarding_transition",
        targetStatus: "ready",
        memberIDs: "",
        loading: false,
        saving: false,
        executing: false
      }
    };
  }

  function emptyOnboardingDraft() {
    return {
      entry_at: "",
      entry_address: "",
      site_contact: "",
      required_documents: "",
      completed_documents: "",
      missing_information: "",
      block_reason: ""
    };
  }

  function emptyFleetDraft() {
    return {
      required: false,
      pickup_address: "",
      pickup_at: "",
      destination: "",
      luggage_note: "",
      vehicle_requirement: "",
      fleet_contact: "",
      driver_name: "",
      driver_phone: "",
      vehicle_plate: ""
    };
  }

  function loadReferences(state, api) {
    state.referencesLoading = true;
    api.requestRender();
    return Promise.all([
      api.api("/api/crm/candidates?limit=200"),
      api.api("/api/crm/jobs?limit=200"),
      api.api("/api/crm/leads?stage=hired&limit=200")
    ]).then(function (responses) {
      state.candidates = Object.create(null);
      state.jobs = Object.create(null);
      (responses[0] && responses[0].candidates || []).forEach(function (item) {
        if (item && item.candidate) {
          state.candidates[item.candidate.id] = item.candidate;
        }
      });
      (responses[1] && responses[1].jobs || []).forEach(function (item) {
        if (item && item.job) {
          state.jobs[item.job.id] = item.job;
        }
      });
      state.hiredLeads = responses[2] && Array.isArray(responses[2].leads) ? responses[2].leads : [];
      state.referencesLoaded = true;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.referencesLoading = false;
      api.requestRender();
    });
  }

  function loadList(state, api) {
    var requestID = state.listRequest + 1;
    state.listRequest = requestID;
    state.loading = true;
    state.error = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases" + api.encodeQuery({
      status: state.filters.status,
      fleet_status: state.filters.fleet_status,
      fleet_required: state.filters.fleet_required,
      limit: 200
    })).then(function (data) {
      if (requestID !== state.listRequest) {
        return false;
      }
      state.items = data && Array.isArray(data.onboarding_cases) ? data.onboarding_cases : [];
      state.total = Number(data && data.total || 0);
      state.loaded = true;
      if (state.selectedID && !state.items.some(function (item) { return item.id === state.selectedID; })) {
        state.selectedID = "";
        state.detail = null;
      }
      return true;
    }).catch(function (err) {
      if (requestID === state.listRequest) {
        state.error = err.message;
      }
      return false;
    }).finally(function () {
      if (requestID === state.listRequest) {
        state.loading = false;
        api.requestRender();
      }
    });
  }

  function loadBatches(state, api) {
    state.batches.loading = true;
    state.error = "";
    api.requestRender();
    return api.api("/api/ops/batches").then(function (data) {
      state.batches.items = data && Array.isArray(data.batches) ? data.batches : [];
      if (state.batches.selected) {
        state.batches.selected = state.batches.items.find(function (item) {
          return item.id === state.batches.selected.id;
        }) || null;
      }
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.batches.loading = false;
      api.requestRender();
    });
  }

  function loadBatchPreview(state, api, id) {
    state.batches.loading = true;
    api.requestRender();
    return api.api("/api/ops/batches/" + encodeURIComponent(id) + "/preview").then(function (preview) {
      state.batches.preview = preview;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.batches.loading = false;
      api.requestRender();
    });
  }

  function createBatch(state, api, form) {
    state.batches.kind = form.kind.value;
    state.batches.targetStatus = form.target_status.value;
    state.batches.saving = true;
    state.error = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/ops/batches", {
      method: "POST",
      body: {
        kind: state.batches.kind,
        target_status: state.batches.targetStatus
      }
    }).then(function (batch) {
      state.batches.selected = batch;
      state.batches.preview = null;
      state.success = "批次草稿已创建。";
      return loadBatches(state, api);
    }).finally(function () {
      state.batches.saving = false;
      api.requestRender();
    }).catch(function (err) {
      state.error = err.message;
    });
  }

  function saveBatchMembers(state, api, form) {
    if (!state.batches.selected) {
      return;
    }
    state.batches.memberIDs = form.member_ids.value;
    var ids = splitLines(state.batches.memberIDs);
    if (!ids.length) {
      state.error = "至少填写一个入职交接 ID。";
      api.requestRender();
      return;
    }
    state.batches.saving = true;
    state.error = "";
    api.requestRender();
    return api.api("/api/ops/batches/" + encodeURIComponent(state.batches.selected.id) + "/members", {
      method: "POST",
      body: {
        expected_version: state.batches.selected.version,
        onboarding_case_ids: ids,
        replace: true
      }
    }).then(function (batch) {
      state.batches.selected = batch;
      state.success = "批次成员与预期版本已锁定。";
      return Promise.all([loadBatches(state, api), loadBatchPreview(state, api, batch.id)]);
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.batches.saving = false;
      api.requestRender();
    });
  }

  function executeBatch(state, api) {
    var batch = state.batches.selected;
    var preview = state.batches.preview;
    if (!batch || !preview) {
      return;
    }
    if (!window.confirm("执行当前预览批次？每项结果写入后不可修改，失败项不会自动重试。")) {
      return;
    }
    state.batches.executing = true;
    state.error = "";
    api.requestRender();
    var request = preview.protected ? api.automationAPI : api.api;
    return request("/api/ops/batches/" + encodeURIComponent(batch.id) + "/execute", {
      method: "POST",
      body: {
        expected_version: preview.batch_version,
        confirmed: true
      }
    }).then(function (completed) {
      state.batches.selected = completed;
      state.success = "批次执行完成；逐项结果已冻结。";
      return Promise.all([loadBatches(state, api), loadList(state, api)]);
    }).catch(function (err) {
      state.error = err.message;
      if (isStale(err)) {
        return loadBatchPreview(state, api, batch.id);
      }
      return null;
    }).finally(function () {
      state.batches.executing = false;
      api.requestRender();
    });
  }

  function loadDetail(state, api, id, preserveDraft) {
    var requestID = state.detailRequest + 1;
    state.detailRequest = requestID;
    state.detailLoading = true;
    state.detailError = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases/" + encodeURIComponent(id)).then(function (detail) {
      if (requestID !== state.detailRequest || state.selectedID !== id) {
        return false;
      }
      state.detail = detail;
      if (!preserveDraft) {
        state.onboardingDraft = onboardingDraftFrom(detail);
        state.fleetDraft = fleetDraftFrom(detail.fleet || {});
      }
      return true;
    }).catch(function (err) {
      if (requestID === state.detailRequest && state.selectedID === id) {
        state.detailError = err.message;
      }
      return false;
    }).finally(function () {
      if (requestID === state.detailRequest) {
        state.detailLoading = false;
        api.requestRender();
      }
    });
  }

  function onboardingDraftFrom(detail) {
    return {
      entry_at: toLocalInput(detail.entry_at),
      entry_address: detail.entry_address || "",
      site_contact: detail.site_contact || "",
      required_documents: joinLines(detail.required_documents),
      completed_documents: joinLines(detail.completed_documents),
      missing_information: joinLines(detail.missing_information),
      block_reason: detail.block_reason || ""
    };
  }

  function fleetDraftFrom(fleet) {
    return {
      required: !!fleet.required,
      pickup_address: fleet.pickup_address || "",
      pickup_at: toLocalInput(fleet.pickup_at),
      destination: fleet.destination || "",
      luggage_note: fleet.luggage_note || "",
      vehicle_requirement: fleet.vehicle_requirement || "",
      fleet_contact: fleet.fleet_contact || "",
      driver_name: fleet.driver_name || "",
      driver_phone: fleet.driver_phone || "",
      vehicle_plate: fleet.vehicle_plate || ""
    };
  }

  function createCase(state, api, form) {
    state.createLeadID = form.lead_id.value;
    state.creating = true;
    state.error = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases", {
      method: "POST",
      body: { lead_id: state.createLeadID }
    }).then(function (created) {
      state.selectedID = created.id;
      state.createLeadID = "";
      return loadList(state, api);
    }).then(function () {
      return loadDetail(state, api, state.selectedID, false);
    }).then(function () {
      state.success = "已创建入职交接单。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.creating = false;
      api.requestRender();
    });
  }

  function saveOnboarding(state, api, form) {
    if (!state.detail) {
      return;
    }
    readOnboardingDraft(state, form);
    var body = {
      expected_version: state.detail.version,
      entry_address: state.onboardingDraft.entry_address,
      site_contact: state.onboardingDraft.site_contact,
      required_documents: splitLines(state.onboardingDraft.required_documents),
      completed_documents: splitLines(state.onboardingDraft.completed_documents),
      missing_information: splitLines(state.onboardingDraft.missing_information),
      block_reason: state.onboardingDraft.block_reason
    };
    if (state.onboardingDraft.entry_at) {
      body.entry_at = toISO(state.onboardingDraft.entry_at);
    } else {
      body.clear_entry_at = true;
    }
    state.saving = "onboarding";
    state.detailError = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases/" + encodeURIComponent(state.detail.id), {
      method: "PATCH",
      body: body
    }).then(function (updated) {
      state.detail = updated;
      state.onboardingDraft = onboardingDraftFrom(updated);
      return loadList(state, api);
    }).then(function () {
      state.success = "入职资料已保存。";
    }).catch(function (err) {
      if (isStale(err)) {
        return recoverStale(state, api, "数据已更新，已保留当前入职草稿并刷新版本。");
      }
      state.detailError = err.message;
      return null;
    }).finally(function () {
      state.saving = "";
      api.requestRender();
    });
  }

  function saveFleet(state, api, form) {
    if (!state.detail) {
      return;
    }
    readFleetDraft(state, form);
    var body = {
      expected_version: state.detail.version,
      required: state.fleetDraft.required,
      pickup_address: state.fleetDraft.pickup_address,
      destination: state.fleetDraft.destination,
      luggage_note: state.fleetDraft.luggage_note,
      vehicle_requirement: state.fleetDraft.vehicle_requirement,
      fleet_contact: state.fleetDraft.fleet_contact,
      driver_name: state.fleetDraft.driver_name,
      driver_phone: state.fleetDraft.driver_phone,
      vehicle_plate: state.fleetDraft.vehicle_plate
    };
    if (state.fleetDraft.pickup_at) {
      body.pickup_at = toISO(state.fleetDraft.pickup_at);
    } else {
      body.clear_pickup_at = true;
    }
    state.saving = "fleet";
    state.detailError = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases/" + encodeURIComponent(state.detail.id) + "/fleet", {
      method: "PATCH",
      body: body
    }).then(function (updated) {
      state.detail = updated;
      state.fleetDraft = fleetDraftFrom(updated.fleet || {});
      return loadList(state, api);
    }).then(function () {
      state.success = "车队协调信息已保存。";
    }).catch(function (err) {
      if (isStale(err)) {
        return recoverStale(state, api, "数据已更新，已保留当前车队草稿并刷新版本。");
      }
      state.detailError = err.message;
      return null;
    }).finally(function () {
      state.saving = "";
      api.requestRender();
    });
  }

  function transitionOnboarding(state, api, toStatus) {
    if (!state.detail) {
      return;
    }
    state.transitioning = toStatus;
    state.detailError = "";
    state.success = "";
    api.requestRender();
    var protectedStatus = toStatus === "completed" || toStatus === "cancelled";
    if (protectedStatus && !window.confirm("确认执行受保护的入职状态变更？")) {
      state.transitioning = "";
      api.requestRender();
      return;
    }
    var request = protectedStatus ? api.automationAPI : api.api;
    return request("/api/ops/onboarding_cases/" + encodeURIComponent(state.detail.id) + "/transition", {
      method: "POST",
      body: {
        expected_version: state.detail.version,
        from_status: state.detail.status,
        to_status: toStatus,
        block_reason: toStatus === "blocked" ? state.onboardingDraft.block_reason : "",
        confirmed: protectedStatus
      }
    }).then(function (updated) {
      state.detail = updated;
      state.onboardingDraft = onboardingDraftFrom(updated);
      return loadList(state, api);
    }).then(function () {
      state.success = "入职状态已更新。";
    }).catch(function (err) {
      if (isStale(err)) {
        return recoverStale(state, api, "状态已被其他操作更新，已保留草稿并刷新。");
      }
      state.detailError = err.message;
      return null;
    }).finally(function () {
      state.transitioning = "";
      api.requestRender();
    });
  }

  function transitionFleet(state, api, toStatus) {
    if (!state.detail) {
      return;
    }
    state.fleetTransitioning = toStatus;
    state.detailError = "";
    state.success = "";
    api.requestRender();
    var protectedStatus = ["confirmed", "dispatched", "delivered", "cancelled"].indexOf(toStatus) >= 0;
    if (protectedStatus && !window.confirm("确认执行受保护的车队状态变更？")) {
      state.fleetTransitioning = "";
      api.requestRender();
      return;
    }
    var request = protectedStatus ? api.automationAPI : api.api;
    return request("/api/ops/onboarding_cases/" + encodeURIComponent(state.detail.id) + "/fleet/transition", {
      method: "POST",
      body: {
        expected_version: state.detail.version,
        from_status: state.detail.fleet.status,
        to_status: toStatus,
        confirmed: protectedStatus
      }
    }).then(function (updated) {
      state.detail = updated;
      state.fleetDraft = fleetDraftFrom(updated.fleet || {});
      return loadList(state, api);
    }).then(function () {
      state.success = "车队状态已更新。";
    }).catch(function (err) {
      if (isStale(err)) {
        return recoverStale(state, api, "车队状态已被更新，已保留草稿并刷新。");
      }
      state.detailError = err.message;
      return null;
    }).finally(function () {
      state.fleetTransitioning = "";
      api.requestRender();
    });
  }

  function recoverStale(state, api, message) {
    return Promise.all([
      loadList(state, api),
      loadDetail(state, api, state.selectedID, true)
    ]).then(function () {
      state.detailError = message;
      api.requestRender();
    });
  }

  function render(state, api) {
    var busy = state.loading ? "正在加载入职交接队列..." :
      state.referencesLoading ? "正在加载候选人与需求..." : "";
    return '<div class="opsOnboardingFleet-root">' +
      '<section class="topbar"><div><h2>入职与车队</h2><p>处理已录用人员的资料、到场和接送交接。</p></div>' +
      '<button class="primary" type="button" data-onboarding-action="refresh" ' + disabled(state.loading) + '>刷新</button></section>' +
      '<div class="opsOnboardingFleet-tabs"><button type="button" data-onboarding-mode="cases" aria-selected="' +
      (state.mode === "cases" ? "true" : "false") + '">单例交接</button><button type="button" data-onboarding-mode="batches" aria-selected="' +
      (state.mode === "batches" ? "true" : "false") + '">操作批次</button></div>' +
      (state.mode === "batches" ? renderBatches(state, api) :
      '<section class="opsOnboardingFleet-toolbar">' +
      renderFilters(state, api) +
      renderCreate(state, api) +
      '</section>' +
      api.renderStatus(state.error, state.success, busy) +
      '<div class="opsOnboardingFleet-workspace">' +
      renderQueue(state, api) +
      renderDetail(state, api) +
      '</div>') + '</div>';
  }

  function renderBatches(state, api) {
    var batch = state.batches.selected;
    return api.renderStatus(state.error, state.success, state.batches.loading ? "正在加载批次..." : "") +
      '<section class="opsOnboardingFleet-batches"><div class="opsOnboardingFleet-batchList">' +
      '<form data-onboarding-form="batch-create"><label>类型<select name="kind">' +
      '<option value="onboarding_transition">入职状态</option><option value="fleet_transition">车队状态</option></select></label>' +
      '<label>目标状态<input name="target_status" value="' + api.escapeHTML(state.batches.targetStatus) +
      '" required></label><button class="primary" type="submit"' + disabled(state.batches.saving) + '>新建草稿</button></form>' +
      '<div>' + (state.batches.items.length ? state.batches.items.map(function (item) {
        return '<button type="button" data-batch-id="' + api.escapeHTML(item.id) + '" aria-current="' +
          (batch && batch.id === item.id ? "true" : "false") + '"><strong>' +
          api.escapeHTML(item.target_status) + '</strong><span>' + api.escapeHTML(item.kind + " · v" + item.version) +
          '</span><small>' + api.escapeHTML(item.status) + '</small></button>';
      }).join("") : '<p>暂无操作批次。</p>') + '</div></div>' +
      '<div class="opsOnboardingFleet-batchDetail">' +
      (batch ? renderBatchDetail(state, api, batch) : '<div class="opsOnboardingFleet-empty">选择或创建批次草稿。</div>') +
      '</div></section>';
  }

  function renderBatchDetail(state, api, batch) {
    var preview = state.batches.preview;
    return '<header><div><h3>' + api.escapeHTML(batch.target_status) + '</h3><p>' +
      api.escapeHTML(batch.id + " · " + batch.kind + " · v" + batch.version) + '</p></div><span>' +
      api.escapeHTML(batch.status) + '</span></header>' +
      (batch.status === "draft" ? '<form data-onboarding-form="batch-members"><label>入职交接 ID<textarea name="member_ids" ' +
        'placeholder="每行一个 ID">' + api.escapeHTML(state.batches.memberIDs || batch.members.map(function (member) {
          return member.onboarding_case_id;
        }).join("\n")) + '</textarea></label><button type="submit"' + disabled(state.batches.saving) +
        '>保存草稿成员</button></form><div class="opsOnboardingFleet-batchActions"><button type="button" data-onboarding-action="preview-batch"' +
        disabled(state.batches.loading) + '>刷新只读预览</button>' +
        (preview ? '<button class="primary" type="button" data-onboarding-action="execute-batch"' +
          disabled(state.batches.executing) + '>执行当前预览</button>' : "") + '</div>' : "") +
      renderBatchPreview(state, api, preview) +
      (batch.results && batch.results.length ? '<div class="opsOnboardingFleet-batchResults"><h4>不可变结果</h4>' +
        batch.results.map(function (result) {
          return '<div><strong>' + api.escapeHTML(result.onboarding_case_id) + '</strong><span>' +
            api.escapeHTML(result.from_status + " -> " + result.to_status) + '</span><b>' +
            api.escapeHTML(result.outcome) + '</b><small>' + api.escapeHTML(result.error_message || "") + '</small></div>';
        }).join("") + '</div>' : "");
  }

  function renderBatchPreview(state, api, preview) {
    if (!preview) {
      return "";
    }
    return '<div class="opsOnboardingFleet-preview"><div><h4>只读精确预览</h4><span>batch v' +
      api.escapeHTML(preview.batch_version) + (preview.protected ? " · 受保护确认" : "") + '</span></div>' +
      preview.items.map(function (item) {
        return '<article class="' + (item.eligible ? "is-eligible" : "is-ineligible") + '"><strong>' +
          api.escapeHTML(item.onboarding_case_id) + '</strong><span>v' + api.escapeHTML(item.expected_version) +
          ' / 当前 v' + api.escapeHTML(item.current_version || 0) + '</span><code>' +
          api.escapeHTML(item.before.onboarding_status + " / " + item.before.fleet_status + " -> " +
            item.after.onboarding_status + " / " + item.after.fleet_status) + '</code><small>' +
          api.escapeHTML(item.error_message || "可执行") + '</small></article>';
      }).join("") + '</div>';
  }

  function renderFilters(state, api) {
    return '<form class="opsOnboardingFleet-filterForm" data-onboarding-form="filters">' +
      '<label>入职状态<select name="status"><option value="">全部</option>' +
      options(onboardingLabels, state.filters.status) + '</select></label>' +
      '<label>车队状态<select name="fleet_status"><option value="">全部</option>' +
      options(fleetLabels, state.filters.fleet_status) + '</select></label>' +
      '<label>车辆需求<select name="fleet_required"><option value="">全部</option>' +
      '<option value="true"' + selected(state.filters.fleet_required, "true") + '>需要车辆</option>' +
      '<option value="false"' + selected(state.filters.fleet_required, "false") + '>无需车辆</option></select></label>' +
      '<button type="submit" ' + disabled(state.loading) + '>应用</button></form>';
  }

  function renderCreate(state, api) {
    var existing = Object.create(null);
    state.items.forEach(function (item) { existing[item.lead_id] = true; });
    var leads = state.hiredLeads.filter(function (item) {
      return item && item.lead && !existing[item.lead.id];
    });
    return '<form class="opsOnboardingFleet-createForm" data-onboarding-form="create">' +
      '<label>补建已录用交接<select name="lead_id" required ' + disabled(state.creating || state.referencesLoading) + '>' +
      '<option value="">选择招聘记录</option>' + leads.map(function (item) {
        var label = (item.candidate_name || item.lead.candidate_id) + " · " + (item.job_title || item.lead.job_id);
        return '<option value="' + api.escapeHTML(item.lead.id) + '"' + selected(state.createLeadID, item.lead.id) + '>' +
          api.escapeHTML(label) + '</option>';
      }).join("") + '</select></label>' +
      '<button class="primary" type="submit" ' + disabled(state.creating || !leads.length) + '>' +
      (state.creating ? "创建中..." : "创建交接") + '</button></form>';
  }

  function renderQueue(state, api) {
    return '<section class="opsOnboardingFleet-queue" aria-label="入职交接队列">' +
      '<header><h3>交接队列</h3><span>' + api.escapeHTML(state.total) + ' 条</span></header>' +
      '<div class="opsOnboardingFleet-list">' +
      (state.items.length ? state.items.map(function (item) {
        var candidate = state.candidates[item.candidate_id] || {};
        var job = state.jobs[item.job_id] || {};
        return '<button type="button" class="opsOnboardingFleet-row" data-onboarding-id="' +
          api.escapeHTML(item.id) + '" aria-current="' + (state.selectedID === item.id ? "true" : "false") + '">' +
          '<span><strong>' + api.escapeHTML(candidate.full_name || item.candidate_id) + '</strong>' +
          '<small>' + api.escapeHTML(job.title || item.job_id) + '</small></span>' +
          '<span class="opsOnboardingFleet-rowStatus"><b>' + api.escapeHTML(onboardingLabels[item.status] || item.status) +
          '</b><small>' + api.escapeHTML(fleetLabels[item.fleet && item.fleet.status] || "") + '</small></span></button>';
      }).join("") : '<div class="opsOnboardingFleet-empty">当前筛选下没有入职交接单。</div>') +
      '</div></section>';
  }

  function renderDetail(state, api) {
    if (!state.selectedID) {
      return '<section class="opsOnboardingFleet-detailEmpty"><strong>交接详情</strong><span>从左侧选择记录后维护资料和车队子流程。</span></section>';
    }
    if (!state.detail) {
      return '<section class="opsOnboardingFleet-detailEmpty">' +
        api.renderStatus(state.detailError, "", state.detailLoading ? "正在加载详情..." : "") + '</section>';
    }
    var item = state.detail;
    var candidate = state.candidates[item.candidate_id] || {};
    var job = state.jobs[item.job_id] || {};
    return '<section class="opsOnboardingFleet-detail" aria-label="入职交接详情">' +
      '<header class="opsOnboardingFleet-detailHeader"><div><h3>' +
      api.escapeHTML(candidate.full_name || item.candidate_id) + '</h3><p>' +
      api.escapeHTML(job.title || item.job_id) + '</p></div><div><span>' +
      api.escapeHTML(onboardingLabels[item.status] || item.status) + '</span><small>v' +
      api.escapeHTML(item.version) + '</small></div></header>' +
      api.renderStatus(state.detailError, "", state.detailLoading ? "正在刷新详情..." : "") +
      '<div class="opsOnboardingFleet-detailGrid">' +
      renderOnboardingEditor(state, api) +
      renderFleetEditor(state, api) +
      '</div></section>';
  }

  function renderOnboardingEditor(state, api) {
    var item = state.detail;
    var transitions = onboardingTransitions[item.status] || [];
    return '<div class="opsOnboardingFleet-editor">' +
      '<div class="opsOnboardingFleet-editorTitle"><h4>入职交接</h4><span>' + api.escapeHTML(item.id) + '</span></div>' +
      '<form class="opsOnboardingFleet-fields" data-onboarding-form="onboarding">' +
      '<label>到场时间<input name="entry_at" type="datetime-local" value="' + api.escapeHTML(state.onboardingDraft.entry_at) + '"></label>' +
      '<label>到场地址<textarea name="entry_address" maxlength="500">' + api.escapeHTML(state.onboardingDraft.entry_address) + '</textarea></label>' +
      '<label>现场联系人<input name="site_contact" maxlength="160" value="' + api.escapeHTML(state.onboardingDraft.site_contact) + '"></label>' +
      '<label>所需材料<textarea name="required_documents" placeholder="每行一项">' + api.escapeHTML(state.onboardingDraft.required_documents) + '</textarea></label>' +
      '<label>已完成材料<textarea name="completed_documents" placeholder="每行一项">' + api.escapeHTML(state.onboardingDraft.completed_documents) + '</textarea></label>' +
      '<label>缺失信息<textarea name="missing_information" placeholder="每行一项">' + api.escapeHTML(state.onboardingDraft.missing_information) + '</textarea></label>' +
      '<label>受阻原因<textarea name="block_reason" maxlength="2000">' + api.escapeHTML(state.onboardingDraft.block_reason) + '</textarea></label>' +
      '<button class="primary" type="submit" ' + disabled(!!state.saving || !!state.transitioning) + '>' +
      (state.saving === "onboarding" ? "保存中..." : "保存入职资料") + '</button></form>' +
      '<div class="opsOnboardingFleet-transitions"><span>推进状态</span><div>' +
      (transitions.length ? transitions.map(function (status) {
        return '<button type="button" data-onboarding-transition="' + status + '" ' +
          disabled(!!state.saving || !!state.transitioning) + '>' +
          (state.transitioning === status ? "更新中..." : api.escapeHTML(onboardingLabels[status])) + '</button>';
      }).join("") : '<small>当前状态无后续操作</small>') + '</div></div></div>';
  }

  function renderFleetEditor(state, api) {
    var fleet = state.detail.fleet || {};
    var transitions = fleetTransitions[fleet.status] || [];
    return '<div class="opsOnboardingFleet-editor">' +
      '<div class="opsOnboardingFleet-editorTitle"><h4>车队协调</h4><span>' +
      api.escapeHTML(fleetLabels[fleet.status] || fleet.status) + '</span></div>' +
      '<form class="opsOnboardingFleet-fields" data-onboarding-form="fleet">' +
      '<label class="opsOnboardingFleet-check"><input name="required" type="checkbox"' +
      (state.fleetDraft.required ? " checked" : "") + '><span>需要车辆接送</span></label>' +
      '<label>接人时间<input name="pickup_at" type="datetime-local" value="' + api.escapeHTML(state.fleetDraft.pickup_at) + '"></label>' +
      '<label>接人地址<textarea name="pickup_address" maxlength="500">' + api.escapeHTML(state.fleetDraft.pickup_address) + '</textarea></label>' +
      '<label>目的地<textarea name="destination" maxlength="500">' + api.escapeHTML(state.fleetDraft.destination) + '</textarea></label>' +
      '<label>行李说明<textarea name="luggage_note" maxlength="1000">' + api.escapeHTML(state.fleetDraft.luggage_note) + '</textarea></label>' +
      '<label>车型要求<input name="vehicle_requirement" maxlength="500" value="' + api.escapeHTML(state.fleetDraft.vehicle_requirement) + '"></label>' +
      '<label>车队联系人<input name="fleet_contact" maxlength="160" value="' + api.escapeHTML(state.fleetDraft.fleet_contact) + '"></label>' +
      '<div class="opsOnboardingFleet-inline"><label>司机<input name="driver_name" maxlength="160" value="' + api.escapeHTML(state.fleetDraft.driver_name) + '"></label>' +
      '<label>司机电话<input name="driver_phone" maxlength="40" value="' + api.escapeHTML(state.fleetDraft.driver_phone) + '"></label></div>' +
      '<label>车牌<input name="vehicle_plate" maxlength="40" value="' + api.escapeHTML(state.fleetDraft.vehicle_plate) + '"></label>' +
      '<button class="primary" type="submit" ' + disabled(!!state.saving || !!state.fleetTransitioning) + '>' +
      (state.saving === "fleet" ? "保存中..." : "保存车队信息") + '</button></form>' +
      '<div class="opsOnboardingFleet-transitions"><span>车队状态</span><div>' +
      (transitions.length ? transitions.map(function (status) {
        return '<button type="button" data-fleet-transition="' + status + '" ' +
          disabled(!!state.saving || !!state.fleetTransitioning) + '>' +
          (state.fleetTransitioning === status ? "更新中..." : api.escapeHTML(fleetLabels[status])) + '</button>';
      }).join("") : '<small>当前状态无后续操作</small>') + '</div></div></div>';
  }

  function bind(root, state, api) {
    root.addEventListener("click", function (event) {
      var row = event.target.closest("[data-onboarding-id]");
      if (row && root.contains(row)) {
        state.selectedID = row.getAttribute("data-onboarding-id");
        state.detail = null;
        api.requestRender();
        loadDetail(state, api, state.selectedID, false);
        return;
      }
      var mode = event.target.closest("[data-onboarding-mode]");
      if (mode && root.contains(mode)) {
        state.mode = mode.getAttribute("data-onboarding-mode");
        api.requestRender();
        if (state.mode === "batches" && !state.batches.items.length) {
          loadBatches(state, api);
        }
        return;
      }
      var batchRow = event.target.closest("[data-batch-id]");
      if (batchRow && root.contains(batchRow)) {
        var id = batchRow.getAttribute("data-batch-id");
        state.batches.selected = state.batches.items.find(function (item) { return item.id === id; }) || null;
        state.batches.preview = null;
        state.batches.memberIDs = "";
        api.requestRender();
        return;
      }
      var transition = event.target.closest("[data-onboarding-transition]");
      if (transition && root.contains(transition)) {
        transitionOnboarding(state, api, transition.getAttribute("data-onboarding-transition"));
        return;
      }
      var fleetTransition = event.target.closest("[data-fleet-transition]");
      if (fleetTransition && root.contains(fleetTransition)) {
        transitionFleet(state, api, fleetTransition.getAttribute("data-fleet-transition"));
        return;
      }
      var action = event.target.closest("[data-onboarding-action]");
      if (action && root.contains(action)) {
        var actionName = action.getAttribute("data-onboarding-action");
        if (actionName === "refresh") {
          if (state.mode === "batches") {
            loadBatches(state, api);
          } else {
            loadList(state, api);
            loadReferences(state, api);
            if (state.selectedID) {
              loadDetail(state, api, state.selectedID, false);
            }
          }
        } else if (actionName === "preview-batch" && state.batches.selected) {
          loadBatchPreview(state, api, state.batches.selected.id);
        } else if (actionName === "execute-batch") {
          executeBatch(state, api);
        }
      }
    });

    root.addEventListener("submit", function (event) {
      var form = event.target.closest("[data-onboarding-form]");
      if (!form || !root.contains(form)) {
        return;
      }
      event.preventDefault();
      var kind = form.getAttribute("data-onboarding-form");
      if (kind === "filters") {
        state.filters.status = form.status.value;
        state.filters.fleet_status = form.fleet_status.value;
        state.filters.fleet_required = form.fleet_required.value;
        loadList(state, api);
      } else if (kind === "create") {
        createCase(state, api, form);
      } else if (kind === "onboarding") {
        saveOnboarding(state, api, form);
      } else if (kind === "fleet") {
        saveFleet(state, api, form);
      } else if (kind === "batch-create") {
        createBatch(state, api, form);
      } else if (kind === "batch-members") {
        saveBatchMembers(state, api, form);
      }
    });
  }

  function readOnboardingDraft(state, form) {
    state.onboardingDraft = {
      entry_at: form.entry_at.value,
      entry_address: form.entry_address.value.trim(),
      site_contact: form.site_contact.value.trim(),
      required_documents: form.required_documents.value,
      completed_documents: form.completed_documents.value,
      missing_information: form.missing_information.value,
      block_reason: form.block_reason.value.trim()
    };
  }

  function readFleetDraft(state, form) {
    state.fleetDraft = {
      required: form.required.checked,
      pickup_address: form.pickup_address.value.trim(),
      pickup_at: form.pickup_at.value,
      destination: form.destination.value.trim(),
      luggage_note: form.luggage_note.value.trim(),
      vehicle_requirement: form.vehicle_requirement.value.trim(),
      fleet_contact: form.fleet_contact.value.trim(),
      driver_name: form.driver_name.value.trim(),
      driver_phone: form.driver_phone.value.trim(),
      vehicle_plate: form.vehicle_plate.value.trim()
    };
  }

  function onEnter(state, api) {
    var work = [];
    if (!state.loaded && !state.loading) {
      work.push(loadList(state, api));
    }
    if (!state.referencesLoaded && !state.referencesLoading) {
      work.push(loadReferences(state, api));
    }
    if (!state.batches.items.length && !state.batches.loading) {
      work.push(loadBatches(state, api));
    }
    return Promise.all(work);
  }

  function splitLines(value) {
    var seen = Object.create(null);
    return String(value || "").split(/\r?\n/).map(function (item) {
      return item.trim();
    }).filter(function (item) {
      if (!item || seen[item]) {
        return false;
      }
      seen[item] = true;
      return true;
    });
  }

  function joinLines(values) {
    return Array.isArray(values) ? values.join("\n") : "";
  }

  function toISO(value) {
    return new Date(value).toISOString();
  }

  function toLocalInput(value) {
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

  function isStale(err) {
    return /(^|\s)409(\s|$)|conflict|stale|version|版本|冲突/i.test(String(err && err.message || ""));
  }

  function options(labels, current) {
    return Object.keys(labels).map(function (value) {
      return '<option value="' + value + '"' + selected(current, value) + '>' + labels[value] + '</option>';
    }).join("");
  }

  function selected(current, value) {
    return current === value ? " selected" : "";
  }

  function disabled(value) {
    return value ? "disabled" : "";
  }

  opsOnboardingFleetAPI.registerView({
    id: "onboarding-fleet",
    label: "入职与车队",
    section: "驻场履约",
    order: 45,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  });
})();
