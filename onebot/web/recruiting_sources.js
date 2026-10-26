(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench) {
    return;
  }

  var PROJECT_PATH = "/api/crm/recruiting/project";
  var SOURCES_PATH = "/api/crm/recruiting/source_groups";
  var GROUPS_PATH = "/api/groups";

  function createState() {
    return {
      entered: false,
      project: {
        organization_name: "国爱人力",
        version: 0,
        require_authoritative_job: true,
        source_groups: []
      },
      organizationDraft: "国爱人力",
      contacts: [],
      loading: true,
      contactsLoading: false,
      refreshingContacts: false,
      savingOrganization: false,
      savingSource: false,
      actionSourceID: "",
      error: "",
      success: "",
      form: emptySourceForm()
    };
  }

  function emptySourceForm() {
    return {
      id: "",
      group_id: "",
      display_name: "",
      enabled: true,
      gate_enabled: false,
      gate_mode: "job_relevance"
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

  function groupDisplayName(group) {
    return group && (group.remark || group.nick_name || group.username) || "未命名群聊";
  }

  function projectSources(state) {
    return state.project && Array.isArray(state.project.source_groups) ?
      state.project.source_groups : [];
  }

  function contactByGroupID(state, groupID) {
    return state.contacts.find(function (group) {
      return group.username === groupID;
    }) || null;
  }

  function applyProject(state, project, preserveOrganization, preserveSource) {
    var organizationDraft = state.organizationDraft;
    var sourceDraft = state.form;
    state.project = project || {
      organization_name: "国爱人力",
      version: 0,
      require_authoritative_job: true,
      source_groups: []
    };
    state.organizationDraft = preserveOrganization ?
      organizationDraft : (state.project.organization_name || "国爱人力");
    if (preserveSource) {
      state.form = sourceDraft;
    } else if (state.form.id) {
      var current = projectSources(state).find(function (source) {
        return source.id === state.form.id;
      });
      state.form = current ? sourceForm(current) : emptySourceForm();
    }
  }

  function sourceForm(source) {
    return {
      id: source.id || "",
      group_id: source.group_id || "",
      display_name: source.display_name || "",
      enabled: !!source.enabled,
      gate_enabled: !!source.gate_enabled,
      gate_mode: source.gate_mode || "job_relevance"
    };
  }

  function isVersionConflict(err) {
    return !!(err && (err.status === 409 || /version conflict|版本冲突/i.test(err.message || "")));
  }

  function errorMessage(err) {
    if (isVersionConflict(err)) {
      return "409 版本冲突：服务端配置已变化。";
    }
    return err && err.message ? err.message : "操作失败";
  }

  function loadProject(state, preserveOrganization, preserveSource, propagateError) {
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.automationAPI(PROJECT_PATH).then(function (project) {
      applyProject(state, project, preserveOrganization, preserveSource);
    }).catch(function (err) {
      state.error = errorMessage(err);
      if (propagateError) {
        throw err;
      }
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function loadContacts(state, propagateError) {
    state.contactsLoading = true;
    Workbench.requestRender();
    return Workbench.api(GROUPS_PATH).then(function (data) {
      state.contacts = data && Array.isArray(data.groups) ? data.groups : [];
    }).catch(function (err) {
      state.error = errorMessage(err);
      state.contacts = [];
      if (propagateError) {
        throw err;
      }
    }).finally(function () {
      state.contactsLoading = false;
      Workbench.requestRender();
    });
  }

  function reloadData(state) {
    if (state.loading || state.contactsLoading || state.refreshingContacts) {
      return null;
    }
    return Promise.all([
      loadProject(state, false, false),
      loadContacts(state)
    ]);
  }

  function responseCount(data, key) {
    var count = Number(data && data[key]);
    return Number.isFinite(count) && count >= 0 ? Math.floor(count) : 0;
  }

  function refreshContactsForAccount(state) {
    if (state.refreshingContacts || state.loading || state.contactsLoading) {
      return null;
    }
    state.refreshingContacts = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.refreshContactData().then(function (data) {
      return Promise.all([
        loadProject(state, false, false, true),
        loadContacts(state, true)
      ]).then(function () {
        state.success = "通讯录已刷新：" + responseCount(data, "contacts") +
          " 个联系人，" + responseCount(data, "groups") + " 个群聊。";
      });
    }).catch(function (err) {
      state.success = "";
      state.error = errorMessage(err);
    }).finally(function () {
      state.refreshingContacts = false;
      Workbench.requestRender();
    });
  }

  function recoverStale(state, preserveOrganization, preserveSource) {
    return loadProject(state, preserveOrganization, preserveSource).then(function () {
      if (!state.error) {
        state.error = "当前草稿已保留，已加载服务端最新项目版本。";
        Workbench.requestRender();
      }
    });
  }

  function saveOrganization(state, form) {
    state.organizationDraft = form.elements.organization_name.value.trim();
    if (!state.organizationDraft) {
      state.error = "组织名称不能为空。";
      Workbench.requestRender();
      return;
    }
    state.savingOrganization = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(PROJECT_PATH, {
      method: "PATCH",
      body: {
        expected_version: state.project.version,
        organization_name: state.organizationDraft
      }
    }).then(function (project) {
      applyProject(state, project, false, false);
      state.success = "组织名称已保存。";
    }).catch(function (err) {
      if (isVersionConflict(err)) {
        return recoverStale(state, true, true);
      }
      state.error = errorMessage(err);
    }).finally(function () {
      state.savingOrganization = false;
      Workbench.requestRender();
    });
  }

  function syncSourceForm(state, form) {
    state.form.group_id = form.elements.group_id.value.trim();
    state.form.display_name = form.elements.display_name.value.trim();
    state.form.enabled = form.elements.enabled.checked;
    state.form.gate_enabled = form.elements.gate_enabled.checked;
    state.form.gate_mode = form.elements.gate_mode.value;
  }

  function saveSource(state, form) {
    syncSourceForm(state, form);
    if (!state.form.group_id.endsWith("@chatroom")) {
      state.error = "群 ID 必须是有效的 @chatroom 标识。";
      Workbench.requestRender();
      return;
    }
    var editing = !!state.form.id;
    var path = editing ? SOURCES_PATH + "/" + encodeURIComponent(state.form.id) : SOURCES_PATH;
    state.savingSource = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(path, {
      method: editing ? "PATCH" : "POST",
      body: {
        expected_project_version: state.project.version,
        group_id: state.form.group_id,
        display_name: state.form.display_name,
        enabled: state.form.enabled,
        gate_enabled: state.form.gate_enabled,
        gate_mode: state.form.gate_mode
      }
    }).then(function (project) {
      applyProject(state, project, false, false);
      state.form = emptySourceForm();
      state.success = editing ? "来源组已更新。" : "来源组已添加。";
    }).catch(function (err) {
      if (isVersionConflict(err)) {
        return recoverStale(state, true, true);
      }
      state.error = errorMessage(err);
    }).finally(function () {
      state.savingSource = false;
      Workbench.requestRender();
    });
  }

  function editSource(state, sourceID) {
    var source = projectSources(state).find(function (item) {
      return item.id === sourceID;
    });
    if (!source) {
      return;
    }
    state.form = sourceForm(source);
    state.error = "";
    state.success = "";
    Workbench.requestRender();
  }

  function toggleSource(state, sourceID) {
    var source = projectSources(state).find(function (item) {
      return item.id === sourceID;
    });
    if (!source) {
      return;
    }
    state.actionSourceID = sourceID;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(SOURCES_PATH + "/" + encodeURIComponent(sourceID), {
      method: "PATCH",
      body: {
        expected_project_version: state.project.version,
        enabled: !source.enabled
      }
    }).then(function (project) {
      applyProject(state, project, false, false);
      state.success = source.enabled ? "来源组已停用。" : "来源组已启用。";
    }).catch(function (err) {
      if (isVersionConflict(err)) {
        return recoverStale(state, true, true);
      }
      state.error = errorMessage(err);
    }).finally(function () {
      state.actionSourceID = "";
      Workbench.requestRender();
    });
  }

  function removeSource(state, sourceID) {
    var source = projectSources(state).find(function (item) {
      return item.id === sourceID;
    });
    if (!source || !window.confirm("确认移除来源组“" + (source.display_name || source.group_id) + "”？")) {
      return;
    }
    state.actionSourceID = sourceID;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(SOURCES_PATH + "/" + encodeURIComponent(sourceID), {
      method: "DELETE",
      body: {
        expected_project_version: state.project.version
      }
    }).then(function (project) {
      applyProject(state, project, false, false);
      if (state.form.id === sourceID) {
        state.form = emptySourceForm();
      }
      state.success = "来源组已移除。";
    }).catch(function (err) {
      if (isVersionConflict(err)) {
        return recoverStale(state, true, true);
      }
      state.error = errorMessage(err);
    }).finally(function () {
      state.actionSourceID = "";
      Workbench.requestRender();
    });
  }

  function contactOptions(state) {
    var options = ['<option value="">从群聊联系人选择</option>'];
    state.contacts.forEach(function (group) {
      var label = groupDisplayName(group) + " (" + (group.username || "") + ")";
      options.push(
        '<option value="' + escapeHTML(group.username || "") + '"' +
        selected(group.username, state.form.group_id) + ">" +
        escapeHTML(label) + "</option>"
      );
    });
    return options.join("");
  }

  function renderSourceRows(state) {
    var sources = projectSources(state);
    if (state.loading && !sources.length) {
      return '<div class="recruitingSources__empty">正在加载来源组...</div>';
    }
    if (!sources.length) {
      return '<div class="recruitingSources__empty">尚未配置招聘来源组。</div>';
    }
    return '<div class="recruitingSources__list" role="list">' + sources.map(function (source) {
      var contact = contactByGroupID(state, source.group_id);
      var busy = state.actionSourceID === source.id ||
        state.savingSource || !Number(state.project.version);
      return '<article class="recruitingSources__row" role="listitem">' +
        '<div class="recruitingSources__rowMain"><div><strong>' +
        escapeHTML(source.display_name || contact && groupDisplayName(contact) || source.group_id) +
        '</strong><span>' + escapeHTML(source.group_id) + '</span></div><span class="recruitingSources__badge recruitingSources__badge--' +
        (source.enabled ? "enabled" : "disabled") + '">' + (source.enabled ? "已启用" : "已停用") + "</span></div>" +
        '<div class="recruitingSources__meta"><span>来源版本 ' + escapeHTML(source.version) + '</span><span>' +
        (contact ? "群联系人已匹配" : "群联系人未匹配") + '</span><span>' +
        (source.gate_enabled ? "职位相关性门控已启用" : "职位相关性门控未启用") + "</span></div>" +
        '<div class="recruitingSources__actions">' +
        '<button type="button" data-source-action="edit" data-source-id="' + escapeHTML(source.id) + '"' +
        disabled(busy) + ">编辑</button>" +
        '<button type="button" data-source-action="toggle" data-source-id="' + escapeHTML(source.id) + '"' +
        disabled(busy) + ">" + (source.enabled ? "停用" : "启用") + "</button>" +
        '<button class="danger" type="button" data-source-action="remove" data-source-id="' +
        escapeHTML(source.id) + '"' + disabled(busy) + ">移除</button></div></article>";
    }).join("") + "</div>";
  }

  function renderSourceForm(state) {
    var editing = !!state.form.id;
    var locked = state.savingSource || state.loading || !Number(state.project.version);
    return '<form class="recruitingSources__form" data-source-form="source">' +
      '<div class="recruitingSources__formHead"><div><h3>' + (editing ? "编辑来源组" : "添加来源组") +
      '</h3><span>' + (editing ? escapeHTML(state.form.group_id) : "项目版本 " + escapeHTML(state.project.version)) +
      '</span></div>' + (editing ?
        '<button type="button" data-source-action="new"' + disabled(locked) + ">新建</button>" : "") + "</div>" +
      '<label>群聊联系人<select name="contact_group_id"' +
      disabled(locked || state.contactsLoading) + ">" + contactOptions(state) + "</select></label>" +
      '<label>群 ID<input name="group_id" value="' + escapeHTML(state.form.group_id) +
      '" placeholder="synthetic_jobs@chatroom" maxlength="256" autocomplete="off"' +
      disabled(locked) + "></label>" +
      '<label>显示名称<input name="display_name" value="' + escapeHTML(state.form.display_name) +
      '" maxlength="160" autocomplete="off"' + disabled(locked) + "></label>" +
      '<label class="recruitingSources__toggle"><input name="enabled" type="checkbox"' +
      (state.form.enabled ? " checked" : "") + disabled(locked) + ">启用来源</label>" +
      '<label class="recruitingSources__toggle"><input name="gate_enabled" type="checkbox"' +
      (state.form.gate_enabled ? " checked" : "") + disabled(locked) + ">启用职位相关性门控</label>" +
      '<label>门控模式<select name="gate_mode"' +
      disabled(locked || !state.form.gate_enabled) + '><option value="job_relevance"' +
      selected("job_relevance", state.form.gate_mode) + ">职位相关性</option></select></label>" +
      '<div class="recruitingSources__formActions"><button class="primary" type="submit"' +
      disabled(locked) + ">" + (state.savingSource ? "保存中..." : editing ? "保存修改" : "添加来源") +
      '</button><button type="button" data-source-action="reset"' + disabled(locked) + ">重置</button></div></form>";
  }

  function render(state) {
    var busyText = state.refreshingContacts ? "正在刷新切换后的微信通讯录..." :
      state.loading ? "正在加载项目配置..." :
      state.contactsLoading ? "正在加载群聊联系人..." : "";
    return '<section class="recruitingSources">' +
      '<header class="recruitingSources__topbar"><div><h2>招聘来源组</h2><p>权威职位订单来源与组织身份。</p></div>' +
      '<div class="recruitingSources__topbarActions"><button type="button" data-source-action="reload"' +
      disabled(state.loading || state.contactsLoading || state.refreshingContacts) +
      '>重新加载</button><button class="primary" type="button" data-source-action="refresh-contacts"' +
      disabled(state.loading || state.contactsLoading || state.refreshingContacts) + ">" +
      (state.refreshingContacts ? "正在刷新通讯录..." : "切换微信账号后刷新通讯录") +
      "</button></div></header>" +
      Workbench.renderStatus(state.error, state.success, busyText) +
      '<section class="recruitingSources__organization" aria-label="招聘组织配置">' +
      '<div><h3>组织身份</h3><span>项目版本 ' + escapeHTML(state.project.version) + "</span></div>" +
      '<form data-source-form="organization"><label>组织名称<input name="organization_name" value="' +
      escapeHTML(state.organizationDraft) + '" maxlength="160" autocomplete="organization"' +
      disabled(state.savingOrganization || state.loading || !Number(state.project.version)) +
      '></label><button class="primary" type="submit"' +
      disabled(state.savingOrganization || state.loading || !Number(state.project.version)) +
      ">" + (state.savingOrganization ? "保存中..." : "保存名称") +
      "</button></form></section>" +
      '<section class="recruitingSources__workspace" aria-label="招聘来源组配置">' +
      '<div class="recruitingSources__panel"><div class="recruitingSources__panelHead"><h3>已配置来源</h3><span>' +
      escapeHTML(projectSources(state).length) + " 个</span></div>" + renderSourceRows(state) + "</div>" +
      '<div class="recruitingSources__panel">' + renderSourceForm(state) + "</div></section></section>";
  }

  function bind(root, state) {
    var organizationForm = root.querySelector('[data-source-form="organization"]');
    if (organizationForm) {
      organizationForm.addEventListener("submit", function (event) {
        event.preventDefault();
        saveOrganization(state, organizationForm);
      });
      organizationForm.elements.organization_name.addEventListener("input", function () {
        state.organizationDraft = organizationForm.elements.organization_name.value;
      });
    }

    var sourceFormElement = root.querySelector('[data-source-form="source"]');
    if (sourceFormElement) {
      sourceFormElement.addEventListener("submit", function (event) {
        event.preventDefault();
        saveSource(state, sourceFormElement);
      });
      sourceFormElement.elements.display_name.addEventListener("input", function () {
        state.form.display_name = sourceFormElement.elements.display_name.value;
      });
      sourceFormElement.elements.enabled.addEventListener("change", function () {
        state.form.enabled = sourceFormElement.elements.enabled.checked;
      });
      sourceFormElement.elements.gate_enabled.addEventListener("change", function () {
        state.form.gate_enabled = sourceFormElement.elements.gate_enabled.checked;
        Workbench.requestRender();
      });
      sourceFormElement.elements.gate_mode.addEventListener("change", function () {
        state.form.gate_mode = sourceFormElement.elements.gate_mode.value;
      });
      if (sourceFormElement.elements.group_id) {
        sourceFormElement.elements.group_id.addEventListener("input", function () {
          state.form.group_id = sourceFormElement.elements.group_id.value;
        });
      }
      sourceFormElement.elements.contact_group_id.addEventListener("change", function () {
        var groupID = sourceFormElement.elements.contact_group_id.value;
        var contact = contactByGroupID(state, groupID);
        state.form.group_id = groupID;
        if (contact && !state.form.display_name.trim()) {
          state.form.display_name = groupDisplayName(contact);
        }
        Workbench.requestRender();
      });
    }

    Array.prototype.forEach.call(root.querySelectorAll("[data-source-action]"), function (button) {
      button.addEventListener("click", function () {
        var action = button.getAttribute("data-source-action");
        var sourceID = button.getAttribute("data-source-id") || "";
        if (action === "reload") {
          reloadData(state);
        } else if (action === "refresh-contacts") {
          refreshContactsForAccount(state);
        } else if (action === "edit") {
          editSource(state, sourceID);
        } else if (action === "toggle") {
          toggleSource(state, sourceID);
        } else if (action === "remove") {
          removeSource(state, sourceID);
        } else if (action === "new" || action === "reset") {
          state.form = emptySourceForm();
          state.error = "";
          state.success = "";
          Workbench.requestRender();
        }
      });
    });
  }

  function onEnter(state) {
    if (state.entered) {
      return null;
    }
    state.entered = true;
    return Promise.all([
      loadProject(state, false, false),
      loadContacts(state)
    ]);
  }

  Workbench.registerView({
    id: "recruiting-sources",
    label: "招聘来源组",
    section: "联系人",
    order: 129,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  });
})();
