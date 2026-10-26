(function () {
  "use strict";

  var workbench = window.OneBotWorkbench;
  var PAGE_SIZE = 20;
  var STATUSES = [
    { value: "active", label: "活跃" },
    { value: "paused", label: "暂停" },
    { value: "placed", label: "已入职" },
    { value: "archived", label: "已归档" }
  ];

  function crmCandidatesBlankForm() {
    return {
      full_name: "",
      wechat_id: "",
      phone: "",
      email: "",
      location: "",
      source: "",
      skills: "",
      owner: "",
      status: "active",
      summary: "",
      version: 0
    };
  }

  function crmCandidatesCreateState() {
    return {
      filters: {
        q: "",
        status: "",
        owner: "",
        source: ""
      },
      page: 0,
      total: 0,
      candidates: [],
      loaded: false,
      loading: false,
      detailLoading: false,
      detailTarget: "",
      saving: false,
      selectedId: "",
      detail: null,
      formMode: "",
      form: crmCandidatesBlankForm(),
      error: "",
      success: "",
      listRequest: 0,
      detailRequest: 0
    };
  }

  function crmCandidatesStatusLabel(status) {
    var item = STATUSES.find(function (entry) {
      return entry.value === status;
    });
    return item ? item.label : String(status || "未知");
  }

  function crmCandidatesSplitSkills(value) {
    var seen = Object.create(null);
    return String(value || "")
      .split(/[\n,，;；]+/)
      .map(function (skill) { return skill.trim(); })
      .filter(function (skill) {
        if (!skill || seen[skill]) {
          return false;
        }
        seen[skill] = true;
        return true;
      });
  }

  function crmCandidatesCandidateForm(candidate) {
    return {
      full_name: candidate.full_name || "",
      wechat_id: candidate.wechat_id || "",
      phone: candidate.phone || "",
      email: candidate.email || "",
      location: candidate.location || "",
      source: candidate.source || "",
      skills: Array.isArray(candidate.skills) ? candidate.skills.join(", ") : "",
      owner: candidate.owner || "",
      status: candidate.status || "active",
      summary: candidate.summary || "",
      version: Number(candidate.version) || 0
    };
  }

  function crmCandidatesDraft(form) {
    return {
      full_name: form.full_name.trim(),
      wechat_id: form.wechat_id.trim(),
      phone: form.phone.trim(),
      email: form.email.trim(),
      location: form.location.trim(),
      source: form.source.trim(),
      skills: crmCandidatesSplitSkills(form.skills),
      owner: form.owner.trim(),
      status: form.status,
      summary: form.summary.trim()
    };
  }

  function crmCandidatesFormatDate(value) {
    if (!value) {
      return "—";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit"
    });
  }

  function crmCandidatesLoadList(state, api, requestedPage) {
    var page = Math.max(0, Number(requestedPage) || 0);
    var request = state.listRequest + 1;
    state.listRequest = request;
    state.loading = true;
    state.error = "";
    api.requestRender();

    return api.api("/api/crm/candidates" + api.encodeQuery({
      q: state.filters.q.trim(),
      status: state.filters.status,
      owner: state.filters.owner.trim(),
      source: state.filters.source.trim(),
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE
    })).then(function (data) {
      if (request !== state.listRequest) {
        return;
      }
      state.total = Number(data && data.total) || 0;
      state.candidates = data && Array.isArray(data.candidates) ? data.candidates : [];
      state.page = page;
      state.loaded = true;
    }).catch(function (err) {
      if (request === state.listRequest) {
        state.error = err.message;
      }
    }).finally(function () {
      if (request === state.listRequest) {
        state.loading = false;
        api.requestRender();
      }
    });
  }

  function crmCandidatesLoadDetail(state, api, id) {
    var request = state.detailRequest + 1;
    state.detailRequest = request;
    state.detailLoading = true;
    state.detailTarget = id;
    state.error = "";
    api.requestRender();

    return api.api("/api/crm/candidates/" + encodeURIComponent(id)).then(function (detail) {
      if (request !== state.detailRequest) {
        return;
      }
      if (!detail || !detail.candidate) {
        throw new Error("候选人详情响应无效");
      }
      var nextForm = crmCandidatesCandidateForm(detail.candidate);
      state.selectedId = id;
      state.detail = detail;
      state.formMode = "";
      state.form = nextForm;
    }).catch(function (err) {
      if (request === state.detailRequest) {
        state.error = err.message;
      }
    }).finally(function () {
      if (request === state.detailRequest) {
        state.detailLoading = false;
        state.detailTarget = "";
        api.requestRender();
      }
    });
  }

  function crmCandidatesSave(state, api) {
    var draft = crmCandidatesDraft(state.form);
    var editing = state.formMode === "edit";
    var selectedID = state.selectedId;
    var path = "/api/crm/candidates";
    var options = {
      method: "POST",
      body: draft
    };

    if (editing) {
      path += "/" + encodeURIComponent(selectedID);
      options.method = "PATCH";
      options.body = Object.assign({ expected_version: state.form.version }, draft);
    }

    state.saving = true;
    state.error = "";
    state.success = "";
    api.requestRender();

    return api.api(path, options).then(function (candidate) {
      if (!candidate || !candidate.id) {
        throw new Error("候选人保存响应无效");
      }
      var previousDetail = state.detail;
      state.selectedId = candidate.id;
      state.detail = {
        candidate: candidate,
        message_event_ids: editing && previousDetail && Array.isArray(previousDetail.message_event_ids) ? previousDetail.message_event_ids : [],
        lead_ids: editing && previousDetail && Array.isArray(previousDetail.lead_ids) ? previousDetail.lead_ids : [],
        open_task_ids: editing && previousDetail && Array.isArray(previousDetail.open_task_ids) ? previousDetail.open_task_ids : [],
        note_count: editing && previousDetail ? Number(previousDetail.note_count) || 0 : 0
      };
      state.form = crmCandidatesCandidateForm(candidate);
      state.formMode = "";
      state.success = editing ? "候选人资料已更新。" : "候选人已创建。";
      return crmCandidatesLoadList(state, api, editing ? state.page : 0);
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.saving = false;
      api.requestRender();
    });
  }

  function crmCandidatesStatusOptions(selected, includeAll) {
    var html = includeAll ? '<option value="">全部状态</option>' : "";
    return html + STATUSES.map(function (status) {
      return '<option value="' + status.value + '"' + (selected === status.value ? " selected" : "") + ">" + status.label + "</option>";
    }).join("");
  }

  function crmCandidatesRenderList(state, api) {
    if (!state.candidates.length) {
      if (state.loading) {
        return '<div class="crmCandidatesEmpty" aria-live="polite">正在加载候选人...</div>';
      }
      return '<div class="crmCandidatesEmpty"><strong>没有匹配的候选人</strong><span>调整搜索条件或创建候选人。</span></div>';
    }

    return '<div class="crmCandidatesList">' + state.candidates.map(function (item) {
      var candidate = item && item.candidate ? item.candidate : {};
      var selected = state.selectedId === candidate.id;
      var loading = state.detailLoading && state.detailTarget === candidate.id;
      var meta = [
        candidate.owner ? "负责人 " + candidate.owner : "未分配负责人",
        "Lead " + (Number(item.active_lead_count) || 0),
        "任务 " + (Number(item.open_task_count) || 0)
      ].join(" · ");
      return '<button class="crmCandidatesRow" type="button" data-crm-candidates-id="' + api.escapeHTML(candidate.id || "") + '" aria-current="' + (selected ? "true" : "false") + '">' +
        '<span class="crmCandidatesRowMain"><strong>' + api.escapeHTML(candidate.full_name || "未命名候选人") + '</strong>' +
        '<span>' + api.escapeHTML(meta) + "</span></span>" +
        '<span class="crmCandidatesRowSide"><span class="crmCandidatesStatus crmCandidatesStatus--' + api.escapeHTML(candidate.status || "unknown") + '">' + api.escapeHTML(crmCandidatesStatusLabel(candidate.status)) + "</span>" +
        '<small>' + api.escapeHTML(loading ? "加载中" : (candidate.location || candidate.source || candidate.id || "—")) + "</small></span>" +
        "</button>";
    }).join("") + "</div>";
  }

  function crmCandidatesRenderPager(state, api) {
    var totalPages = Math.max(1, Math.ceil(state.total / PAGE_SIZE));
    var start = state.total ? state.page * PAGE_SIZE + 1 : 0;
    var end = Math.min(state.total, (state.page + 1) * PAGE_SIZE);
    return '<div class="crmCandidatesPager">' +
      '<button type="button" data-crm-candidates-action="previous" ' + (state.page === 0 || state.loading ? "disabled" : "") + ">上一页</button>" +
      '<span>' + api.escapeHTML(start + "–" + end + " / " + state.total + "，第 " + (state.page + 1) + " / " + totalPages + " 页") + "</span>" +
      '<button type="button" data-crm-candidates-action="next" ' + ((state.page + 1) >= totalPages || state.loading ? "disabled" : "") + ">下一页</button>" +
      "</div>";
  }

  function crmCandidatesRenderIDList(values, api) {
    if (!Array.isArray(values) || !values.length) {
      return '<span class="crmCandidatesMuted">无</span>';
    }
    return '<div class="crmCandidatesIDs">' + values.map(function (value) {
      return "<code>" + api.escapeHTML(value) + "</code>";
    }).join("") + "</div>";
  }

  function crmCandidatesRenderSkills(skills, api) {
    if (!Array.isArray(skills) || !skills.length) {
      return '<span class="crmCandidatesMuted">未填写</span>';
    }
    return '<div class="crmCandidatesSkills">' + skills.map(function (skill) {
      return "<span>" + api.escapeHTML(skill) + "</span>";
    }).join("") + "</div>";
  }

  function crmCandidatesRenderDetail(state, api) {
    if (!state.detail || !state.detail.candidate) {
      return '<div class="crmCandidatesEmpty crmCandidatesEmpty--detail"><strong>选择候选人查看详情</strong><span>目录选择与表单内容会在请求失败时保留。</span></div>';
    }

    var detail = state.detail;
    var candidate = detail.candidate;
    var sourceIDs = Array.isArray(detail.message_event_ids) ? detail.message_event_ids.slice() : [];
    if (candidate.source_event_id && sourceIDs.indexOf(candidate.source_event_id) < 0) {
      sourceIDs.unshift(candidate.source_event_id);
    }

    return '<div class="crmCandidatesDetail">' +
      '<div class="crmCandidatesDetailHead"><div><h3>' + api.escapeHTML(candidate.full_name || "未命名候选人") + "</h3>" +
      '<p class="crmCandidatesBreak">' + api.escapeHTML(candidate.id || "—") + "</p></div>" +
      '<div class="crmCandidatesDetailActions"><span class="crmCandidatesStatus crmCandidatesStatus--' + api.escapeHTML(candidate.status || "unknown") + '">' + api.escapeHTML(crmCandidatesStatusLabel(candidate.status)) + "</span>" +
      '<button type="button" data-crm-candidates-action="reload-detail" ' + (state.detailLoading ? "disabled" : "") + ">刷新详情</button>" +
      '<button class="primary" type="button" data-crm-candidates-action="edit">编辑</button></div></div>' +
      '<div class="crmCandidatesSummary">' + api.escapeHTML(candidate.summary || "暂无候选人摘要。") + "</div>" +
      '<section class="crmCandidatesSection"><h4>归属与来源</h4><dl class="crmCandidatesKV">' +
      "<dt>负责人</dt><dd>" + api.escapeHTML(candidate.owner || "未分配") + "</dd>" +
      "<dt>状态</dt><dd>" + api.escapeHTML(crmCandidatesStatusLabel(candidate.status)) + "</dd>" +
      "<dt>来源</dt><dd>" + api.escapeHTML(candidate.source || "未填写") + "</dd>" +
      "<dt>地点</dt><dd>" + api.escapeHTML(candidate.location || "未填写") + "</dd>" +
      "<dt>来源消息</dt><dd>" + crmCandidatesRenderIDList(sourceIDs, api) + "</dd></dl></section>" +
      '<section class="crmCandidatesSection"><h4>技能</h4>' + crmCandidatesRenderSkills(candidate.skills, api) + "</section>" +
      '<section class="crmCandidatesSection"><h4>联系信息</h4><dl class="crmCandidatesKV">' +
      "<dt>微信号</dt><dd class=\"crmCandidatesBreak\">" + api.escapeHTML(candidate.wechat_id || "未填写") + "</dd>" +
      "<dt>电话</dt><dd class=\"crmCandidatesBreak\">" + api.escapeHTML(candidate.phone || "未填写") + "</dd>" +
      "<dt>邮箱</dt><dd class=\"crmCandidatesBreak\">" + api.escapeHTML(candidate.email || "未填写") + "</dd></dl></section>" +
      '<section class="crmCandidatesSection"><h4>招聘活动汇总</h4><div class="crmCandidatesMetrics">' +
      '<div><strong>' + api.escapeHTML(Array.isArray(detail.lead_ids) ? detail.lead_ids.length : 0) + "</strong><span>关联 Lead</span></div>" +
      '<div><strong>' + api.escapeHTML(Array.isArray(detail.open_task_ids) ? detail.open_task_ids.length : 0) + "</strong><span>开放任务</span></div>" +
      '<div><strong>' + api.escapeHTML(Number(detail.note_count) || 0) + "</strong><span>备注</span></div></div>" +
      '<dl class="crmCandidatesKV"><dt>Lead ID</dt><dd>' + crmCandidatesRenderIDList(detail.lead_ids, api) + "</dd>" +
      "<dt>开放任务 ID</dt><dd>" + crmCandidatesRenderIDList(detail.open_task_ids, api) + "</dd></dl></section>" +
      '<section class="crmCandidatesSection"><h4>记录信息</h4><dl class="crmCandidatesKV">' +
      "<dt>版本</dt><dd>" + api.escapeHTML(candidate.version || 0) + "</dd>" +
      "<dt>创建时间</dt><dd>" + api.escapeHTML(crmCandidatesFormatDate(candidate.created_at)) + "</dd>" +
      "<dt>更新时间</dt><dd>" + api.escapeHTML(crmCandidatesFormatDate(candidate.updated_at)) + "</dd></dl></section>" +
      "</div>";
  }

  function crmCandidatesRenderForm(state, api) {
    var form = state.form;
    var editing = state.formMode === "edit";
    return '<form class="crmCandidatesForm" data-crm-candidates-form="candidate">' +
      '<div class="crmCandidatesFormHead"><div><h3>' + (editing ? "编辑候选人" : "创建候选人") + "</h3>" +
      "<p>" + (editing ? api.escapeHTML(state.selectedId) : "新增候选人资料") + "</p></div>" +
      '<button type="button" data-crm-candidates-action="cancel-form" ' + (state.saving ? "disabled" : "") + ">取消</button></div>" +
      '<div class="crmCandidatesFormGrid">' +
      '<label>姓名<input name="full_name" required maxlength="160" value="' + api.escapeHTML(form.full_name) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>状态<select name="status" ' + (state.saving ? "disabled" : "") + ">" + crmCandidatesStatusOptions(form.status, false) + "</select></label>" +
      '<label>负责人<input name="owner" maxlength="80" value="' + api.escapeHTML(form.owner) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>来源<input name="source" maxlength="160" value="' + api.escapeHTML(form.source) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>地点<input name="location" maxlength="160" value="' + api.escapeHTML(form.location) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>微信号<input name="wechat_id" maxlength="160" value="' + api.escapeHTML(form.wechat_id) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>电话<input name="phone" maxlength="80" value="' + api.escapeHTML(form.phone) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label>邮箱<input name="email" type="email" maxlength="320" value="' + api.escapeHTML(form.email) + '" ' + (state.saving ? "disabled" : "") + "></label>" +
      '<label class="crmCandidatesWide">技能（逗号或换行分隔）<textarea name="skills" maxlength="4096" ' + (state.saving ? "disabled" : "") + ">" + api.escapeHTML(form.skills) + "</textarea></label>" +
      '<label class="crmCandidatesWide">摘要<textarea name="summary" maxlength="4000" ' + (state.saving ? "disabled" : "") + ">" + api.escapeHTML(form.summary) + "</textarea></label></div>" +
      '<div class="crmCandidatesFormActions"><button class="primary" type="submit" ' + (state.saving ? "disabled" : "") + ">" + (state.saving ? "保存中..." : (editing ? "保存修改" : "创建候选人")) + "</button>" +
      '<span class="crmCandidatesMuted">' + (editing ? "基于版本 " + api.escapeHTML(form.version) + " 更新" : "默认创建为活跃状态") + "</span></div>" +
      "</form>";
  }

  function crmCandidatesRender(state, api) {
    var busyText = "";
    if (state.loading) {
      busyText = "正在加载候选人目录...";
    } else if (state.detailLoading) {
      busyText = "正在加载候选人详情...";
    } else if (state.saving) {
      busyText = "正在保存候选人资料...";
    }

    return '<section class="topbar crmCandidatesTopbar"><div><h2>候选人</h2><p>集中检索、创建和维护候选人档案与招聘活动关联。</p></div>' +
      '<div class="crmCandidatesTopActions"><button type="button" data-crm-candidates-action="refresh" ' + (state.loading ? "disabled" : "") + ">刷新</button>" +
      '<button class="primary" type="button" data-crm-candidates-action="create" ' + (state.saving ? "disabled" : "") + ">创建候选人</button></div></section>" +
      api.renderStatus(state.error, state.success, busyText) +
      '<section class="crmCandidatesWorkspace" aria-label="候选人目录工作区">' +
      '<div class="panel crmCandidatesDirectory"><div class="panel-header"><h3>候选人目录</h3><span class="status">' + api.escapeHTML("匹配 " + state.total + " 人") + "</span></div>" +
      '<div class="panel-body"><form class="crmCandidatesFilters" data-crm-candidates-form="filters">' +
      '<label>搜索<input name="q" type="search" maxlength="200" placeholder="姓名、技能、联系方式" value="' + api.escapeHTML(state.filters.q) + '"></label>' +
      '<label>状态<select name="status">' + crmCandidatesStatusOptions(state.filters.status, true) + "</select></label>" +
      '<label>负责人<input name="owner" maxlength="80" value="' + api.escapeHTML(state.filters.owner) + '"></label>' +
      '<label>来源<input name="source" maxlength="160" value="' + api.escapeHTML(state.filters.source) + '"></label>' +
      '<div class="crmCandidatesFilterActions"><button class="primary" type="submit" ' + (state.loading ? "disabled" : "") + ">筛选</button>" +
      '<button type="button" data-crm-candidates-action="clear-filters" ' + (state.loading ? "disabled" : "") + ">清除</button></div></form>" +
      crmCandidatesRenderList(state, api) + crmCandidatesRenderPager(state, api) + "</div></div>" +
      '<div class="panel crmCandidatesInspector"><div class="panel-header"><h3>' + (state.formMode ? (state.formMode === "edit" ? "编辑资料" : "新建资料") : "候选人详情") + "</h3>" +
      '<span class="status">' + api.escapeHTML(state.selectedId || "未选择") + "</span></div><div class=\"panel-body\">" +
      (state.formMode ? crmCandidatesRenderForm(state, api) : crmCandidatesRenderDetail(state, api)) +
      "</div></div></section>";
  }

  function crmCandidatesReadForm(form, target) {
    [
      "full_name",
      "wechat_id",
      "phone",
      "email",
      "location",
      "source",
      "skills",
      "owner",
      "status",
      "summary"
    ].forEach(function (name) {
      if (form.elements[name]) {
        target[name] = form.elements[name].value;
      }
    });
  }

  function crmCandidatesBind(root, state, api) {
    root.addEventListener("input", function (event) {
      var form = event.target.form;
      if (!form) {
        return;
      }
      var kind = form.getAttribute("data-crm-candidates-form");
      if (kind === "candidate") {
        crmCandidatesReadForm(form, state.form);
      } else if (kind === "filters" && event.target.name && Object.prototype.hasOwnProperty.call(state.filters, event.target.name)) {
        state.filters[event.target.name] = event.target.value;
      }
    });

    root.addEventListener("change", function (event) {
      var form = event.target.form;
      if (!form) {
        return;
      }
      var kind = form.getAttribute("data-crm-candidates-form");
      if (kind === "candidate") {
        crmCandidatesReadForm(form, state.form);
      } else if (kind === "filters" && event.target.name && Object.prototype.hasOwnProperty.call(state.filters, event.target.name)) {
        state.filters[event.target.name] = event.target.value;
      }
    });

    root.addEventListener("submit", function (event) {
      var kind = event.target.getAttribute("data-crm-candidates-form");
      if (kind === "filters") {
        event.preventDefault();
        crmCandidatesLoadList(state, api, 0);
      } else if (kind === "candidate") {
        event.preventDefault();
        crmCandidatesReadForm(event.target, state.form);
        crmCandidatesSave(state, api);
      }
    });

    root.addEventListener("click", function (event) {
      var row = event.target.closest("[data-crm-candidates-id]");
      if (row && root.contains(row)) {
        crmCandidatesLoadDetail(state, api, row.getAttribute("data-crm-candidates-id"));
        return;
      }

      var button = event.target.closest("[data-crm-candidates-action]");
      if (!button || !root.contains(button)) {
        return;
      }
      var action = button.getAttribute("data-crm-candidates-action");
      if (action === "refresh") {
        crmCandidatesLoadList(state, api, state.page);
      } else if (action === "previous" && state.page > 0) {
        crmCandidatesLoadList(state, api, state.page - 1);
      } else if (action === "next" && (state.page + 1) * PAGE_SIZE < state.total) {
        crmCandidatesLoadList(state, api, state.page + 1);
      } else if (action === "clear-filters") {
        state.filters = { q: "", status: "", owner: "", source: "" };
        crmCandidatesLoadList(state, api, 0);
      } else if (action === "create") {
        state.formMode = "create";
        state.form = crmCandidatesBlankForm();
        state.error = "";
        state.success = "";
        api.requestRender();
      } else if (action === "edit" && state.detail && state.detail.candidate) {
        state.formMode = "edit";
        state.form = crmCandidatesCandidateForm(state.detail.candidate);
        state.error = "";
        state.success = "";
        api.requestRender();
      } else if (action === "cancel-form") {
        state.formMode = "";
        state.form = state.detail && state.detail.candidate ? crmCandidatesCandidateForm(state.detail.candidate) : crmCandidatesBlankForm();
        state.error = "";
        api.requestRender();
      } else if (action === "reload-detail" && state.selectedId) {
        crmCandidatesLoadDetail(state, api, state.selectedId);
      }
    });
  }

  workbench.registerView({
    id: "candidates",
    label: "候选人",
    section: "招聘交付",
    order: 20,
    createState: crmCandidatesCreateState,
    render: crmCandidatesRender,
    onEnter: function (state, api) {
      if (!state.loaded && !state.loading) {
        return crmCandidatesLoadList(state, api, state.page);
      }
      return null;
    },
    bind: crmCandidatesBind
  });
})();
