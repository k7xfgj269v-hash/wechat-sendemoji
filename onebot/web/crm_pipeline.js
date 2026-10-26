(function () {
  "use strict";

  var crmPipelineViewAPI = window.OneBotWorkbench;
  if (!crmPipelineViewAPI) {
    return;
  }

  var crmPipelineViewStages = [
    "sourced",
    "screening",
    "interview",
    "offer",
    "hired",
    "rejected",
    "withdrawn"
  ];

  var crmPipelineViewStageLabels = {
    sourced: "已寻访",
    screening: "筛选中",
    interview: "面试中",
    offer: "已发 Offer",
    hired: "已入职",
    rejected: "已拒绝",
    withdrawn: "已退出"
  };
  var crmPipelineViewTransitionContract = {
    sourced: ["screening", "rejected", "withdrawn"],
    screening: ["interview", "rejected", "withdrawn"],
    interview: ["offer", "rejected", "withdrawn"],
    offer: ["hired", "rejected", "withdrawn"],
    hired: [],
    rejected: [],
    withdrawn: []
  };

  function crmPipelineViewCreateState() {
    return {
      stages: crmPipelineViewEmptyStages(),
      total: 0,
      candidates: [],
      jobs: [],
      jobID: "",
      owner: "",
      selectedID: "",
      notes: [],
      notesLeadID: "",
      createCandidateID: "",
      createJobID: "",
      createOwner: "",
      ownerDraft: "",
      noteAuthor: "",
      noteBody: "",
      handoff: null,
      handoffLeadID: "",
      loaded: false,
      optionsLoaded: false,
      loading: false,
      optionsLoading: false,
      creating: false,
      ownerSaving: false,
      transitioning: "",
      notesLoading: false,
      noteSaving: false,
      handoffLoading: false,
      handoffCreating: false,
      boardRequest: 0,
      notesRequest: 0,
      error: "",
      success: "",
      detailError: ""
    };
  }

  function crmPipelineViewEmptyStages() {
    return crmPipelineViewStages.map(function (stage) {
      return { stage: stage, leads: [] };
    });
  }

  function crmPipelineViewNormalizeStages(data) {
    var grouped = Object.create(null);
    crmPipelineViewStages.forEach(function (stage) {
      grouped[stage] = [];
    });
    var stages = data && Array.isArray(data.stages) ? data.stages : [];
    stages.forEach(function (stage) {
      if (stage && Object.prototype.hasOwnProperty.call(grouped, stage.stage) && Array.isArray(stage.leads)) {
        grouped[stage.stage] = stage.leads;
      }
    });
    return crmPipelineViewStages.map(function (stage) {
      return { stage: stage, leads: grouped[stage] };
    });
  }

  function crmPipelineViewFindLead(state, leadID) {
    var found = null;
    state.stages.some(function (stage) {
      return stage.leads.some(function (item) {
        if (item && item.lead && item.lead.id === leadID) {
          found = item;
          return true;
        }
        return false;
      });
    });
    return found;
  }

  function crmPipelineViewAllowedTransitions(item) {
    if (item && Array.isArray(item.allowed_transitions)) {
      return item.allowed_transitions;
    }
    var stage = item && item.lead && item.lead.stage;
    return crmPipelineViewTransitionContract[stage] || [];
  }

  function crmPipelineViewTerminalStage(stage) {
    return stage === "hired" || stage === "rejected" || stage === "withdrawn";
  }

  function crmPipelineViewLoadBoard(state, api) {
    var requestID = state.boardRequest + 1;
    state.boardRequest = requestID;
    state.loading = true;
    state.error = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/crm/pipeline" + api.encodeQuery({
      job_id: state.jobID,
      owner: state.owner
    })).then(function (data) {
      if (requestID !== state.boardRequest) {
        return;
      }
      state.stages = crmPipelineViewNormalizeStages(data);
      state.total = Number(data && data.total || 0);
      state.loaded = true;
      var selected = crmPipelineViewFindLead(state, state.selectedID);
      if (!selected) {
        state.selectedID = "";
        state.ownerDraft = "";
        state.notes = [];
        state.notesLeadID = "";
        state.handoff = null;
        state.handoffLeadID = "";
      } else {
        state.ownerDraft = selected.lead.owner || "";
      }
      return true;
    }).catch(function (err) {
      if (requestID === state.boardRequest) {
        state.error = err.message;
      }
      return false;
    }).finally(function () {
      if (requestID === state.boardRequest) {
        state.loading = false;
        api.requestRender();
      }
    });
  }

  function crmPipelineViewLoadOptions(state, api) {
    state.optionsLoading = true;
    api.requestRender();
    return Promise.all([
      api.api("/api/crm/candidates?limit=200"),
      api.api("/api/crm/jobs?limit=200")
    ]).then(function (responses) {
      state.candidates = responses[0] && Array.isArray(responses[0].candidates) ? responses[0].candidates : [];
      state.jobs = responses[1] && Array.isArray(responses[1].jobs) ? responses[1].jobs : [];
      state.optionsLoaded = true;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.optionsLoading = false;
      api.requestRender();
    });
  }

  function crmPipelineViewLoadNotes(state, api, leadID) {
    var requestID = state.notesRequest + 1;
    state.notesRequest = requestID;
    state.notesLoading = true;
    state.detailError = "";
    api.requestRender();
    return api.api("/api/crm/notes" + api.encodeQuery({
      target_type: "lead",
      target_id: leadID
    })).then(function (data) {
      if (requestID === state.notesRequest && state.selectedID === leadID) {
        state.notes = data && Array.isArray(data.notes) ? data.notes : [];
        state.notesLeadID = leadID;
      }
    }).catch(function (err) {
      if (requestID === state.notesRequest && state.selectedID === leadID) {
        state.detailError = err.message;
        state.notes = [];
        state.notesLeadID = leadID;
      }
    }).finally(function () {
      if (requestID === state.notesRequest) {
        state.notesLoading = false;
        api.requestRender();
      }
    });
  }

  function crmPipelineViewLoadHandoff(state, api, leadID) {
    var selected = crmPipelineViewFindLead(state, leadID);
    if (!selected || selected.lead.stage !== "hired") {
      state.handoff = null;
      state.handoffLeadID = leadID;
      return Promise.resolve(null);
    }
    state.handoffLoading = true;
    state.detailError = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases" + api.encodeQuery({
      lead_id: leadID,
      limit: 1
    })).then(function (data) {
      if (state.selectedID !== leadID) {
        return null;
      }
      var cases = data && Array.isArray(data.onboarding_cases) ? data.onboarding_cases : [];
      state.handoff = cases.length ? cases[0] : null;
      state.handoffLeadID = leadID;
      return state.handoff;
    }).catch(function (err) {
      if (state.selectedID === leadID) {
        state.detailError = err.message;
        state.handoff = null;
        state.handoffLeadID = leadID;
      }
      return null;
    }).finally(function () {
      if (state.selectedID === leadID) {
        state.handoffLoading = false;
        api.requestRender();
      }
    });
  }

  function crmPipelineViewCreateHandoff(state, api) {
    var selected = crmPipelineViewFindLead(state, state.selectedID);
    if (!selected || selected.lead.stage !== "hired") {
      return;
    }
    var leadID = selected.lead.id;
    state.handoffCreating = true;
    state.detailError = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/ops/onboarding_cases", {
      method: "POST",
      body: {
        lead_id: leadID,
        actor: "operator"
      }
    }).then(function (handoff) {
      if (state.selectedID === leadID) {
        state.handoff = handoff;
        state.handoffLeadID = leadID;
        state.success = "已创建入职交接单。";
      }
    }).catch(function (err) {
      if (crmPipelineViewIsStale(err)) {
        return crmPipelineViewLoadHandoff(state, api, leadID).then(function (handoff) {
          if (handoff) {
            state.success = "入职交接单已存在，已加载当前记录。";
          }
        });
      }
      if (state.selectedID === leadID) {
        state.detailError = err.message;
      }
      return null;
    }).finally(function () {
      state.handoffCreating = false;
      api.requestRender();
    });
  }

  function crmPipelineViewIsStale(err) {
    return /(^|\s)409(\s|$)|conflict|stale|version|stage conflict|版本|阶段冲突/i.test(String(err && err.message || ""));
  }

  function crmPipelineViewRecoverStale(state, api) {
    return crmPipelineViewLoadBoard(state, api).then(function (reloaded) {
      if (reloaded) {
        state.error = "数据已被其他操作更新，已重新加载最新看板。";
        api.requestRender();
      }
    });
  }

  function crmPipelineViewCreateLead(state, api, form) {
    state.createCandidateID = form.candidate_id.value;
    state.createJobID = form.job_id.value;
    state.createOwner = form.owner.value.trim();
    state.creating = true;
    state.error = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/crm/leads", {
      method: "POST",
      body: {
        candidate_id: state.createCandidateID,
        job_id: state.createJobID,
        owner: state.createOwner
      }
    }).then(function (lead) {
      state.selectedID = lead && lead.id || "";
      state.createCandidateID = "";
      state.createOwner = "";
      return crmPipelineViewLoadBoard(state, api);
    }).then(function (reloaded) {
      if (reloaded) {
        state.success = "已创建招聘流程记录。";
      }
      if (reloaded && state.selectedID) {
        state.notes = [];
        state.notesLeadID = state.selectedID;
      }
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.creating = false;
      api.requestRender();
    });
  }

  function crmPipelineViewSaveOwner(state, api, form) {
    var selected = crmPipelineViewFindLead(state, state.selectedID);
    if (!selected) {
      return;
    }
    state.ownerDraft = form.owner.value.trim();
    state.ownerSaving = true;
    state.error = "";
    state.success = "";
    api.requestRender();
    return api.api("/api/crm/leads/" + encodeURIComponent(selected.lead.id), {
      method: "PATCH",
      body: {
        expected_version: selected.lead.version,
        owner: state.ownerDraft
      }
    }).then(function () {
      return crmPipelineViewLoadBoard(state, api);
    }).then(function (reloaded) {
      if (reloaded) {
        state.success = "负责人已更新。";
      }
    }).catch(function (err) {
      if (crmPipelineViewIsStale(err)) {
        return crmPipelineViewRecoverStale(state, api);
      }
      state.error = err.message;
      return null;
    }).finally(function () {
      state.ownerSaving = false;
      api.requestRender();
    });
  }

  function crmPipelineViewTransition(state, api, toStage) {
    var selected = crmPipelineViewFindLead(state, state.selectedID);
    if (!selected) {
      return;
    }
    var fromStage = selected.lead.stage;
    var allowed = crmPipelineViewAllowedTransitions(selected);
    if (allowed.indexOf(toStage) < 0) {
      return;
    }
    var terminal = crmPipelineViewTerminalStage(toStage);
    if (terminal && !window.confirm("确认将该候选流程更新为“" + crmPipelineViewStageLabels[toStage] + "”？此操作不可撤销。")) {
      return;
    }
    state.transitioning = toStage;
    state.error = "";
    state.success = "";
    api.requestRender();
    var path = "/api/crm/leads/" + encodeURIComponent(selected.lead.id) + "/transition";
    var options = {
      method: "POST",
      body: {
        expected_version: selected.lead.version,
        from_stage: fromStage,
        to_stage: toStage,
        confirmed: terminal
      }
    };
    var transitionRequest = terminal ? api.automationAPI(path, options) :
      api.api("/api/crm/leads/" + encodeURIComponent(selected.lead.id) + "/transition", options);
    return transitionRequest.then(function () {
      return crmPipelineViewLoadBoard(state, api);
    }).then(function (reloaded) {
      if (reloaded) {
        state.success = toStage === "hired" ? "已录用并完成入职交接建单。" : "招聘阶段已更新。";
        if (toStage === "hired") {
          return crmPipelineViewLoadHandoff(state, api, state.selectedID);
        }
      }
      return null;
    }).catch(function (err) {
      if (crmPipelineViewIsStale(err)) {
        return crmPipelineViewRecoverStale(state, api);
      }
      state.error = err.message;
      return null;
    }).finally(function () {
      state.transitioning = "";
      api.requestRender();
    });
  }

  function crmPipelineViewAddNote(state, api, form) {
    var selected = crmPipelineViewFindLead(state, state.selectedID);
    if (!selected) {
      return;
    }
    var leadID = selected.lead.id;
    state.noteAuthor = form.author.value.trim();
    state.noteBody = form.body.value.trim();
    state.noteSaving = true;
    state.detailError = "";
    api.requestRender();
    return api.api("/api/crm/notes", {
      method: "POST",
      body: {
        target_type: "lead",
        target_id: leadID,
        author: state.noteAuthor,
        body: state.noteBody
      }
    }).then(function () {
      if (state.selectedID !== leadID) {
        return null;
      }
      state.noteBody = "";
      return crmPipelineViewLoadNotes(state, api, leadID);
    }).catch(function (err) {
      if (state.selectedID === leadID) {
        state.detailError = err.message;
      }
    }).finally(function () {
      state.noteSaving = false;
      api.requestRender();
    });
  }

  function crmPipelineViewRender(state, api) {
    var busyText = state.loading ? "正在加载招聘流程..." : state.optionsLoading ? "正在加载候选人与职位..." : "";
    return '<div class="crmPipelineView-root">' +
      '<section class="topbar">' +
      '<div><h2>招聘流程</h2><p>按固定阶段推进候选人，并在详情中维护负责人和跟进记录。</p></div>' +
      '<button class="primary crmPipelineView-refresh" type="button" data-crm-pipeline-view-action="refresh" ' + crmPipelineViewDisabled(state.loading) + '>刷新看板</button>' +
      "</section>" +
      '<section class="crmPipelineView-controls" aria-label="招聘流程筛选与创建">' +
      crmPipelineViewRenderFilters(state, api) +
      crmPipelineViewRenderCreate(state, api) +
      "</section>" +
      api.renderStatus(state.error, state.success, busyText) +
      '<section class="crmPipelineView-boardRegion" aria-label="七阶段招聘流程看板">' +
      '<div class="crmPipelineView-boardHeading"><div><h3>候选流程</h3><span>' + api.escapeHTML(state.total) + ' 条</span></div><span>横向查看七个阶段</span></div>' +
      '<div class="crmPipelineView-boardScroll" data-crm-pipeline-view-overflow="stage-grid" tabindex="0">' +
      '<div class="crmPipelineView-stageGrid">' +
      state.stages.map(function (stage) {
        return crmPipelineViewRenderStage(state, api, stage);
      }).join("") +
      "</div></div></section>" +
      crmPipelineViewRenderDetail(state, api) +
      "</div>";
  }

  function crmPipelineViewRenderFilters(state, api) {
    return '<form class="crmPipelineView-filterForm" data-crm-pipeline-view-form="filters">' +
      '<label>职位筛选<select name="job_id" ' + crmPipelineViewDisabled(state.loading || state.optionsLoading) + ">" +
      '<option value="">全部职位</option>' +
      state.jobs.map(function (item) {
        var job = item && item.job || {};
        var label = (job.title || "未命名职位") + (job.company ? " · " + job.company : "");
        return '<option value="' + api.escapeHTML(job.id || "") + '"' + (state.jobID === job.id ? " selected" : "") + ">" + api.escapeHTML(label) + "</option>";
      }).join("") +
      "</select></label>" +
      '<label>负责人筛选<input name="owner" value="' + api.escapeHTML(state.owner) + '" maxlength="80" placeholder="输入负责人"></label>' +
      '<div class="crmPipelineView-formActions"><button class="primary" type="submit" ' + crmPipelineViewDisabled(state.loading) + '>应用筛选</button>' +
      '<button type="button" data-crm-pipeline-view-action="reset-filters" ' + crmPipelineViewDisabled(state.loading) + ">清除</button></div>" +
      "</form>";
  }

  function crmPipelineViewRenderCreate(state, api) {
    return '<form class="crmPipelineView-createForm" data-crm-pipeline-view-form="create">' +
      '<label>候选人<select name="candidate_id" required ' + crmPipelineViewDisabled(state.creating || state.optionsLoading) + ">" +
      '<option value="">选择候选人</option>' +
      state.candidates.filter(function (item) {
        return item && item.candidate && item.candidate.status !== "archived";
      }).map(function (item) {
        var candidate = item.candidate;
        var label = candidate.full_name || candidate.id || "未命名候选人";
        return '<option value="' + api.escapeHTML(candidate.id || "") + '"' + (state.createCandidateID === candidate.id ? " selected" : "") + ">" + api.escapeHTML(label) + "</option>";
      }).join("") +
      "</select></label>" +
      '<label>开放职位<select name="job_id" required ' + crmPipelineViewDisabled(state.creating || state.optionsLoading) + ">" +
      '<option value="">选择开放职位</option>' +
      state.jobs.filter(function (item) {
        return item && item.job && item.job.status === "open";
      }).map(function (item) {
        var job = item.job;
        var label = (job.title || job.id || "未命名职位") + (job.company ? " · " + job.company : "");
        return '<option value="' + api.escapeHTML(job.id || "") + '"' + (state.createJobID === job.id ? " selected" : "") + ">" + api.escapeHTML(label) + "</option>";
      }).join("") +
      "</select></label>" +
      '<label>负责人<input name="owner" value="' + api.escapeHTML(state.createOwner) + '" maxlength="80" placeholder="可留空"></label>' +
      '<button class="primary" type="submit" ' + crmPipelineViewDisabled(state.creating || state.optionsLoading) + ">" + (state.creating ? "创建中..." : "创建流程") + "</button>" +
      "</form>";
  }

  function crmPipelineViewRenderStage(state, api, stage) {
    var items = stage.leads || [];
    return '<section class="crmPipelineView-stage crmPipelineView-stage-' + stage.stage + '" aria-labelledby="crmPipelineView-stage-' + stage.stage + '">' +
      '<header><h3 id="crmPipelineView-stage-' + stage.stage + '">' + api.escapeHTML(crmPipelineViewStageLabels[stage.stage]) + '</h3><span>' + api.escapeHTML(items.length) + "</span></header>" +
      '<div class="crmPipelineView-stageLeads">' +
      (items.length ? items.map(function (item) {
        return crmPipelineViewRenderLead(state, api, item);
      }).join("") : '<div class="crmPipelineView-emptyStage">暂无候选人</div>') +
      "</div></section>";
  }

  function crmPipelineViewRenderLead(state, api, item) {
    var lead = item && item.lead || {};
    var busy = state.loading || state.ownerSaving || !!state.transitioning || state.noteSaving;
    return '<button class="crmPipelineView-lead" type="button" data-crm-pipeline-view-lead="' + api.escapeHTML(lead.id || "") + '" aria-current="' + (state.selectedID === lead.id ? "true" : "false") + '" ' + crmPipelineViewDisabled(busy) + ">" +
      '<strong>' + api.escapeHTML(item.candidate_name || "未命名候选人") + "</strong>" +
      '<span class="crmPipelineView-jobTitle">' + api.escapeHTML(item.job_title || "未命名职位") + "</span>" +
      '<span class="crmPipelineView-leadMeta"><span>' + api.escapeHTML(lead.owner || "未分配") + "</span><span>" + api.escapeHTML(lead.id || "") + "</span></span>" +
      "</button>";
  }

  function crmPipelineViewRenderDetail(state, api) {
    var selected = crmPipelineViewFindLead(state, state.selectedID);
    if (!selected) {
      return '<section class="crmPipelineView-detailEmpty"><strong>流程详情</strong><span>从看板选择一条记录后可转派负责人、推进阶段和添加备注。</span></section>';
    }
    var lead = selected.lead;
    var transitions = crmPipelineViewAllowedTransitions(selected);
    return '<section class="crmPipelineView-detail" aria-label="招聘流程详情">' +
      '<div class="crmPipelineView-detailSummary">' +
      '<div class="crmPipelineView-detailHeading"><div><h3>' + api.escapeHTML(selected.candidate_name || "未命名候选人") + '</h3><p>' + api.escapeHTML(selected.job_title || "未命名职位") + '</p></div><span>' + api.escapeHTML(crmPipelineViewStageLabels[lead.stage] || lead.stage) + "</span></div>" +
      '<dl><dt>流程 ID</dt><dd>' + api.escapeHTML(lead.id) + '</dd><dt>候选人 ID</dt><dd>' + api.escapeHTML(lead.candidate_id) + '</dd><dt>职位 ID</dt><dd>' + api.escapeHTML(lead.job_id) + '</dd><dt>版本</dt><dd>' + api.escapeHTML(lead.version) + '</dd><dt>阶段更新时间</dt><dd>' + api.escapeHTML(lead.stage_changed_at || "无") + "</dd></dl>" +
      '<form class="crmPipelineView-ownerForm" data-crm-pipeline-view-form="owner"><label>负责人<input name="owner" maxlength="80" value="' + api.escapeHTML(state.ownerDraft) + '" placeholder="可留空" ' + crmPipelineViewDisabled(state.ownerSaving || !!state.transitioning) + '></label><button type="submit" ' + crmPipelineViewDisabled(state.ownerSaving || !!state.transitioning) + ">" + (state.ownerSaving ? "保存中..." : "保存负责人") + "</button></form>" +
      '<div class="crmPipelineView-transitionArea"><span>下一阶段</span><div class="crmPipelineView-transitionActions">' +
      (transitions.length ? transitions.map(function (stage) {
        return '<button type="button" data-crm-pipeline-view-transition="' + stage + '" data-terminal="' +
          (crmPipelineViewTerminalStage(stage) ? "true" : "false") + '" ' +
          crmPipelineViewDisabled(state.ownerSaving || !!state.transitioning) + ">" +
          (state.transitioning === stage ? "更新中..." : api.escapeHTML(crmPipelineViewStageLabels[stage])) + "</button>";
      }).join("") : '<span class="crmPipelineView-terminal">该流程已结束</span>') +
      "</div></div>" +
      crmPipelineViewRenderStageHistory(selected, api) +
      crmPipelineViewRenderHandoff(state, api, selected) +
      "</div>" +
      '<div class="crmPipelineView-notes">' +
      '<div class="crmPipelineView-notesHeading"><h3>跟进备注</h3><span>' + api.escapeHTML(state.notesLeadID === lead.id ? state.notes.length : 0) + " 条</span></div>" +
      api.renderStatus(state.detailError, "", state.notesLoading ? "正在加载备注..." : "") +
      crmPipelineViewRenderNotes(state, api, lead.id) +
      '<form class="crmPipelineView-noteForm" data-crm-pipeline-view-form="note"><label>记录人<input name="author" maxlength="80" value="' + api.escapeHTML(state.noteAuthor) + '" placeholder="可留空" ' + crmPipelineViewDisabled(state.noteSaving) + '></label><label>备注<textarea name="body" maxlength="4000" required placeholder="记录沟通结果或下一步" ' + crmPipelineViewDisabled(state.noteSaving) + ">" + api.escapeHTML(state.noteBody) + '</textarea></label><button class="primary" type="submit" ' + crmPipelineViewDisabled(state.noteSaving) + ">" + (state.noteSaving ? "添加中..." : "添加备注") + "</button></form>" +
      "</div></section>";
  }

  function crmPipelineViewRenderStageHistory(selected, api) {
    var events = selected && Array.isArray(selected.stage_events) ? selected.stage_events : [];
    return '<div class="crmPipelineView-history"><div><span>阶段历史</span><small>' +
      api.escapeHTML(events.length) + ' 条</small></div>' +
      (events.length ? '<ol>' + events.map(function (item) {
        return '<li><div><strong>' +
          api.escapeHTML(crmPipelineViewStageLabels[item.from_stage] || item.from_stage) +
          ' → ' + api.escapeHTML(crmPipelineViewStageLabels[item.to_stage] || item.to_stage) +
          '</strong><time>' + api.escapeHTML(item.created_at || "") + '</time></div><p>' +
          api.escapeHTML(item.reason || "无备注") + '</p><small>' +
          api.escapeHTML(item.actor || "system") + '</small></li>';
      }).join("") + '</ol>' : '<p class="crmPipelineView-historyEmpty">尚无阶段变更记录。</p>') +
      '</div>';
  }

  function crmPipelineViewRenderHandoff(state, api, selected) {
    if (!selected || selected.lead.stage !== "hired") {
      return "";
    }
    if (state.handoffLoading && state.handoffLeadID !== selected.lead.id) {
      return '<div class="crmPipelineView-handoff"><span>正在加载入职交接...</span></div>';
    }
    if (state.handoff && state.handoffLeadID === selected.lead.id) {
      return '<div class="crmPipelineView-handoff"><div><span>入职交接</span><strong>' +
        api.escapeHTML(state.handoff.status || "") + '</strong></div><p>' +
        api.escapeHTML(state.handoff.id || "") + ' · 车队 ' +
        api.escapeHTML(state.handoff.fleet && state.handoff.fleet.status || "not_required") +
        '</p><button type="button" data-crm-pipeline-view-action="open-handoff">打开入职与车队</button></div>';
    }
    return '<div class="crmPipelineView-handoff"><div><span>入职交接</span><strong>未建单</strong></div>' +
      '<p>可为历史已录用记录补建交接单。</p><button type="button" data-crm-pipeline-view-action="create-handoff" ' +
      crmPipelineViewDisabled(state.handoffCreating) + '>' +
      (state.handoffCreating ? "创建中..." : "创建入职交接") + '</button></div>';
  }

  function crmPipelineViewRenderNotes(state, api, leadID) {
    if (state.notesLoading && state.notesLeadID !== leadID) {
      return "";
    }
    if (state.notesLeadID !== leadID || !state.notes.length) {
      return '<div class="crmPipelineView-emptyNotes">暂无跟进备注。</div>';
    }
    return '<div class="crmPipelineView-noteList">' + state.notes.map(function (note) {
      return '<article class="crmPipelineView-note"><div><strong>' + api.escapeHTML(note.author || "未署名") + '</strong><time>' + api.escapeHTML(note.created_at || "") + '</time></div><p>' + api.escapeHTML(note.body || "") + "</p></article>";
    }).join("") + "</div>";
  }

  function crmPipelineViewDisabled(value) {
    return value ? "disabled" : "";
  }

  function crmPipelineViewOnEnter(state, api) {
    var work = [];
    if (!state.loaded && !state.loading) {
      work.push(crmPipelineViewLoadBoard(state, api));
    }
    if (!state.optionsLoaded && !state.optionsLoading) {
      work.push(crmPipelineViewLoadOptions(state, api));
    }
    return Promise.all(work);
  }

  function crmPipelineViewBind(root, state, api) {
    root.addEventListener("click", function (event) {
      var leadButton = event.target.closest("[data-crm-pipeline-view-lead]");
      if (leadButton && root.contains(leadButton)) {
        var leadID = leadButton.getAttribute("data-crm-pipeline-view-lead");
        var selected = crmPipelineViewFindLead(state, leadID);
        if (selected) {
          state.selectedID = leadID;
          state.ownerDraft = selected.lead.owner || "";
          state.notes = [];
          state.notesLeadID = "";
          state.handoff = null;
          state.handoffLeadID = "";
          api.requestRender();
          crmPipelineViewLoadNotes(state, api, leadID);
          crmPipelineViewLoadHandoff(state, api, leadID);
        }
        return;
      }

      var transitionButton = event.target.closest("[data-crm-pipeline-view-transition]");
      if (transitionButton && root.contains(transitionButton)) {
        crmPipelineViewTransition(state, api, transitionButton.getAttribute("data-crm-pipeline-view-transition"));
        return;
      }

      var actionButton = event.target.closest("[data-crm-pipeline-view-action]");
      if (!actionButton || !root.contains(actionButton)) {
        return;
      }
      var action = actionButton.getAttribute("data-crm-pipeline-view-action");
      if (action === "refresh") {
        crmPipelineViewLoadBoard(state, api);
        if (!state.optionsLoading) {
          crmPipelineViewLoadOptions(state, api);
        }
        if (state.selectedID) {
          crmPipelineViewLoadHandoff(state, api, state.selectedID);
        }
      } else if (action === "reset-filters") {
        state.jobID = "";
        state.owner = "";
        crmPipelineViewLoadBoard(state, api);
      } else if (action === "create-handoff") {
        crmPipelineViewCreateHandoff(state, api);
      } else if (action === "open-handoff") {
        api.navigate("onboarding-fleet");
      }
    });

    root.addEventListener("submit", function (event) {
      var form = event.target.closest("[data-crm-pipeline-view-form]");
      if (!form || !root.contains(form)) {
        return;
      }
      event.preventDefault();
      var kind = form.getAttribute("data-crm-pipeline-view-form");
      if (kind === "filters") {
        state.jobID = form.job_id.value;
        state.owner = form.owner.value.trim();
        crmPipelineViewLoadBoard(state, api);
      } else if (kind === "create") {
        crmPipelineViewCreateLead(state, api, form);
      } else if (kind === "owner") {
        crmPipelineViewSaveOwner(state, api, form);
      } else if (kind === "note") {
        crmPipelineViewAddNote(state, api, form);
      }
    });
  }

  crmPipelineViewAPI.registerView({
    id: "pipeline",
    label: "招聘流程",
    section: "招聘交付",
    order: 40,
    createState: crmPipelineViewCreateState,
    render: crmPipelineViewRender,
    onEnter: crmPipelineViewOnEnter,
    bind: crmPipelineViewBind
  });
})();
