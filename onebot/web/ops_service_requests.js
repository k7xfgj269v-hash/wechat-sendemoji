(function () {
  "use strict";

  var wb = window.OneBotWorkbench;
  if (!wb) {
    return;
  }

  var API_PATH = "/api/ops/service_requests";
  var QUEUE_PATH = "/api/ops/service_queues";
  var HUMAN_CATEGORIES = { safety: true, dispute: true, fund: true };

  function emptyForm() {
    return {
      candidateID: "",
      jobID: "",
      onboardingCaseID: "",
      category: "general",
      priority: "normal",
      description: "",
      owner: "",
      queueID: "",
      requiresHuman: false,
      dueAt: "",
      resolution: "",
      nextStatus: "triaged"
    };
  }

  function createState() {
    return {
      entered: false,
      items: [],
      total: 0,
      selectedID: "",
      selected: null,
      candidates: [],
      jobs: [],
      queues: [],
      loading: false,
      detailLoading: false,
      referencesLoading: false,
      saving: false,
      transitioning: false,
      queueSaving: false,
      claiming: false,
      error: "",
      success: "",
      filters: {
        q: "",
        status: "",
        category: "",
        priority: "",
        owner: "",
        requiresHuman: "",
        overdue: ""
      },
      form: emptyForm()
    };
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function selected(value, current) {
    return String(value) === String(current) ? " selected" : "";
  }

  function checked(value) {
    return value ? " checked" : "";
  }

  function localToUTC(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      throw new Error("到期时间无效");
    }
    return date.toISOString();
  }

  function utcToLocal(value) {
    var date = new Date(value);
    if (!value || Number.isNaN(date.getTime())) {
      return "";
    }
    function pad(number) {
      return String(number).padStart(2, "0");
    }
    return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate()) +
      "T" + pad(date.getHours()) + ":" + pad(date.getMinutes());
  }

  function formatDate(value) {
    var date = new Date(value);
    if (!value || Number.isNaN(date.getTime())) {
      return "未设置";
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

  function categoryLabel(value) {
    return ({
      general: "一般",
      transport: "交通",
      accommodation: "住宿",
      equipment: "设备",
      schedule: "排班",
      document: "材料",
      safety: "安全",
      dispute: "争议",
      fund: "资金",
      other: "其他"
    })[value] || value || "未分类";
  }

  function statusLabel(value) {
    return ({
      open: "待处理",
      triaged: "已分诊",
      assigned: "已分派",
      in_progress: "处理中",
      awaiting_requester: "等待反馈",
      resolved: "已解决",
      closed: "已归档",
      escalated: "已升级",
      cancelled: "已取消"
    })[value] || value || "未知";
  }

  function priorityLabel(value) {
    return ({ low: "低", normal: "普通", high: "高", urgent: "紧急" })[value] || value || "普通";
  }

  function candidateLabel(id, candidates) {
    var match = candidates.find(function (item) {
      return item && item.candidate && item.candidate.id === id;
    });
    return match && match.candidate ? match.candidate.full_name || id : id;
  }

  function jobLabel(id, jobs) {
    var match = jobs.find(function (item) {
      return item && item.job && item.job.id === id;
    });
    return match && match.job ? match.job.title || id : id;
  }

  function optionList(values, current, emptyLabel, project) {
    var options = ['<option value="">' + wb.escapeHTML(emptyLabel) + "</option>"];
    values.forEach(function (item) {
      var value = project(item);
      options.push('<option value="' + wb.escapeHTML(value.id) + '"' +
        selected(value.id, current) + ">" + wb.escapeHTML(value.label) + "</option>");
    });
    return options.join("");
  }

  function categoryOptions(current, includeEmpty) {
    var values = [
      ["general", "一般"], ["transport", "交通"], ["accommodation", "住宿"],
      ["equipment", "设备"], ["schedule", "排班"], ["document", "材料"],
      ["safety", "安全"], ["dispute", "争议"], ["fund", "资金"], ["other", "其他"]
    ];
    return (includeEmpty ? ['<option value="">全部分类</option>'] : []).concat(values.map(function (item) {
      return '<option value="' + item[0] + '"' + selected(item[0], current) + ">" + item[1] + "</option>";
    })).join("");
  }

  function statusOptions(current, includeEmpty) {
    var values = [
      ["open", "待处理"], ["triaged", "已分诊"], ["assigned", "已分派"],
      ["in_progress", "处理中"], ["awaiting_requester", "等待反馈"],
      ["resolved", "已解决"], ["closed", "已归档"],
      ["escalated", "已升级"], ["cancelled", "已取消"]
    ];
    return (includeEmpty ? ['<option value="">全部状态</option>'] : []).concat(values.map(function (item) {
      return '<option value="' + item[0] + '"' + selected(item[0], current) + ">" + item[1] + "</option>";
    })).join("");
  }

  function priorityOptions(current, includeEmpty) {
    var values = [["low", "低"], ["normal", "普通"], ["high", "高"], ["urgent", "紧急"]];
    return (includeEmpty ? ['<option value="">全部优先级</option>'] : []).concat(values.map(function (item) {
      return '<option value="' + item[0] + '"' + selected(item[0], current) + ">" + item[1] + "</option>";
    })).join("");
  }

  function transitionOptions(status, current) {
    var allowed = {
      open: ["triaged", "assigned", "in_progress", "escalated", "cancelled"],
      triaged: ["assigned", "in_progress", "escalated", "cancelled"],
      assigned: ["in_progress", "awaiting_requester", "resolved", "escalated", "cancelled"],
      in_progress: ["awaiting_requester", "resolved", "escalated", "cancelled"],
      awaiting_requester: ["in_progress", "resolved", "escalated", "cancelled"],
      escalated: ["triaged", "assigned", "in_progress", "awaiting_requester", "resolved", "cancelled"],
      resolved: ["closed", "in_progress", "escalated"]
    };
    return (allowed[status] || []).map(function (value) {
      return '<option value="' + value + '"' + selected(value, current) + ">" +
        wb.escapeHTML(statusLabel(value)) + "</option>";
    }).join("");
  }

  function defaultTransition(status) {
    return ({
      open: "triaged",
      triaged: "assigned",
      assigned: "in_progress",
      in_progress: "resolved",
      awaiting_requester: "in_progress",
      escalated: "assigned",
      resolved: "closed"
    })[status] || "";
  }

  function renderFilters(s) {
    var f = s.filters;
    return '<form class="opsServiceRequests-filters" data-service-form="filters">' +
      '<label>搜索<input name="q" value="' + wb.escapeHTML(f.q) + '" placeholder="描述、负责人或 ID"></label>' +
      '<label>状态<select name="status">' + statusOptions(f.status, true) + "</select></label>" +
      '<label>分类<select name="category">' + categoryOptions(f.category, true) + "</select></label>" +
      '<label>优先级<select name="priority">' + priorityOptions(f.priority, true) + "</select></label>" +
      '<label>负责人<input name="owner" value="' + wb.escapeHTML(f.owner) + '" placeholder="精确匹配"></label>' +
      '<label>人工处理<select name="requires_human"><option value="">全部</option>' +
      '<option value="true"' + selected("true", f.requiresHuman) + '>需要</option>' +
      '<option value="false"' + selected("false", f.requiresHuman) + '>不需要</option></select></label>' +
      '<label>时效<select name="overdue"><option value="">全部</option>' +
      '<option value="true"' + selected("true", f.overdue) + '>已逾期</option>' +
      '<option value="false"' + selected("false", f.overdue) + '>未逾期</option></select></label>' +
      '<div class="opsServiceRequests-filterActions"><button class="primary" type="submit">筛选</button>' +
      '<button type="button" data-service-action="clear-filters">清除</button></div></form>';
  }

  function renderList(s) {
    if (s.loading && !s.items.length) {
      return '<div class="notice">正在加载服务请求...</div>';
    }
    if (!s.items.length) {
      return '<div class="notice">当前筛选条件下没有服务请求。</div>';
    }
    return '<div class="opsServiceRequests-list" role="list">' + s.items.map(function (item) {
      var overdue = item.due_at && new Date(item.due_at).getTime() < Date.now() &&
        ["resolved", "closed", "cancelled"].indexOf(item.status) < 0;
      return '<button class="opsServiceRequests-row" type="button" role="listitem" data-service-id="' +
        wb.escapeHTML(item.id) + '" aria-current="' + (item.id === s.selectedID ? "true" : "false") + '">' +
        '<span class="opsServiceRequests-rowTop"><strong>' +
        wb.escapeHTML(categoryLabel(item.category) + " · " + priorityLabel(item.priority)) + "</strong>" +
        '<span class="opsServiceRequests-status opsServiceRequests-status--' + wb.escapeHTML(item.status) + '">' +
        wb.escapeHTML(statusLabel(item.status)) + "</span></span>" +
        '<span class="opsServiceRequests-description">' + wb.escapeHTML(item.description) + "</span>" +
        '<span class="opsServiceRequests-context">' +
        wb.escapeHTML(candidateLabel(item.candidate_id, s.candidates) || item.candidate_id) +
        (item.job_id ? " · " + wb.escapeHTML(jobLabel(item.job_id, s.jobs)) : "") + "</span>" +
        '<span class="opsServiceRequests-rowBottom"><span class="' + (overdue ? "is-overdue" : "") + '">' +
        wb.escapeHTML(item.due_at ? "到期 " + formatDate(item.due_at) : "无到期时间") + "</span><span>" +
        (item.requires_human ? '<b class="opsServiceRequests-human">需人工</b> · ' : "") +
        wb.escapeHTML(item.owner || "未分派") + "</span></span></button>";
    }).join("") + "</div>";
  }

  function renderEditor(s) {
    var item = s.selected || {};
    var editing = !!s.selectedID;
    var busy = s.saving || s.transitioning || s.detailLoading;
    var terminal = item.status === "closed" || item.status === "cancelled";
    return '<form class="opsServiceRequests-form" data-service-form="editor">' +
      '<div class="opsServiceRequests-formGrid"><label>候选人<select name="candidate_id" required' +
      disabled(busy || s.referencesLoading) + ">" + optionList(s.candidates, s.form.candidateID, "选择候选人", function (entry) {
        var candidate = entry.candidate || {};
        return { id: candidate.id || "", label: (candidate.full_name || "未命名") + " · " + (candidate.id || "") };
      }) + "</select></label>" +
      '<label>职位<select name="job_id"' + disabled(busy || s.referencesLoading) + ">" +
      optionList(s.jobs, s.form.jobID, "不关联职位", function (entry) {
        var job = entry.job || {};
        return { id: job.id || "", label: (job.title || "未命名") + " · " + (job.id || "") };
      }) + "</select></label>" +
      '<label>分类<select name="category"' + disabled(busy) + ">" + categoryOptions(s.form.category, false) + "</select></label>" +
      '<label>优先级<select name="priority"' + disabled(busy) + ">" + priorityOptions(s.form.priority, false) + "</select></label>" +
      '<label>负责人<input name="owner" maxlength="80" value="' + wb.escapeHTML(s.form.owner) +
      '" placeholder="姓名或团队账号"' + disabled(busy) + "></label>" +
      '<label>服务队列<select name="queue_id"' + disabled(busy || !editing) + '><option value="">未入队</option>' +
      s.queues.map(function (queue) {
        return '<option value="' + wb.escapeHTML(queue.id) + '"' + selected(queue.id, s.form.queueID) +
          (queue.enabled ? "" : " disabled") + ">" + wb.escapeHTML(queue.name + (queue.enabled ? "" : "（停用）")) +
          "</option>";
      }).join("") + "</select></label>" +
      '<label>到期时间<input name="due_at" type="datetime-local" value="' + wb.escapeHTML(s.form.dueAt) +
      '"' + disabled(busy) + "></label>" +
      '<label class="opsServiceRequests-wide">Onboarding Case ID<input name="onboarding_case_id" maxlength="80" value="' +
      wb.escapeHTML(s.form.onboardingCaseID) + '" placeholder="可选"' + disabled(busy) + "></label></div>" +
      '<label>问题描述<textarea name="description" required maxlength="4000"' + disabled(busy) + ">" +
      wb.escapeHTML(s.form.description) + "</textarea></label>" +
      '<label class="opsServiceRequests-check"><input name="requires_human" type="checkbox"' +
      checked(s.form.requiresHuman || HUMAN_CATEGORIES[s.form.category]) +
      disabled(busy || HUMAN_CATEGORIES[s.form.category]) + ">需要人工处理</label>" +
      (editing ? '<label>解决记录<textarea name="resolution" maxlength="4000"' + disabled(busy) + ">" +
        wb.escapeHTML(s.form.resolution) + "</textarea></label>" : "") +
      (editing ? '<div class="opsServiceRequests-meta"><span>' + wb.escapeHTML(statusLabel(item.status)) +
        " · 版本 " + wb.escapeHTML(item.version) + '</span><span class="opsServiceRequests-id">' +
        wb.escapeHTML(item.id) + "</span></div>" : "") +
      '<div class="opsServiceRequests-actions"><button class="primary" type="submit"' + disabled(busy) + ">" +
      (s.saving ? "保存中..." : editing ? "保存修改" : "创建请求") + "</button>" +
      '<button type="button" data-service-action="new"' + disabled(busy) + ">新建</button></div>" +
      (editing && !terminal ? '<div class="opsServiceRequests-transition"><label>流转到<select name="next_status"' +
        disabled(busy) + ">" + transitionOptions(item.status, s.form.nextStatus) + "</select></label>" +
        '<button type="button" data-service-action="transition"' + disabled(busy) + ">" +
        (s.transitioning ? "处理中..." : "执行流转") + "</button>" +
        (item.queue_id && !item.claimed_by ? '<button class="primary" type="button" data-service-action="claim"' +
          disabled(busy || s.claiming) + ">" + (s.claiming ? "领取中..." : "领取请求") + "</button>" : "") +
        "</div>" : "") +
      "</form>";
  }

  function render(s) {
    return '<section class="topbar opsServiceRequests-topbar"><div><h2>服务请求</h2>' +
      '<p>分诊、分派和闭环现场入职后的问题。</p></div>' +
      '<button type="button" data-service-action="reload"' + disabled(s.loading) + ">刷新</button></section>" +
      renderQueues(s) +
      '<section class="opsServiceRequests-layout" aria-label="服务请求工作区"><div class="opsServiceRequests-queue">' +
      '<div class="opsServiceRequests-sectionHeader"><h3>处理队列</h3><span>' +
      wb.escapeHTML(s.total + " 条") + "</span></div>" + renderFilters(s) +
      wb.renderStatus(s.error, s.success, s.loading ? "正在刷新队列..." : "") + renderList(s) + "</div>" +
      '<div class="opsServiceRequests-editor"><div class="opsServiceRequests-sectionHeader"><h3>' +
      (s.selectedID ? "请求详情" : "新建请求") + "</h3><span>" +
      wb.escapeHTML(s.detailLoading ? "加载中" : s.selectedID ? "已选择" : "待创建") +
      "</span></div>" + renderEditor(s) + "</div></section>";
  }

  function renderQueues(s) {
    return '<section class="opsServiceRequests-queueAdmin"><div class="opsServiceRequests-sectionHeader"><h3>服务队列</h3><span>' +
      wb.escapeHTML(s.queues.length + " 个") + '</span></div><form data-service-form="queue-create">' +
      '<label>名称<input name="name" maxlength="160" required placeholder="例如：现场支持"></label>' +
      '<label>负责人<input name="owner" maxlength="80" placeholder="可选"></label>' +
      '<label>SLA 秒<input name="sla_seconds" type="number" min="0" max="31536000" value="0"></label>' +
      '<label>分类<select name="category">' + categoryOptions("", true) + '</select></label>' +
      '<button type="submit"' + disabled(s.queueSaving) + '>创建队列</button></form><div class="opsServiceRequests-queueChips">' +
      (s.queues.length ? s.queues.map(function (queue) {
        return '<button type="button" data-service-queue-toggle="' + wb.escapeHTML(queue.id) + '" data-version="' +
          wb.escapeHTML(queue.version) + '" data-enabled="' + (queue.enabled ? "true" : "false") + '">' +
          wb.escapeHTML(queue.name) + '<span>' + wb.escapeHTML(queue.enabled ? "启用" : "停用") + '</span></button>';
      }).join("") : '<span>暂无服务队列</span>') + '</div></section>';
  }

  function filterQuery(s) {
    return {
      q: s.filters.q.trim(),
      status: s.filters.status,
      category: s.filters.category,
      priority: s.filters.priority,
      owner: s.filters.owner.trim(),
      requires_human: s.filters.requiresHuman,
      overdue: s.filters.overdue,
      limit: 200,
      offset: 0
    };
  }

  function loadList(s, preserveNotice) {
    s.loading = true;
    if (!preserveNotice) {
      s.error = "";
      s.success = "";
    }
    wb.requestRender();
    return wb.api(API_PATH + wb.encodeQuery(filterQuery(s))).then(function (data) {
      s.items = data && Array.isArray(data.service_requests) ? data.service_requests : [];
      s.total = Number(data && data.total || 0);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.loading = false;
      wb.requestRender();
    });
  }

  function loadReferences(s) {
    s.referencesLoading = true;
    wb.requestRender();
    return Promise.all([
      wb.api("/api/crm/candidates" + wb.encodeQuery({ limit: 200, offset: 0 })),
      wb.api("/api/crm/jobs" + wb.encodeQuery({ limit: 200, offset: 0 }))
    ]).then(function (responses) {
      s.candidates = responses[0] && Array.isArray(responses[0].candidates) ? responses[0].candidates : [];
      s.jobs = responses[1] && Array.isArray(responses[1].jobs) ? responses[1].jobs : [];
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.referencesLoading = false;
      wb.requestRender();
    });
  }

  function loadQueues(s) {
    return wb.api(QUEUE_PATH).then(function (data) {
      s.queues = data && Array.isArray(data.service_queues) ? data.service_queues : [];
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      wb.requestRender();
    });
  }

  function setForm(s, item) {
    s.form = {
      candidateID: item.candidate_id || "",
      jobID: item.job_id || "",
      onboardingCaseID: item.onboarding_case_id || "",
      category: item.category || "general",
      priority: item.priority || "normal",
      description: item.description || "",
      owner: item.owner || "",
      queueID: item.queue_id || "",
      requiresHuman: !!item.requires_human,
      dueAt: utcToLocal(item.due_at),
      resolution: item.resolution || "",
      nextStatus: defaultTransition(item.status)
    };
  }

  function loadDetail(s, id, preserveDraft) {
    var draft = preserveDraft ? Object.assign({}, s.form) : null;
    s.selectedID = id;
    s.detailLoading = true;
    wb.requestRender();
    return wb.api(API_PATH + "/" + encodeURIComponent(id)).then(function (item) {
      s.selected = item;
      setForm(s, item);
      if (draft) {
        s.form = draft;
        s.form.nextStatus = defaultTransition(item.status);
      }
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.detailLoading = false;
      wb.requestRender();
    });
  }

  function readFilters(s, form) {
    s.filters.q = form.elements.q.value;
    s.filters.status = form.elements.status.value;
    s.filters.category = form.elements.category.value;
    s.filters.priority = form.elements.priority.value;
    s.filters.owner = form.elements.owner.value;
    s.filters.requiresHuman = form.elements.requires_human.value;
    s.filters.overdue = form.elements.overdue.value;
  }

  function readEditor(s, form) {
    s.form.candidateID = form.elements.candidate_id.value;
    s.form.jobID = form.elements.job_id.value;
    s.form.onboardingCaseID = form.elements.onboarding_case_id.value;
    s.form.category = form.elements.category.value;
    s.form.priority = form.elements.priority.value;
    s.form.description = form.elements.description.value;
    s.form.owner = form.elements.owner.value;
    s.form.queueID = form.elements.queue_id ? form.elements.queue_id.value : s.form.queueID;
    s.form.dueAt = form.elements.due_at.value;
    s.form.requiresHuman = HUMAN_CATEGORIES[s.form.category] || form.elements.requires_human.checked;
    s.form.resolution = form.elements.resolution ? form.elements.resolution.value : "";
    s.form.nextStatus = form.elements.next_status ? form.elements.next_status.value : "";
  }

  function payload(s, editing) {
    if (!s.form.candidateID) {
      throw new Error("必须选择候选人");
    }
    if (!s.form.description.trim()) {
      throw new Error("问题描述不能为空");
    }
    var body = {
      candidate_id: s.form.candidateID,
      job_id: s.form.jobID,
      onboarding_case_id: s.form.onboardingCaseID.trim(),
      category: s.form.category,
      priority: s.form.priority,
      description: s.form.description.trim(),
      owner: s.form.owner.trim(),
      requires_human: !!s.form.requiresHuman
    };
    if (s.form.dueAt) {
      body.due_at = localToUTC(s.form.dueAt);
    } else if (editing && s.selected.due_at) {
      body.clear_due_at = true;
    }
    if (editing) {
      body.expected_version = s.selected.version;
      body.resolution = s.form.resolution.trim();
      if (s.form.queueID !== (s.selected.queue_id || "")) {
        if (s.form.queueID) {
          body.queue_id = s.form.queueID;
        } else {
          body.clear_queue = true;
        }
      }
    }
    return body;
  }

  function isConflict(err) {
    return /(^|\s)409(\s|$)|conflict|version|stale|版本|冲突/i.test(String(err && err.message || ""));
  }

  function save(s, form) {
    readEditor(s, form);
    var editing = !!s.selectedID;
    var body;
    try {
      body = payload(s, editing);
    } catch (err) {
      s.error = err.message;
      s.success = "";
      wb.requestRender();
      return;
    }
    s.saving = true;
    s.error = "";
    s.success = "";
    wb.requestRender();
    var requestPath = editing ? API_PATH + "/" + encodeURIComponent(s.selectedID) : API_PATH;
    var requestOptions = editing ?
      { method: "PATCH", body: body } :
      { method: "POST", body: body };
    wb.api(requestPath, requestOptions).then(function (item) {
      s.selectedID = item.id;
      s.selected = item;
      setForm(s, item);
      s.success = editing ? "服务请求已更新。" : "服务请求已创建。";
      return loadList(s, true);
    }).catch(function (err) {
      s.error = isConflict(err) ? "版本已变化，已刷新最新记录；当前草稿仍保留。" : err.message;
      if (isConflict(err) && s.selectedID) {
        return loadDetail(s, s.selectedID, true);
      }
      return null;
    }).finally(function () {
      s.saving = false;
      wb.requestRender();
    });
  }

  function transition(s, form) {
    readEditor(s, form);
    if (!s.form.nextStatus) {
      return;
    }
    if (s.form.nextStatus === "resolved" && !s.form.resolution.trim()) {
      s.error = "解决请求前必须填写解决记录";
      wb.requestRender();
      return;
    }
    s.transitioning = true;
    s.error = "";
    s.success = "";
    wb.requestRender();
    var protectedTransition = s.selected.requires_human ||
      s.form.nextStatus === "closed" ||
      s.form.nextStatus === "cancelled";
    if (protectedTransition && !window.confirm("此状态变更需要操作员确认，是否继续？")) {
      s.transitioning = false;
      wb.requestRender();
      return;
    }
    var request = protectedTransition ? wb.automationAPI : wb.api;
    request(API_PATH + "/" + encodeURIComponent(s.selectedID) + "/transition", {
      method: "POST",
      body: {
        expected_version: s.selected.version,
        from_status: s.selected.status,
        to_status: s.form.nextStatus,
        resolution: s.form.resolution.trim()
      }
    }).then(function (item) {
      s.selected = item;
      setForm(s, item);
      s.success = "状态已更新为“" + statusLabel(item.status) + "”。";
      return loadList(s, true);
    }).catch(function (err) {
      s.error = isConflict(err) ? "状态已被其他操作更新，已刷新详情并保留草稿。" : err.message;
      if (isConflict(err)) {
        return loadDetail(s, s.selectedID, true);
      }
      return null;
    }).finally(function () {
      s.transitioning = false;
      wb.requestRender();
    });
  }

  function createQueue(s, form) {
    var category = form.elements.category.value;
    s.queueSaving = true;
    s.error = "";
    wb.requestRender();
    return wb.api(QUEUE_PATH, {
      method: "POST",
      body: {
        name: form.elements.name.value.trim(),
        owner: form.elements.owner.value.trim(),
        categories: category ? [category] : [],
        sla_seconds: Number(form.elements.sla_seconds.value) || 0,
        enabled: true
      }
    }).then(function () {
      form.reset();
      s.success = "服务队列已创建。";
      return loadQueues(s);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.queueSaving = false;
      wb.requestRender();
    });
  }

  function toggleQueue(s, button) {
    var enabled = button.getAttribute("data-enabled") === "true";
    s.queueSaving = true;
    s.error = "";
    wb.requestRender();
    return wb.api(QUEUE_PATH + "/" + encodeURIComponent(button.getAttribute("data-service-queue-toggle")), {
      method: "PATCH",
      body: {
        expected_version: Number(button.getAttribute("data-version")),
        enabled: !enabled
      }
    }).then(function () {
      s.success = enabled ? "服务队列已停用。" : "服务队列已启用。";
      return loadQueues(s);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.queueSaving = false;
      wb.requestRender();
    });
  }

  function claimRequest(s) {
    if (!s.selected) {
      return;
    }
    s.claiming = true;
    s.error = "";
    wb.requestRender();
    return wb.automationAPI(API_PATH + "/" + encodeURIComponent(s.selected.id) + "/claim", {
      method: "POST",
      body: { expected_version: s.selected.version }
    }).then(function (item) {
      s.selected = item;
      setForm(s, item);
      s.success = "请求已由当前认证操作员领取。";
      return loadList(s, true);
    }).catch(function (err) {
      s.error = isConflict(err) ? "请求已被其他操作员领取，已刷新详情。" : err.message;
      if (isConflict(err)) {
        return loadDetail(s, s.selectedID, false);
      }
      return null;
    }).finally(function () {
      s.claiming = false;
      wb.requestRender();
    });
  }

  function newRequest(s) {
    s.selectedID = "";
    s.selected = null;
    s.form = emptyForm();
    s.error = "";
    s.success = "";
    wb.requestRender();
  }

  function clearFilters(s) {
    s.filters = {
      q: "", status: "", category: "", priority: "", owner: "",
      requiresHuman: "", overdue: ""
    };
    loadList(s);
  }

  function bind(root, s) {
    Array.prototype.forEach.call(root.querySelectorAll("[data-service-id]"), function (button) {
      button.addEventListener("click", function () {
        loadDetail(s, button.getAttribute("data-service-id"));
      });
    });

    var filters = root.querySelector('[data-service-form="filters"]');
    if (filters) {
      filters.addEventListener("submit", function (event) {
        event.preventDefault();
        readFilters(s, filters);
        loadList(s);
      });
    }

    var editor = root.querySelector('[data-service-form="editor"]');
    if (editor) {
      editor.addEventListener("submit", function (event) {
        event.preventDefault();
        save(s, editor);
      });
      editor.elements.category.addEventListener("change", function () {
        readEditor(s, editor);
        if (HUMAN_CATEGORIES[s.form.category]) {
          s.form.requiresHuman = true;
        }
        wb.requestRender();
      });
    }

    var queueCreate = root.querySelector('[data-service-form="queue-create"]');
    if (queueCreate) {
      queueCreate.addEventListener("submit", function (event) {
        event.preventDefault();
        createQueue(s, queueCreate);
      });
    }

    Array.prototype.forEach.call(root.querySelectorAll("[data-service-queue-toggle]"), function (button) {
      button.addEventListener("click", function () {
        toggleQueue(s, button);
      });
    });

    Array.prototype.forEach.call(root.querySelectorAll("[data-service-action]"), function (button) {
      button.addEventListener("click", function () {
        var action = button.getAttribute("data-service-action");
        if (action === "reload") {
          loadList(s);
          loadReferences(s);
        } else if (action === "clear-filters") {
          clearFilters(s);
        } else if (action === "new") {
          newRequest(s);
        } else if (action === "transition" && editor) {
          transition(s, editor);
        } else if (action === "claim") {
          claimRequest(s);
        }
      });
    });
  }

  function onEnter(s) {
    if (s.entered) {
      return null;
    }
    s.entered = true;
    return Promise.all([loadList(s), loadReferences(s), loadQueues(s)]);
  }

  wb.registerView({
    id: "service-requests",
    label: "服务请求",
    section: "驻场履约",
    order: 70,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  });
})();
