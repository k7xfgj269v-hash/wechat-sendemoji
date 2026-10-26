(function () {
  "use strict";

  var wb = window.OneBotWorkbench;
  if (!wb) {
    return;
  }

  function crmTasksEmptyForm() {
    return {
      title: "",
      description: "",
      owner: "",
      dueAt: "",
      targetType: "candidate",
      targetID: ""
    };
  }

  function crmTasksCreateState() {
    return {
      entered: false,
      items: [],
      total: 0,
      selectedID: "",
      selectedItem: null,
      candidates: [],
      leads: [],
      loading: false,
      detailLoading: false,
      targetsLoading: false,
      saving: false,
      actioning: false,
      error: "",
      success: "",
      filters: {
        status: "",
        owner: "",
        dueFrom: "",
        dueTo: "",
        overdue: ""
      },
      form: crmTasksEmptyForm()
    };
  }

  function crmTasksDisabled(value) {
    return value ? " disabled" : "";
  }

  function crmTasksSelected(value, current) {
    return String(value) === String(current) ? " selected" : "";
  }

  function crmTasksLocalToUTC(value) {
    var text = String(value || "").trim();
    var match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(text);
    if (!match) {
      throw new Error("日期时间格式无效");
    }
    var parts = match.slice(1).map(Number);
    var date = new Date(parts[0], parts[1] - 1, parts[2], parts[3], parts[4], parts[5] || 0, 0);
    if (
      Number.isNaN(date.getTime()) ||
      date.getFullYear() !== parts[0] ||
      date.getMonth() !== parts[1] - 1 ||
      date.getDate() !== parts[2] ||
      date.getHours() !== parts[3] ||
      date.getMinutes() !== parts[4] ||
      date.getSeconds() !== (parts[5] || 0)
    ) {
      throw new Error("日期时间无效");
    }
    return date.toISOString();
  }

  function crmTasksUTCToLocal(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return "";
    }
    function pad(number) {
      return String(number).padStart(2, "0");
    }
    return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate()) +
      "T" + pad(date.getHours()) + ":" + pad(date.getMinutes());
  }

  function crmTasksFormatDate(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return value || "未设置";
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

  function crmTasksAsItem(data) {
    if (!data) {
      return null;
    }
    return data.task ? data : { task: data };
  }

  function crmTasksTask(item) {
    return item && item.task ? item.task : {};
  }

  function crmTasksStatusLabel(status) {
    return status === "completed" ? "已完成" : "待跟进";
  }

  function crmTasksStageLabel(stage) {
    var labels = {
      sourced: "已寻源",
      screening: "筛选",
      interview: "面试",
      offer: "Offer",
      hired: "已录用",
      rejected: "已拒绝",
      withdrawn: "已退出"
    };
    return labels[stage] || stage || "";
  }

  function crmTasksTargetOptions(s) {
    var options = ['<option value="">选择关联对象</option>'];
    if (s.form.targetType === "lead") {
      s.leads.forEach(function (item) {
        var lead = item.lead || {};
        var label = (item.candidate_name || "未命名候选人") + " / " +
          (item.job_title || "未命名职位") + " / " + crmTasksStageLabel(lead.stage) +
          " (" + (lead.id || "") + ")";
        options.push('<option value="' + wb.escapeHTML(lead.id || "") + '"' +
          crmTasksSelected(lead.id, s.form.targetID) + ">" + wb.escapeHTML(label) + "</option>");
      });
    } else {
      s.candidates.forEach(function (item) {
        var candidate = item.candidate || {};
        var label = (candidate.full_name || "未命名候选人") + " (" + (candidate.id || "") + ")";
        options.push('<option value="' + wb.escapeHTML(candidate.id || "") + '"' +
          crmTasksSelected(candidate.id, s.form.targetID) + ">" + wb.escapeHTML(label) + "</option>");
      });
    }
    return options.join("");
  }

  function crmTasksRenderFilters(s) {
    var f = s.filters;
    return '<form class="crmTasks-filters" data-crm-tasks-form="filters">' +
      '<label>状态<select name="status"' + crmTasksDisabled(s.loading) + ">" +
      '<option value=""' + crmTasksSelected("", f.status) + ">全部</option>" +
      '<option value="open"' + crmTasksSelected("open", f.status) + ">待跟进</option>" +
      '<option value="completed"' + crmTasksSelected("completed", f.status) + ">已完成</option>" +
      "</select></label>" +
      '<label>负责人<input name="owner" value="' + wb.escapeHTML(f.owner) + '" placeholder="按负责人精确筛选"' + crmTasksDisabled(s.loading) + "></label>" +
      '<label>到期起点<input name="due_from" type="datetime-local" value="' + wb.escapeHTML(f.dueFrom) + '"' + crmTasksDisabled(s.loading) + "></label>" +
      '<label>到期终点<input name="due_to" type="datetime-local" value="' + wb.escapeHTML(f.dueTo) + '"' + crmTasksDisabled(s.loading) + "></label>" +
      '<label>逾期<select name="overdue"' + crmTasksDisabled(s.loading) + ">" +
      '<option value=""' + crmTasksSelected("", f.overdue) + ">全部</option>" +
      '<option value="true"' + crmTasksSelected("true", f.overdue) + ">仅逾期</option>" +
      '<option value="false"' + crmTasksSelected("false", f.overdue) + ">未逾期</option>" +
      "</select></label>" +
      '<div class="crmTasks-filterActions"><button class="primary" type="submit"' + crmTasksDisabled(s.loading) + ">筛选</button>" +
      '<button type="button" data-crm-tasks-action="clear-filters"' + crmTasksDisabled(s.loading) + ">清除</button></div>" +
      "</form>";
  }

  function crmTasksContext(item) {
    var task = crmTasksTask(item);
    var parts = [];
    if (item.candidate_name) {
      parts.push("候选人：" + item.candidate_name);
    } else if (task.candidate_id) {
      parts.push("候选人 ID：" + task.candidate_id);
    }
    if (task.lead_id) {
      parts.push("Lead：" + task.lead_id);
    }
    if (item.lead_stage) {
      parts.push("阶段：" + crmTasksStageLabel(item.lead_stage));
    }
    if (item.job_title || item.job_id) {
      parts.push("职位：" + (item.job_title || item.job_id));
    }
    return parts.length ? parts.join(" · ") : "未关联上下文";
  }

  function crmTasksRenderList(s) {
    if (s.loading && !s.items.length) {
      return '<div class="notice">正在加载跟进任务...</div>';
    }
    if (!s.items.length) {
      return '<div class="notice">当前筛选条件下没有跟进任务。</div>';
    }
    return '<div class="crmTasks-list" role="list">' + s.items.map(function (item) {
      var task = crmTasksTask(item);
      var overdue = task.status === "open" && new Date(task.due_at).getTime() < Date.now();
      return '<button class="crmTasks-row" type="button" role="listitem" data-crm-tasks-id="' +
        wb.escapeHTML(task.id || "") + '" aria-current="' + (s.selectedID === task.id ? "true" : "false") + '">' +
        '<span class="crmTasks-rowTop"><strong>' + wb.escapeHTML(task.title || "未命名任务") + "</strong>" +
        '<span class="crmTasks-status crmTasks-status--' + wb.escapeHTML(task.status || "open") + '">' +
        wb.escapeHTML(crmTasksStatusLabel(task.status)) + "</span></span>" +
        '<span class="crmTasks-due' + (overdue ? " crmTasks-due--overdue" : "") + '">到期：' +
        wb.escapeHTML(crmTasksFormatDate(task.due_at)) + (overdue ? " · 已逾期" : "") + "</span>" +
        '<span class="crmTasks-context">' + wb.escapeHTML(crmTasksContext(item)) + "</span>" +
        '<span class="crmTasks-rowBottom"><span>负责人：' + wb.escapeHTML(task.owner || "未分配") + "</span>" +
        '<span class="crmTasks-id">' + wb.escapeHTML(task.id || "") + "</span></span></button>";
    }).join("") + "</div>";
  }

  function crmTasksRenderEditor(s) {
    var task = crmTasksTask(s.selectedItem);
    var editing = !!s.selectedID;
    var busy = s.saving || s.actioning || s.detailLoading;
    if (s.detailLoading && !s.selectedItem) {
      return '<div class="notice">正在加载任务详情...</div>';
    }
    return '<form class="crmTasks-form" data-crm-tasks-form="task">' +
      '<label>任务标题<input name="title" required maxlength="200" value="' + wb.escapeHTML(s.form.title) +
      '" placeholder="例如：确认二面时间"' + crmTasksDisabled(busy) + "></label>" +
      '<label>说明<textarea name="description" maxlength="4000" placeholder="记录下一步动作和必要背景"' +
      crmTasksDisabled(busy) + ">" + wb.escapeHTML(s.form.description) + "</textarea></label>" +
      '<div class="crmTasks-formRow"><label>负责人<input name="owner" maxlength="80" value="' +
      wb.escapeHTML(s.form.owner) + '" placeholder="姓名或团队账号"' + crmTasksDisabled(busy) + "></label>" +
      '<label>到期时间<input name="due_at" type="datetime-local" required value="' +
      wb.escapeHTML(s.form.dueAt) + '"' + crmTasksDisabled(busy) + "></label></div>" +
      '<div class="crmTasks-formRow"><label>关联类型<select name="target_type"' + crmTasksDisabled(busy || s.targetsLoading) + ">" +
      '<option value="candidate"' + crmTasksSelected("candidate", s.form.targetType) + '>候选人</option>' +
      '<option value="lead"' + crmTasksSelected("lead", s.form.targetType) + '>Lead</option></select></label>' +
      '<label>关联对象<select name="target_id" required' + crmTasksDisabled(busy || s.targetsLoading) + ">" +
      crmTasksTargetOptions(s) + "</select></label></div>" +
      (editing ? '<div class="crmTasks-current"><span>状态：' + wb.escapeHTML(crmTasksStatusLabel(task.status)) +
        '</span><span class="crmTasks-id">版本 ' + wb.escapeHTML(task.version || "") + " · " +
        wb.escapeHTML(task.id || "") + "</span></div>" : "") +
      '<div class="crmTasks-formActions"><button class="primary" type="submit" data-crm-tasks-submit="save"' +
      crmTasksDisabled(busy) + ">" + (s.saving ? "保存中..." : editing ? "保存修改" : "创建任务") + "</button>" +
      (editing ? '<button type="submit" data-crm-tasks-submit="reassign"' + crmTasksDisabled(busy) + ">转派</button>" : "") +
      (editing ? '<button type="button" data-crm-tasks-action="toggle-status"' + crmTasksDisabled(busy) + ">" +
        (s.actioning ? "处理中..." : task.status === "completed" ? "重开" : "完成") + "</button>" : "") +
      '<button type="button" data-crm-tasks-action="new-task"' + crmTasksDisabled(busy) + ">新建</button></div>" +
      "</form>";
  }

  function crmTasksRender(s) {
    return '<section class="topbar crmTasks-topbar"><div><h2>跟进任务</h2><p>集中处理候选人与招聘流程的下一步动作。</p></div>' +
      '<button type="button" data-crm-tasks-action="reload"' + crmTasksDisabled(s.loading || s.targetsLoading) + ">刷新</button></section>" +
      '<section class="crmTasks-layout" aria-label="跟进任务工作区">' +
      '<div class="crmTasks-queue"><div class="crmTasks-sectionHeader"><h3>任务队列</h3><span>' +
      wb.escapeHTML(s.total + " 条") + "</span></div>" +
      crmTasksRenderFilters(s) +
      wb.renderStatus(s.error, s.success, s.loading ? "正在加载任务..." : "") +
      crmTasksRenderList(s) + "</div>" +
      '<div class="crmTasks-editor"><div class="crmTasks-sectionHeader"><h3>' +
      (s.selectedID ? "编辑任务" : "新建任务") + '</h3><span>' +
      wb.escapeHTML(s.targetsLoading ? "加载关联对象..." : s.selectedID ? "已选择" : "待创建") +
      "</span></div>" + crmTasksRenderEditor(s) + "</div></section>";
  }

  function crmTasksFilterQuery(s) {
    var f = s.filters;
    var dueFrom = f.dueFrom ? crmTasksLocalToUTC(f.dueFrom) : "";
    var dueTo = f.dueTo ? crmTasksLocalToUTC(f.dueTo) : "";
    if (dueFrom && dueTo && new Date(dueFrom).getTime() > new Date(dueTo).getTime()) {
      throw new Error("到期起点不能晚于到期终点");
    }
    return {
      owner: f.owner.trim(),
      status: f.status,
      overdue: f.overdue,
      due_from: dueFrom,
      due_to: dueTo,
      candidate_id: "",
      lead_id: "",
      limit: 200,
      offset: 0
    };
  }

  function crmTasksLoadTasks(s, preserveNotice) {
    var query;
    try {
      query = crmTasksFilterQuery(s);
    } catch (err) {
      s.error = err.message;
      s.success = "";
      wb.requestRender();
      return Promise.resolve();
    }
    s.loading = true;
    if (!preserveNotice) {
      s.error = "";
      s.success = "";
    }
    wb.requestRender();
    return wb.api("/api/crm/tasks" + wb.encodeQuery(query)).then(function (data) {
      s.items = data && Array.isArray(data.tasks) ? data.tasks : [];
      s.total = Number(data && data.total || 0);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.loading = false;
      wb.requestRender();
    });
  }

  function crmTasksLoadTargets(s) {
    s.targetsLoading = true;
    wb.requestRender();
    return Promise.all([
      wb.api("/api/crm/candidates" + wb.encodeQuery({ limit: 200, offset: 0 })),
      wb.api("/api/crm/leads" + wb.encodeQuery({ limit: 200, offset: 0 }))
    ]).then(function (responses) {
      s.candidates = responses[0] && Array.isArray(responses[0].candidates) ? responses[0].candidates : [];
      s.leads = responses[1] && Array.isArray(responses[1].leads) ? responses[1].leads : [];
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.targetsLoading = false;
      wb.requestRender();
    });
  }

  function crmTasksSetFormFromItem(s, item) {
    var task = crmTasksTask(item);
    s.form = {
      title: task.title || "",
      description: task.description || "",
      owner: task.owner || "",
      dueAt: crmTasksUTCToLocal(task.due_at),
      targetType: task.lead_id ? "lead" : "candidate",
      targetID: task.lead_id || task.candidate_id || ""
    };
  }

  function crmTasksLoadDetail(s, id) {
    s.selectedID = id;
    s.detailLoading = true;
    s.error = "";
    wb.requestRender();
    return wb.api("/api/crm/tasks/" + encodeURIComponent(id)).then(function (data) {
      var item = crmTasksAsItem(data);
      s.selectedItem = item;
      crmTasksSetFormFromItem(s, item);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.detailLoading = false;
      wb.requestRender();
    });
  }

  function crmTasksReadFilters(s, form) {
    s.filters.status = form.elements.status.value;
    s.filters.owner = form.elements.owner.value;
    s.filters.dueFrom = form.elements.due_from.value;
    s.filters.dueTo = form.elements.due_to.value;
    s.filters.overdue = form.elements.overdue.value;
  }

  function crmTasksReadForm(s, form) {
    s.form.title = form.elements.title.value;
    s.form.description = form.elements.description.value;
    s.form.owner = form.elements.owner.value;
    s.form.dueAt = form.elements.due_at.value;
    s.form.targetType = form.elements.target_type.value;
    s.form.targetID = form.elements.target_id.value;
  }

  function crmTasksPayload(s) {
    var title = s.form.title.trim();
    if (!title) {
      throw new Error("任务标题不能为空");
    }
    if (!s.form.targetID) {
      throw new Error("必须选择候选人或 Lead");
    }
    var payload = {
      title: title,
      description: s.form.description.trim(),
      owner: s.form.owner.trim(),
      due_at: crmTasksLocalToUTC(s.form.dueAt),
      candidate_id: "",
      lead_id: ""
    };
    if (s.form.targetType === "lead") {
      payload.lead_id = s.form.targetID;
    } else {
      payload.candidate_id = s.form.targetID;
    }
    return payload;
  }

  function crmTasksSave(s, form, submitter) {
    crmTasksReadForm(s, form);
    var payload;
    try {
      payload = crmTasksPayload(s);
    } catch (err) {
      s.error = err.message;
      s.success = "";
      wb.requestRender();
      return;
    }
    var editing = !!s.selectedID;
    var transfer = submitter && submitter.getAttribute("data-crm-tasks-submit") === "reassign";
    if (editing) {
      payload.expected_version = crmTasksTask(s.selectedItem).version;
    }
    s.saving = true;
    s.error = "";
    s.success = "";
    wb.requestRender();
    var requestPath = editing ? "/api/crm/tasks/" + encodeURIComponent(s.selectedID) : "/api/crm/tasks";
    var requestOptions = editing ?
      { method: "PATCH", body: payload } :
      { method: "POST", body: payload };
    wb.api(requestPath, requestOptions).then(function (data) {
      var item = crmTasksAsItem(data);
      s.selectedID = crmTasksTask(item).id || s.selectedID;
      s.selectedItem = item;
      crmTasksSetFormFromItem(s, item);
      s.success = transfer ? "任务已转派。" : editing ? "任务已更新。" : "任务已创建。";
      return crmTasksLoadTasks(s, true);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.saving = false;
      wb.requestRender();
    });
  }

  function crmTasksToggleStatus(s) {
    var task = crmTasksTask(s.selectedItem);
    if (!task.id || s.actioning) {
      return;
    }
    var nextStatus = task.status === "completed" ? "open" : "completed";
    s.actioning = true;
    s.error = "";
    s.success = "";
    wb.requestRender();
    wb.api("/api/crm/tasks/" + encodeURIComponent(task.id) + "/status", {
      method: "POST",
      body: {
        expected_version: task.version,
        status: nextStatus
      }
    }).then(function (data) {
      var item = crmTasksAsItem(data);
      s.selectedItem = item;
      crmTasksSetFormFromItem(s, item);
      s.success = nextStatus === "completed" ? "任务已完成。" : "任务已重开。";
      return crmTasksLoadTasks(s, true);
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.actioning = false;
      wb.requestRender();
    });
  }

  function crmTasksNew(s) {
    s.selectedID = "";
    s.selectedItem = null;
    s.form = crmTasksEmptyForm();
    s.error = "";
    s.success = "";
    wb.requestRender();
  }

  function crmTasksClearFilters(s) {
    s.filters = {
      status: "",
      owner: "",
      dueFrom: "",
      dueTo: "",
      overdue: ""
    };
    crmTasksLoadTasks(s);
  }

  function crmTasksBind(root, s) {
    Array.prototype.forEach.call(root.querySelectorAll("[data-crm-tasks-id]"), function (button) {
      button.addEventListener("click", function () {
        crmTasksLoadDetail(s, button.getAttribute("data-crm-tasks-id"));
      });
    });

    var filterForm = root.querySelector('[data-crm-tasks-form="filters"]');
    if (filterForm) {
      filterForm.addEventListener("submit", function (event) {
        event.preventDefault();
        crmTasksReadFilters(s, filterForm);
        crmTasksLoadTasks(s);
      });
    }

    var taskForm = root.querySelector('[data-crm-tasks-form="task"]');
    if (taskForm) {
      taskForm.addEventListener("submit", function (event) {
        event.preventDefault();
        crmTasksSave(s, taskForm, event.submitter);
      });
      taskForm.elements.target_type.addEventListener("change", function () {
        s.form.targetType = taskForm.elements.target_type.value;
        s.form.targetID = "";
        wb.requestRender();
      });
    }

    Array.prototype.forEach.call(root.querySelectorAll("[data-crm-tasks-action]"), function (button) {
      button.addEventListener("click", function () {
        var action = button.getAttribute("data-crm-tasks-action");
        if (action === "reload") {
          crmTasksLoadTasks(s);
          crmTasksLoadTargets(s);
        } else if (action === "clear-filters") {
          crmTasksClearFilters(s);
        } else if (action === "new-task") {
          crmTasksNew(s);
        } else if (action === "toggle-status") {
          crmTasksToggleStatus(s);
        }
      });
    });
  }

  function crmTasksOnEnter(s) {
    if (s.entered) {
      return null;
    }
    s.entered = true;
    return Promise.all([crmTasksLoadTasks(s), crmTasksLoadTargets(s)]);
  }

  wb.registerView({
    id: "tasks",
    label: "跟进任务",
    section: "招聘交付",
    order: 50,
    createState: crmTasksCreateState,
    render: crmTasksRender,
    onEnter: crmTasksOnEnter,
    bind: crmTasksBind
  });
})();
