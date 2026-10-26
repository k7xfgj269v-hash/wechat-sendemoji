(function () {
  "use strict";

  var crmJobsWorkbench = window.OneBotWorkbench;
  var crmJobsPageSize = 50;

  function crmJobsEmptyForm() {
    return {
      title: "",
      company: "",
      location: "",
      employment_type: "",
      description: "",
      requirements: "",
      demand_code: "",
      site_name: "",
      onsite_contact: "",
      headcount: "0",
      priority: "normal",
      start_at: "",
      deadline_at: "",
      shift: "",
      work_schedule: "",
      compensation_summary: "",
      accommodation: "",
      transport_requirements: "",
      required_documents: "",
      owner: "",
      status: "open"
    };
  }

  function crmJobsCreateState() {
    return {
      q: "",
      status: "",
      priority: "",
      site_name: "",
      owner: "",
      limit: crmJobsPageSize,
      offset: 0,
      total: 0,
      jobs: [],
      selectedID: "",
      pendingSelection: "",
      detail: null,
      revisions: [],
      activeRevisionID: "",
      revisionDetail: null,
      comparison: null,
      compareFrom: "",
      compareTo: "",
      mode: "create",
      form: crmJobsEmptyForm(),
      dirty: false,
      loading: false,
      detailLoading: false,
      revisionsLoading: false,
      revisionLoading: false,
      comparisonLoading: false,
      activatingRevision: "",
      saving: false,
      statusSaving: "",
      loaded: false,
      listSequence: 0,
      detailSequence: 0,
      error: "",
      revisionError: "",
      success: ""
    };
  }

  function crmJobsEscape(value) {
    return crmJobsWorkbench.escapeHTML(value);
  }

  function crmJobsDisabled(value) {
    return value ? "disabled" : "";
  }

  function crmJobsStatusLabel(status) {
    if (status === "open") {
      return "开放";
    }
    if (status === "closed") {
      return "已关闭";
    }
    if (status === "archived") {
      return "已归档";
    }
    return status || "未知";
  }

  function crmJobsStatusOptions(selected, includeAll) {
    var options = includeAll ? [["", "全部状态"]] : [];
    options = options.concat([
      ["open", "开放"],
      ["closed", "已关闭"],
      ["archived", "已归档"]
    ]);
    return options.map(function (option) {
      return '<option value="' + option[0] + '"' + (selected === option[0] ? " selected" : "") + ">" + option[1] + "</option>";
    }).join("");
  }

  function crmJobsPriorityLabel(priority) {
    if (priority === "low") {
      return "低";
    }
    if (priority === "normal") {
      return "普通";
    }
    if (priority === "high") {
      return "高";
    }
    if (priority === "urgent") {
      return "紧急";
    }
    return priority || "未知";
  }

  function crmJobsPriorityOptions(selected, includeAll) {
    var options = includeAll ? [["", "全部优先级"]] : [];
    options = options.concat([
      ["low", "低"],
      ["normal", "普通"],
      ["high", "高"],
      ["urgent", "紧急"]
    ]);
    return options.map(function (option) {
      return '<option value="' + option[0] + '"' + (selected === option[0] ? " selected" : "") + ">" + option[1] + "</option>";
    }).join("");
  }

  function crmJobsParseList(value) {
    return String(value || "").split(/\r?\n|,/).map(function (item) {
      return item.trim();
    }).filter(Boolean);
  }

  function crmJobsParseRequirements(value) {
    return crmJobsParseList(value);
  }

  function crmJobsRequirementValues(job) {
    if (!job || !Array.isArray(job.requirements)) {
      return [];
    }
    return job.requirements.map(function (item) {
      return String(item == null ? "" : item).trim();
    }).filter(Boolean);
  }

  function crmJobsDocumentValues(job) {
    if (!job || !Array.isArray(job.required_documents)) {
      return [];
    }
    return job.required_documents.map(function (item) {
      return String(item == null ? "" : item).trim();
    }).filter(Boolean);
  }

  function crmJobsDateTimeLocal(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return "";
    }
    var local = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
    return local.toISOString().slice(0, 16);
  }

  function crmJobsDateTimeAPI(value) {
    if (!value) {
      return null;
    }
    var date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date.toISOString();
  }

  function crmJobsFormFromJob(job) {
    return {
      title: job.title || "",
      company: job.company || "",
      location: job.location || "",
      employment_type: job.employment_type || "",
      description: job.description || "",
      requirements: crmJobsRequirementValues(job).join("\n"),
      demand_code: job.demand_code || "",
      site_name: job.site_name || "",
      onsite_contact: job.onsite_contact || "",
      headcount: String(job.headcount == null ? 0 : job.headcount),
      priority: job.priority || "normal",
      start_at: crmJobsDateTimeLocal(job.start_at),
      deadline_at: crmJobsDateTimeLocal(job.deadline_at),
      shift: job.shift || "",
      work_schedule: job.work_schedule || "",
      compensation_summary: job.compensation_summary || "",
      accommodation: job.accommodation || "",
      transport_requirements: job.transport_requirements || "",
      required_documents: crmJobsDocumentValues(job).join("\n"),
      owner: job.owner || "",
      status: job.status || "open"
    };
  }

  function crmJobsSyncForm(form, state) {
    state.form = {
      title: form.elements.title.value,
      company: form.elements.company.value,
      location: form.elements.location.value,
      employment_type: form.elements.employment_type.value,
      description: form.elements.description.value,
      requirements: form.elements.requirements.value,
      demand_code: form.elements.demand_code.value,
      site_name: form.elements.site_name.value,
      onsite_contact: form.elements.onsite_contact.value,
      headcount: form.elements.headcount.value,
      priority: form.elements.priority.value,
      start_at: form.elements.start_at.value,
      deadline_at: form.elements.deadline_at.value,
      shift: form.elements.shift.value,
      work_schedule: form.elements.work_schedule.value,
      compensation_summary: form.elements.compensation_summary.value,
      accommodation: form.elements.accommodation.value,
      transport_requirements: form.elements.transport_requirements.value,
      required_documents: form.elements.required_documents.value,
      owner: form.elements.owner.value,
      status: state.mode === "create" ? form.elements.status.value : state.form.status
    };
    state.dirty = true;
  }

  function crmJobsRender(state) {
    return '<section class="topbar">' +
      '<div><h2>职位</h2><p>维护客户需求、现场安排、入场条件和招聘交付进度。</p></div>' +
      '<div class="crmJobsTopActions">' +
      '<button type="button" data-crm-jobs-action="reload" ' + crmJobsDisabled(state.loading) + '>刷新</button>' +
      '<button class="primary" type="button" data-crm-jobs-action="create" ' + crmJobsDisabled(state.saving) + '>新建职位</button>' +
      "</div></section>" +
      '<section class="crmJobsLayout" aria-label="职位管理工作区">' +
      '<div class="panel"><div class="panel-header"><h3>职位列表</h3><span class="status" aria-live="polite">' +
      crmJobsEscape("匹配 " + state.total + " 个") + "</span></div>" +
      '<div class="panel-body">' + crmJobsRenderFilters(state) +
      crmJobsWorkbench.renderStatus(state.error, state.success, state.loading ? "正在加载职位..." : "") +
      crmJobsRenderList(state) + crmJobsRenderPager(state) + "</div></div>" +
      '<div class="panel"><div class="panel-header"><h3>' +
      (state.mode === "create" ? "新建职位" : "职位详情与编辑") +
      '</h3><span class="status">' + crmJobsEditorStatus(state) + "</span></div>" +
      '<div class="panel-body">' + crmJobsRenderEditor(state) + "</div></div>" +
      "</section>";
  }

  function crmJobsRenderFilters(state) {
    return '<form class="crmJobsFilters" data-crm-jobs-form="filters">' +
      '<label>搜索<input name="q" value="' + crmJobsEscape(state.q) + '" autocomplete="off" placeholder="需求编号、职位、客户、联系人、证件" ' + crmJobsDisabled(state.loading) + "></label>" +
      '<label>状态<select name="status" ' + crmJobsDisabled(state.loading) + ">" + crmJobsStatusOptions(state.status, true) + "</select></label>" +
      '<label>优先级<select name="priority" ' + crmJobsDisabled(state.loading) + ">" + crmJobsPriorityOptions(state.priority, true) + "</select></label>" +
      '<label>现场<input name="site_name" value="' + crmJobsEscape(state.site_name) + '" autocomplete="off" placeholder="现场名称" ' + crmJobsDisabled(state.loading) + "></label>" +
      '<label>负责人<input name="owner" value="' + crmJobsEscape(state.owner) + '" autocomplete="off" placeholder="负责人" ' + crmJobsDisabled(state.loading) + "></label>" +
      '<button class="primary" type="submit" ' + crmJobsDisabled(state.loading) + '>筛选</button>' +
      "</form>";
  }

  function crmJobsRenderList(state) {
    if (state.loading && !state.loaded && !state.jobs.length) {
      return '<div class="notice">正在加载职位列表...</div>';
    }
    if (state.loaded && !state.jobs.length) {
      return '<div class="notice">当前筛选条件下没有职位。编辑器中的内容仍会保留。</div>';
    }
    if (!state.jobs.length) {
      return '<div class="notice">职位列表尚未加载。</div>';
    }
    return '<div class="crmJobsList" role="list">' + state.jobs.map(function (item) {
      var job = item && item.job ? item.job : {};
      var requirements = crmJobsRequirementValues(job);
      var documents = crmJobsDocumentValues(job);
      var selected = state.selectedID === job.id;
      var pending = state.pendingSelection === job.id;
      var latestDecision = item && item.latest_ingestion_decision;
      var provenanceCount = Number(item && item.provenance_count || 0);
      return '<button class="crmJobsRow" type="button" role="listitem" data-crm-jobs-id="' + crmJobsEscape(job.id) +
        '" aria-current="' + (selected ? "true" : "false") + '" ' + crmJobsDisabled(state.detailLoading && pending) + ">" +
        '<span class="crmJobsRowHead"><strong>' + crmJobsEscape(job.title || "未命名职位") +
        '</strong><span class="crmJobsStatus crmJobsStatus-' + crmJobsEscape(job.status) + '">' +
        crmJobsEscape(crmJobsStatusLabel(job.status)) + "</span></span>" +
        '<span class="crmJobsRowFields">' +
        '<span>需求 ' + crmJobsEscape(job.demand_code || "未编号") + "</span>" +
        '<span>公司 ' + crmJobsEscape(job.company || "未填写") + "</span>" +
        '<span>现场 ' + crmJobsEscape(job.site_name || job.location || "未填写") + "</span>" +
        '<span>人数 ' + crmJobsEscape(job.headcount == null ? 0 : job.headcount) + "</span>" +
        '<span>优先级 ' + crmJobsEscape(crmJobsPriorityLabel(job.priority)) + "</span>" +
        '<span>截止 ' + crmJobsEscape(job.deadline_at || "未设置") + "</span>" +
        '<span>负责人 ' + crmJobsEscape(job.owner || "未分配") + "</span>" +
        '<span>候选线索 ' + crmJobsEscape(item.lead_count || 0) + "</span>" +
        (latestDecision ? '<span>摄取 ' + crmJobsEscape(crmJobsDecisionLabel(latestDecision)) + "</span>" : "") +
        (provenanceCount ? '<span>来源记录 ' + crmJobsEscape(provenanceCount) + "</span>" : "") +
        "</span>" +
        '<span class="crmJobsRowRequirements">要求：' +
        crmJobsEscape(requirements.length ? requirements.join(" · ") : "未填写") +
        "；证件：" + crmJobsEscape(documents.length ? documents.join(" · ") : "未填写") + "</span></button>";
    }).join("") + "</div>";
  }

  function crmJobsRenderPager(state) {
    var start = state.total === 0 ? 0 : state.offset + 1;
    var end = Math.min(state.total, state.offset + state.jobs.length);
    return '<div class="crmJobsPager"><span>显示 ' + crmJobsEscape(start) + "-" + crmJobsEscape(end) +
      " / 共 " + crmJobsEscape(state.total) + " 个</span><div>" +
      '<button type="button" data-crm-jobs-action="previous" ' + crmJobsDisabled(state.loading || state.offset === 0) + '>上一页</button>' +
      '<button type="button" data-crm-jobs-action="next" ' + crmJobsDisabled(state.loading || state.offset + state.limit >= state.total) + '>下一页</button>' +
      "</div></div>";
  }

  function crmJobsEditorStatus(state) {
    if (state.detailLoading) {
      return "正在读取详情";
    }
    if (state.saving) {
      return "正在保存";
    }
    if (state.mode === "create") {
      return state.dirty ? "未保存的新职位" : "填写职位信息";
    }
    return state.dirty ? "有未保存修改" : "版本 " + crmJobsEscape(state.detail && state.detail.job ? state.detail.job.version : "");
  }

  function crmJobsRenderEditor(state) {
    var detail = state.detail;
    var job = detail && detail.job ? detail.job : null;
    var currentStatus = job ? job.status : state.form.status;
    var form = state.form;
    var busy = state.saving || !!state.statusSaving;
    return '<form class="crmJobsEditor" data-crm-jobs-form="editor">' +
      '<fieldset class="crmJobsSection"><legend>需求基础</legend><div class="crmJobsFieldGrid">' +
      '<label>职位名称<input name="title" maxlength="160" required value="' + crmJobsEscape(form.title) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>公司<input name="company" maxlength="160" required value="' + crmJobsEscape(form.company) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>需求编号<input name="demand_code" maxlength="120" value="' + crmJobsEscape(form.demand_code) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>负责人<input name="owner" maxlength="80" value="' + crmJobsEscape(form.owner) + '" ' + crmJobsDisabled(busy) + "></label>" +
      crmJobsRenderStatusField(state, currentStatus, busy) +
      '<label>优先级<select name="priority" ' + crmJobsDisabled(busy) + ">" +
      crmJobsPriorityOptions(form.priority, false) + "</select></label></div></fieldset>" +
      '<fieldset class="crmJobsSection"><legend>现场与时间</legend><div class="crmJobsFieldGrid">' +
      '<label>地点<input name="location" maxlength="160" value="' + crmJobsEscape(form.location) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>现场名称<input name="site_name" maxlength="160" value="' + crmJobsEscape(form.site_name) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>现场联系人<input name="onsite_contact" maxlength="160" value="' + crmJobsEscape(form.onsite_contact) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>需求人数<input name="headcount" type="number" min="0" max="1000000" step="1" value="' + crmJobsEscape(form.headcount) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>开始时间<input name="start_at" type="datetime-local" value="' + crmJobsEscape(form.start_at) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>截止时间<input name="deadline_at" type="datetime-local" value="' + crmJobsEscape(form.deadline_at) + '" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>班次<input name="shift" maxlength="160" value="' + crmJobsEscape(form.shift) + '" placeholder="白班、夜班、两班倒" ' + crmJobsDisabled(busy) + "></label>" +
      '<label>雇佣类型<input name="employment_type" maxlength="80" value="' + crmJobsEscape(form.employment_type) + '" placeholder="全职、实习、合同制" ' + crmJobsDisabled(busy) + "></label>" +
      "</div></fieldset>" +
      '<fieldset class="crmJobsSection"><legend>现场保障</legend><div class="crmJobsTextGrid">' +
      '<label>工作安排<textarea name="work_schedule" maxlength="2000" rows="4" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.work_schedule) + "</textarea></label>" +
      '<label>薪酬说明<textarea name="compensation_summary" maxlength="2000" rows="4" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.compensation_summary) + "</textarea></label>" +
      '<label>住宿安排<textarea name="accommodation" maxlength="2000" rows="4" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.accommodation) + "</textarea></label>" +
      '<label>交通要求<textarea name="transport_requirements" maxlength="2000" rows="4" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.transport_requirements) + "</textarea></label>" +
      "</div></fieldset>" +
      '<label>职位描述<textarea name="description" maxlength="8000" rows="6" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.description) + "</textarea></label>" +
      '<label>岗位要求<textarea name="requirements" rows="7" placeholder="每行一项，也可用逗号分隔" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.requirements) + "</textarea></label>" +
      crmJobsRenderRequirementPreview(form.requirements) +
      '<label>入场证件<textarea name="required_documents" rows="6" placeholder="每行一种证件或材料" ' + crmJobsDisabled(busy) + ">" + crmJobsEscape(form.required_documents) + "</textarea></label>" +
      crmJobsRenderDocumentPreview(form.required_documents) +
      '<div class="crmJobsFormActions"><button class="primary" type="submit" ' + crmJobsDisabled(busy) + ">" +
      (state.saving ? "保存中..." : state.mode === "create" ? "创建职位" : "保存修改") + "</button>" +
      '<button type="button" data-crm-jobs-action="reset" ' + crmJobsDisabled(busy) + '>重置</button></div>' +
      "</form>" + crmJobsRenderDetail(state);
  }

  function crmJobsRenderStatusField(state, currentStatus, busy) {
    if (state.mode === "create") {
      return '<label>初始状态<select name="status" ' + crmJobsDisabled(busy) + ">" +
        crmJobsStatusOptions(state.form.status, false) + "</select></label>";
    }
    return '<label>当前状态<output class="crmJobsStatusOutput">' +
      '<span class="crmJobsStatus crmJobsStatus-' + crmJobsEscape(currentStatus) + '">' +
      crmJobsEscape(crmJobsStatusLabel(currentStatus)) + "</span></output></label>" +
      '<input type="hidden" name="status" value="' + crmJobsEscape(currentStatus) + '">';
  }

  function crmJobsRenderRequirementPreview(value) {
    var requirements = crmJobsParseRequirements(value);
    if (!requirements.length) {
      return '<div class="crmJobsRequirementPreview"><span class="status">尚未填写岗位要求</span></div>';
    }
    return '<div class="crmJobsRequirementPreview" aria-label="岗位要求预览">' + requirements.map(function (requirement) {
      return '<span>' + crmJobsEscape(requirement) + "</span>";
    }).join("") + "</div>";
  }

  function crmJobsRenderDocumentPreview(value) {
    var documents = crmJobsParseList(value);
    if (!documents.length) {
      return '<div class="crmJobsRequirementPreview"><span class="status">尚未填写入场证件</span></div>';
    }
    return '<div class="crmJobsRequirementPreview" aria-label="入场证件预览">' + documents.map(function (documentName) {
      return '<span>' + crmJobsEscape(documentName) + "</span>";
    }).join("") + "</div>";
  }

  function crmJobsRenderDetail(state) {
    if (state.mode === "create") {
      return '<section class="crmJobsDetail"><h4>关联信息</h4><p class="status">职位创建后显示版本、候选线索和更新时间。</p></section>';
    }
    if (!state.detail || !state.detail.job) {
      return '<section class="crmJobsDetail"><h4>关联信息</h4><p class="status">职位详情尚未加载，当前草稿保持不变。</p></section>';
    }
    var detail = state.detail;
    var job = detail.job;
    var leadIDs = Array.isArray(detail.lead_ids) ? detail.lead_ids : [];
    var requirements = crmJobsRequirementValues(job);
    var documents = crmJobsDocumentValues(job);
    return '<section class="crmJobsDetail" aria-label="职位结构化详情"><div class="crmJobsDetailHead"><h4>结构化详情</h4>' +
      crmJobsRenderStatusActions(state, job) + "</div><dl>" +
      crmJobsDetailValue("职位 ID", job.id) +
      crmJobsDetailValue("需求编号", job.demand_code || "无") +
      crmJobsDetailValue("状态", crmJobsStatusLabel(job.status)) +
      crmJobsDetailValue("优先级", crmJobsPriorityLabel(job.priority)) +
      crmJobsDetailValue("需求人数", job.headcount == null ? 0 : job.headcount) +
      crmJobsDetailValue("客户公司", job.company || "无") +
      crmJobsDetailValue("地点", job.location || "无") +
      crmJobsDetailValue("雇佣类型", job.employment_type || "无") +
      crmJobsDetailValue("负责人", job.owner || "无") +
      crmJobsDetailValue("现场名称", job.site_name || "无") +
      crmJobsDetailValue("现场联系人", job.onsite_contact || "无") +
      crmJobsDetailValue("开始时间", job.start_at || "无") +
      crmJobsDetailValue("截止时间", job.deadline_at || "无") +
      crmJobsDetailValue("班次", job.shift || "无") +
      crmJobsDetailValue("工作安排", job.work_schedule || "无") +
      crmJobsDetailValue("薪酬说明", job.compensation_summary || "无") +
      crmJobsDetailValue("住宿安排", job.accommodation || "无") +
      crmJobsDetailValue("交通要求", job.transport_requirements || "无") +
      crmJobsDetailValue("职位描述", job.description || "无") +
      crmJobsDetailValue("岗位要求", requirements.length ? requirements.join(", ") : "无") +
      crmJobsDetailValue("入场证件", documents.length ? documents.join(", ") : "无") +
      crmJobsDetailValue("关联线索数", detail.lead_count || 0) +
      crmJobsDetailValue("线索 ID", leadIDs.length ? leadIDs.join(", ") : "无") +
      crmJobsDetailValue("版本", job.version) +
      crmJobsDetailValue("创建时间", job.created_at || "无") +
      crmJobsDetailValue("更新时间", job.updated_at || "无") +
      "</dl></section>" + crmJobsRenderProvenance(detail) + crmJobsRenderRevisionHistory(state, job);
  }

  function crmJobsDecisionLabel(decision) {
    var labels = {
      create: "创建",
      update: "更新",
      duplicate: "重复",
      noop: "无变化",
      noise: "噪声",
      failure: "失败"
    };
    return labels[decision] || decision || "未知";
  }

  function crmJobsProvenance(detail) {
    return Array.isArray(detail && detail.provenance) ? detail.provenance : [];
  }

  function crmJobsRenderProvenance(detail) {
    var provenance = crmJobsProvenance(detail);
    return '<section class="crmJobsProvenance" aria-label="不可变职位来源记录">' +
      '<div class="crmJobsRevisionHead"><div><h4>来源与摄取决策</h4><span>' +
      crmJobsEscape(provenance.length + " 条 append-only provenance") + '</span></div><span class="status">' +
      crmJobsEscape(provenance.length ? "权威来源" : "手工或未知来源") + "</span></div>" +
      (provenance.length ? '<div class="crmJobsProvenanceList">' + provenance.map(function (entry) {
        var record = entry || {};
        return '<article class="crmJobsProvenanceRow"><div><strong>' +
          crmJobsEscape(crmJobsDecisionLabel(record.decision)) + '</strong><span>' +
          crmJobsEscape(record.id || "无 provenance ID") + '</span></div><dl>' +
          crmJobsDetailValue("来源群", record.group_id || "无") +
          crmJobsDetailValue("来源作者", record.author_id || "无") +
          crmJobsDetailValue("事件", record.event_id || "无") +
          crmJobsDetailValue("消息键", record.message_key || "无") +
          crmJobsDetailValue("职位修订", record.job_revision == null ? "无" : record.job_revision) +
          crmJobsDetailValue("需求编号", record.demand_code || "无") +
          crmJobsDetailValue("来源时间", record.source_timestamp || "无") +
          crmJobsDetailValue("内容 SHA-256", record.content_sha256 || "无") +
          crmJobsDetailValue("规范指纹", record.canonical_fingerprint || "无") +
          crmJobsDetailValue("记录时间", record.created_at || "无") +
          "</dl></article>";
      }).join("") + '</div>' : '<p class="status">暂无来源摄取记录。</p>') + "</section>";
  }

  function crmJobsDetailValue(label, value) {
    return "<dt>" + crmJobsEscape(label) + "</dt><dd>" + crmJobsEscape(value) + "</dd>";
  }

  function crmJobsRenderRevisionHistory(state, job) {
    var revisions = Array.isArray(state.revisions) ? state.revisions : [];
    var busy = state.revisionsLoading || state.revisionLoading || state.comparisonLoading || !!state.activatingRevision;
    return '<section class="crmJobsRevisions" aria-label="需求版本历史">' +
      '<div class="crmJobsRevisionHead"><div><h4>需求版本</h4><span>当前 #' +
      crmJobsEscape(job.demand_revision_number || 0) + '</span></div>' +
      '<button type="button" data-crm-jobs-action="reload-revisions" ' + crmJobsDisabled(busy) + '>刷新版本</button></div>' +
      crmJobsWorkbench.renderStatus(state.revisionError, "", state.revisionsLoading ? "正在加载需求版本..." : "") +
      (revisions.length ? '<div class="crmJobsRevisionList">' + revisions.map(function (revision) {
        var active = revision.id === job.active_demand_revision_id;
        return '<article class="crmJobsRevisionRow"><button type="button" data-crm-jobs-revision="' +
          crmJobsEscape(revision.id) + '" ' + crmJobsDisabled(busy) + '><strong>#' +
          crmJobsEscape(revision.number) + (active ? " · 当前" : "") + '</strong><span>' +
          crmJobsEscape(revision.created_at || "") + '</span><small>' +
          crmJobsEscape(revision.created_by || "system") + ' · ' +
          crmJobsEscape(revision.reason || "无原因") + '</small></button>' +
          (active ? "" : '<button type="button" data-crm-jobs-activate-revision="' +
            crmJobsEscape(revision.id) + '" ' + crmJobsDisabled(busy || state.dirty) + '>' +
            (state.activatingRevision === revision.id ? "激活中..." : "激活") + '</button>') +
          '</article>';
      }).join("") + '</div>' : '<p class="status">尚无需求版本。</p>') +
      crmJobsRenderRevisionCompare(state, revisions, busy) +
      crmJobsRenderRevisionDetail(state) +
      '</section>';
  }

  function crmJobsRenderRevisionCompare(state, revisions, busy) {
    if (revisions.length < 2) {
      return "";
    }
    var options = revisions.map(function (revision) {
      return '<option value="' + crmJobsEscape(revision.id) + '">#' +
        crmJobsEscape(revision.number) + '</option>';
    }).join("");
    return '<form class="crmJobsRevisionCompare" data-crm-jobs-form="compare">' +
      '<label>从<select name="from" ' + crmJobsDisabled(busy) + '>' +
      options.replace('value="' + crmJobsEscape(state.compareFrom) + '"', 'value="' +
        crmJobsEscape(state.compareFrom) + '" selected') + '</select></label>' +
      '<label>到<select name="to" ' + crmJobsDisabled(busy) + '>' +
      options.replace('value="' + crmJobsEscape(state.compareTo) + '"', 'value="' +
        crmJobsEscape(state.compareTo) + '" selected') + '</select></label>' +
      '<button type="submit" ' + crmJobsDisabled(busy) + '>' +
      (state.comparisonLoading ? "比较中..." : "比较") + '</button></form>';
  }

  function crmJobsRenderRevisionDetail(state) {
    if (state.comparison && Array.isArray(state.comparison.changes)) {
      return '<div class="crmJobsRevisionDetail"><h5>版本差异</h5>' +
        (state.comparison.changes.length ? '<dl>' + state.comparison.changes.map(function (change) {
          return crmJobsDetailValue(change.field, crmJobsRevisionValue(change.before) + " → " +
            crmJobsRevisionValue(change.after));
        }).join("") + '</dl>' : '<p class="status">两个版本没有字段差异。</p>') + '</div>';
    }
    if (!state.revisionDetail || !state.revisionDetail.snapshot) {
      return "";
    }
    var revision = state.revisionDetail;
    var snapshot = revision.snapshot;
    return '<div class="crmJobsRevisionDetail"><h5>版本 #' + crmJobsEscape(revision.number) + '</h5><dl>' +
      crmJobsDetailValue("职位", snapshot.title || "") +
      crmJobsDetailValue("公司", snapshot.company || "") +
      crmJobsDetailValue("需求编号", snapshot.demand_code || "无") +
      crmJobsDetailValue("人数", snapshot.headcount == null ? 0 : snapshot.headcount) +
      crmJobsDetailValue("优先级", crmJobsPriorityLabel(snapshot.priority)) +
      crmJobsDetailValue("现场", snapshot.site_name || snapshot.location || "无") +
      crmJobsDetailValue("要求", crmJobsRevisionValue(snapshot.requirements)) +
      crmJobsDetailValue("证件", crmJobsRevisionValue(snapshot.required_documents)) +
      '</dl></div>';
  }

  function crmJobsRevisionValue(value) {
    if (Array.isArray(value)) {
      return value.length ? value.join(", ") : "无";
    }
    if (value == null || value === "") {
      return "无";
    }
    return String(value);
  }

  function crmJobsRenderStatusActions(state, job) {
    if (job.status === "archived") {
      return '<span class="status">归档职位不可重新开启</span>';
    }
    var actions = [];
    if (job.status === "open") {
      actions.push(["closed", "关闭职位"]);
    } else if (job.status === "closed") {
      actions.push(["open", "重新开启"]);
    }
    actions.push(["archived", "归档职位"]);
    return '<div class="crmJobsStatusActions">' + actions.map(function (action) {
      return '<button type="button" data-crm-jobs-status="' + action[0] + '" ' +
        crmJobsDisabled(state.saving || !!state.statusSaving) + ">" +
        (state.statusSaving === action[0] ? "处理中..." : action[1]) + "</button>";
    }).join("") + "</div>";
  }

  function crmJobsLoadList(state) {
    var sequence = ++state.listSequence;
    state.loading = true;
    state.error = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs" + crmJobsWorkbench.encodeQuery({
      q: state.q,
      status: state.status,
      priority: state.priority,
      site_name: state.site_name,
      owner: state.owner,
      limit: state.limit,
      offset: state.offset
    })).then(function (data) {
      if (sequence !== state.listSequence) {
        return;
      }
      state.total = Number(data && data.total || 0);
      state.jobs = data && Array.isArray(data.jobs) ? data.jobs : [];
      state.loaded = true;
    }).catch(function (err) {
      if (sequence === state.listSequence) {
        state.error = crmJobsErrorMessage(err, "加载职位失败");
      }
    }).finally(function () {
      if (sequence === state.listSequence) {
        state.loading = false;
        crmJobsWorkbench.requestRender();
      }
    });
  }

  function crmJobsLoadDetail(state, id) {
    var sequence = ++state.detailSequence;
    state.pendingSelection = id;
    state.detailLoading = true;
    state.error = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(id)).then(function (detail) {
      if (sequence !== state.detailSequence || !detail || !detail.job) {
        return;
      }
      state.selectedID = id;
      state.detail = detail;
      state.mode = "edit";
      state.form = crmJobsFormFromJob(detail.job);
      state.dirty = false;
      state.success = "";
      state.revisions = Array.isArray(detail.revisions) ? detail.revisions : [];
      state.activeRevisionID = detail.job.active_demand_revision_id || "";
      state.revisionDetail = null;
      state.comparison = null;
      state.compareFrom = "";
      state.compareTo = "";
      return crmJobsLoadRevisions(state, id);
    }).catch(function (err) {
      if (sequence === state.detailSequence) {
        state.error = crmJobsErrorMessage(err, "加载职位详情失败");
      }
    }).finally(function () {
      if (sequence === state.detailSequence) {
        state.pendingSelection = "";
        state.detailLoading = false;
        crmJobsWorkbench.requestRender();
      }
    });
  }

  function crmJobsLoadRevisions(state, jobID) {
    if (!jobID) {
      return Promise.resolve();
    }
    state.revisionsLoading = true;
    state.revisionError = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(jobID) + "/revisions").then(function (data) {
      if (!state.detail || !state.detail.job || state.detail.job.id !== jobID) {
        return;
      }
      state.revisions = data && Array.isArray(data.revisions) ? data.revisions : [];
      state.activeRevisionID = data && data.active_revision_id || "";
      if (!state.compareFrom && state.revisions.length) {
        state.compareFrom = state.revisions[state.revisions.length - 1].id;
      }
      if (!state.compareTo && state.revisions.length) {
        state.compareTo = state.revisions[0].id;
      }
    }).catch(function (err) {
      state.revisionError = crmJobsErrorMessage(err, "加载需求版本失败");
    }).finally(function () {
      state.revisionsLoading = false;
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsLoadRevision(state, revisionID) {
    if (!state.detail || !state.detail.job) {
      return;
    }
    state.revisionLoading = true;
    state.revisionError = "";
    state.comparison = null;
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(state.detail.job.id) +
      "/revisions/" + encodeURIComponent(revisionID)).then(function (revision) {
      state.revisionDetail = revision;
    }).catch(function (err) {
      state.revisionError = crmJobsErrorMessage(err, "加载需求版本详情失败");
    }).finally(function () {
      state.revisionLoading = false;
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsCompareRevisions(state, form) {
    if (!state.detail || !state.detail.job) {
      return;
    }
    state.compareFrom = form.elements.from.value;
    state.compareTo = form.elements.to.value;
    state.comparisonLoading = true;
    state.revisionError = "";
    state.revisionDetail = null;
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(state.detail.job.id) +
      "/revisions/compare" + crmJobsWorkbench.encodeQuery({
        from: state.compareFrom,
        to: state.compareTo
      })).then(function (comparison) {
      state.comparison = comparison;
    }).catch(function (err) {
      state.revisionError = crmJobsErrorMessage(err, "比较需求版本失败");
    }).finally(function () {
      state.comparisonLoading = false;
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsActivateRevision(state, revisionID) {
    if (!state.detail || !state.detail.job || state.dirty) {
      return;
    }
    var revision = state.revisions.find(function (item) {
      return item.id === revisionID;
    });
    if (!revision || !window.confirm("确认激活需求版本 #" + revision.number + "？当前职位需求字段将回滚到该版本。")) {
      return;
    }
    state.activatingRevision = revisionID;
    state.revisionError = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.automationAPI("/api/crm/jobs/" + encodeURIComponent(state.detail.job.id) +
      "/revisions/" + encodeURIComponent(revisionID) + "/activate", {
        method: "POST",
        body: { expected_version: state.detail.job.version }
      }).then(function (job) {
      state.detail.job = job;
      state.form = crmJobsFormFromJob(job);
      state.dirty = false;
      state.revisionDetail = null;
      state.comparison = null;
      state.success = "已激活需求版本 #" + revision.number + "。";
      crmJobsUpdateListItem(state, job);
      return crmJobsLoadRevisions(state, job.id);
    }).catch(function (err) {
      if (crmJobsIsConflict(err)) {
        return crmJobsRecoverStaleDetail(state, "激活需求版本失败");
      }
      state.revisionError = crmJobsErrorMessage(err, "激活需求版本失败");
      return null;
    }).finally(function () {
      state.activatingRevision = "";
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsSave(state, form) {
    crmJobsSyncForm(form, state);
    var body = {
      title: state.form.title.trim(),
      company: state.form.company.trim(),
      location: state.form.location.trim(),
      employment_type: state.form.employment_type.trim(),
      description: state.form.description.trim(),
      requirements: crmJobsParseRequirements(state.form.requirements),
      demand_code: state.form.demand_code.trim(),
      site_name: state.form.site_name.trim(),
      onsite_contact: state.form.onsite_contact.trim(),
      headcount: Number(state.form.headcount),
      priority: state.form.priority,
      shift: state.form.shift.trim(),
      work_schedule: state.form.work_schedule.trim(),
      compensation_summary: state.form.compensation_summary.trim(),
      accommodation: state.form.accommodation.trim(),
      transport_requirements: state.form.transport_requirements.trim(),
      required_documents: crmJobsParseList(state.form.required_documents),
      owner: state.form.owner.trim()
    };
    if (!body.title || !body.company) {
      state.error = "职位名称和公司不能为空。";
      state.success = "";
      crmJobsWorkbench.requestRender();
      return;
    }
    if (!Number.isInteger(body.headcount) || body.headcount < 0 || body.headcount > 1000000) {
      state.error = "需求人数必须是 0 到 1000000 之间的整数。";
      state.success = "";
      crmJobsWorkbench.requestRender();
      return;
    }
    var startAt = crmJobsDateTimeAPI(state.form.start_at);
    var deadlineAt = crmJobsDateTimeAPI(state.form.deadline_at);
    if (state.form.start_at && !startAt) {
      state.error = "开始时间格式无效。";
      crmJobsWorkbench.requestRender();
      return;
    }
    if (state.form.deadline_at && !deadlineAt) {
      state.error = "截止时间格式无效。";
      crmJobsWorkbench.requestRender();
      return;
    }
    if (startAt && deadlineAt && new Date(deadlineAt) < new Date(startAt)) {
      state.error = "截止时间不能早于开始时间。";
      crmJobsWorkbench.requestRender();
      return;
    }
    var method = "POST";
    var path = "/api/crm/jobs";
    if (state.mode === "create") {
      body.status = state.form.status;
      body.start_at = startAt;
      body.deadline_at = deadlineAt;
    } else {
      if (!state.detail || !state.detail.job) {
        state.error = "职位详情尚未加载，无法保存修改。";
        crmJobsWorkbench.requestRender();
        return;
      }
      method = "PATCH";
      path += "/" + encodeURIComponent(state.detail.job.id);
      body.expected_version = state.detail.job.version;
      if (startAt) {
        body.start_at = startAt;
      } else if (state.detail.job.start_at) {
        body.clear_start_at = true;
      }
      if (deadlineAt) {
        body.deadline_at = deadlineAt;
      } else if (state.detail.job.deadline_at) {
        body.clear_deadline_at = true;
      }
    }
    state.saving = true;
    state.error = "";
    state.success = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api(path, { method: method, body: body }).then(function (job) {
      var previousDetail = state.detail;
      var sameJob = previousDetail && previousDetail.job && previousDetail.job.id === job.id;
      state.selectedID = job.id;
      state.detail = {
        job: job,
        lead_ids: sameJob && Array.isArray(previousDetail.lead_ids) ? previousDetail.lead_ids : [],
        lead_count: sameJob ? Number(previousDetail.lead_count || 0) : 0,
        provenance: sameJob && Array.isArray(previousDetail.provenance) ? previousDetail.provenance : []
      };
      state.mode = "edit";
      state.form = crmJobsFormFromJob(job);
      state.dirty = false;
      state.success = method === "POST" ? "职位已创建。" : "职位已更新。";
      crmJobsUpdateListItem(state, job);
      return Promise.all([
        crmJobsLoadList(state),
        crmJobsLoadRevisions(state, job.id)
      ]);
    }).catch(function (err) {
      if (method === "PATCH" && crmJobsIsConflict(err)) {
        return crmJobsRecoverStaleDetail(state, "保存职位失败");
      }
      state.error = crmJobsErrorMessage(err, "保存职位失败");
    }).finally(function () {
      state.saving = false;
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsSetStatus(state, status) {
    if (!state.detail || !state.detail.job) {
      return;
    }
    var job = state.detail.job;
    state.statusSaving = status;
    state.error = "";
    state.success = "";
    crmJobsWorkbench.requestRender();
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(job.id) + "/status", {
      method: "POST",
      body: {
        expected_version: job.version,
        status: status
      }
    }).then(function (updated) {
      state.detail.job = updated;
      state.form.status = updated.status;
      state.success = "职位状态已更新为" + crmJobsStatusLabel(updated.status) + "。";
      crmJobsUpdateListItem(state, updated);
      return crmJobsLoadList(state);
    }).catch(function (err) {
      if (crmJobsIsConflict(err)) {
        return crmJobsRecoverStaleDetail(state, "更新职位状态失败");
      }
      state.error = crmJobsErrorMessage(err, "更新职位状态失败");
    }).finally(function () {
      state.statusSaving = "";
      crmJobsWorkbench.requestRender();
    });
  }

  function crmJobsUpdateListItem(state, job) {
    var existing = state.jobs.find(function (item) {
      return item && item.job && item.job.id === job.id;
    });
    if (existing) {
      existing.job = job;
      return;
    }
    state.jobs.unshift({ job: job, lead_count: 0 });
  }

  function crmJobsIsConflict(err) {
    return !!(err && (err.status === 409 || /conflict|409/i.test(err.message || "")));
  }

  function crmJobsRecoverStaleDetail(state, actionLabel) {
    if (!state.detail || !state.detail.job) {
      state.error = actionLabel + "（409 版本冲突）：当前草稿已保留。";
      return Promise.resolve();
    }
    var draft = state.form;
    var id = state.detail.job.id;
    return crmJobsWorkbench.api("/api/crm/jobs/" + encodeURIComponent(id)).then(function (latest) {
      if (!latest || !latest.job) {
        throw new Error("latest job detail is unavailable");
      }
      state.detail = latest;
      state.form = draft;
      state.dirty = true;
      state.error = actionLabel + "（409 版本冲突）：当前草稿已保留，已加载服务端最新版本 " +
        crmJobsEscape(latest.job.version) + "，可核对后再次保存。";
      crmJobsUpdateListItem(state, latest.job);
    }).catch(function (reloadErr) {
      state.form = draft;
      state.dirty = true;
      state.error = actionLabel + "（409 版本冲突）：当前草稿已保留；读取服务端最新版本失败：" +
        (reloadErr && reloadErr.message ? reloadErr.message : "未知错误");
    });
  }

  function crmJobsErrorMessage(err, fallback) {
    var message = err && err.message ? err.message : "未知错误";
    if (/conflict/i.test(message)) {
      return fallback + "（409 版本冲突）：请保留当前草稿并确认服务端最新版本。";
    }
    if (/persistence|operation failed|internal server/i.test(message)) {
      return fallback + "（500）：服务暂不可用，当前草稿已保留。";
    }
    if (/validation|invalid|required|must /i.test(message)) {
      return fallback + "（400）：" + message;
    }
    return fallback + "：" + message;
  }

  function crmJobsStartCreate(state) {
    state.mode = "create";
    state.selectedID = "";
    state.detail = null;
    state.revisions = [];
    state.activeRevisionID = "";
    state.revisionDetail = null;
    state.comparison = null;
    state.compareFrom = "";
    state.compareTo = "";
    state.revisionError = "";
    state.form = crmJobsEmptyForm();
    state.dirty = false;
    state.error = "";
    state.success = "";
    crmJobsWorkbench.requestRender();
  }

  function crmJobsResetEditor(state) {
    state.form = state.mode === "edit" && state.detail && state.detail.job ?
      crmJobsFormFromJob(state.detail.job) : crmJobsEmptyForm();
    state.dirty = false;
    state.error = "";
    state.success = "";
    crmJobsWorkbench.requestRender();
  }

  function crmJobsBind(root, state) {
    var editor = root.querySelector('form[data-crm-jobs-form="editor"]');
    if (editor) {
      editor.addEventListener("input", function () {
        crmJobsSyncForm(editor, state);
      });
      editor.addEventListener("change", function () {
        crmJobsSyncForm(editor, state);
      });
      editor.addEventListener("submit", function (event) {
        event.preventDefault();
        crmJobsSave(state, editor);
      });
    }
    var filters = root.querySelector('form[data-crm-jobs-form="filters"]');
    if (filters) {
      filters.addEventListener("submit", function (event) {
        event.preventDefault();
        state.q = filters.elements.q.value.trim();
        state.status = filters.elements.status.value;
        state.priority = filters.elements.priority.value;
        state.site_name = filters.elements.site_name.value.trim();
        state.owner = filters.elements.owner.value.trim();
        state.offset = 0;
        state.success = "";
        crmJobsLoadList(state);
      });
    }
    var compare = root.querySelector('form[data-crm-jobs-form="compare"]');
    if (compare) {
      compare.addEventListener("submit", function (event) {
        event.preventDefault();
        crmJobsCompareRevisions(state, compare);
      });
    }
    root.addEventListener("click", function (event) {
      var row = event.target.closest("[data-crm-jobs-id]");
      if (row && root.contains(row)) {
        crmJobsLoadDetail(state, row.getAttribute("data-crm-jobs-id"));
        return;
      }
      var revisionButton = event.target.closest("[data-crm-jobs-revision]");
      if (revisionButton && root.contains(revisionButton)) {
        crmJobsLoadRevision(state, revisionButton.getAttribute("data-crm-jobs-revision"));
        return;
      }
      var activateButton = event.target.closest("[data-crm-jobs-activate-revision]");
      if (activateButton && root.contains(activateButton)) {
        crmJobsActivateRevision(state, activateButton.getAttribute("data-crm-jobs-activate-revision"));
        return;
      }
      var statusButton = event.target.closest("[data-crm-jobs-status]");
      if (statusButton && root.contains(statusButton)) {
        crmJobsSetStatus(state, statusButton.getAttribute("data-crm-jobs-status"));
        return;
      }
      var actionButton = event.target.closest("[data-crm-jobs-action]");
      if (!actionButton || !root.contains(actionButton)) {
        return;
      }
      var action = actionButton.getAttribute("data-crm-jobs-action");
      if (action === "reload") {
        crmJobsLoadList(state);
      } else if (action === "create") {
        crmJobsStartCreate(state);
      } else if (action === "reset") {
        crmJobsResetEditor(state);
      } else if (action === "reload-revisions" && state.detail && state.detail.job) {
        crmJobsLoadRevisions(state, state.detail.job.id);
      } else if (action === "previous" && state.offset > 0) {
        state.offset = Math.max(0, state.offset - state.limit);
        crmJobsLoadList(state);
      } else if (action === "next" && state.offset + state.limit < state.total) {
        state.offset += state.limit;
        crmJobsLoadList(state);
      }
    });
  }

  crmJobsWorkbench.registerView({
    id: "jobs",
    label: "职位",
    section: "招聘交付",
    order: 30,
    createState: crmJobsCreateState,
    render: crmJobsRender,
    onEnter: function (state) {
      if (!state.loaded && !state.loading) {
        return crmJobsLoadList(state);
      }
      return null;
    },
    bind: crmJobsBind
  });
})();
