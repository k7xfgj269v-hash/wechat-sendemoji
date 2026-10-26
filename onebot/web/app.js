(function () {
  "use strict";

  var PAGE_SIZE = 50;
  var CONTACT_BATCH_SIZE = 1000;
  var CONTACT_REFRESH_TIMEOUT_MS = 50000;
  var AUTOMATION_TOKEN_KEY = "onebot.automation.operator_token";
  var app = document.getElementById("app");
  var views = Object.create(null);
  var viewStates = Object.create(null);
  var pendingViewIDs = Object.create(null);
  var pendingViewOrders = Object.create(null);
  var renderQueued = false;
  var bootstrapped = false;
  var enteredView = "";
  var enteredDefinition = null;
  var workbenchAPI = null;
  var contactRefreshPromise = null;
  var contactDataEpoch = 0;
  var contactDataStale = {};
  var navigationSections = ["招聘交付", "驻场履约", "AI 中心", "联系人", "系统"];
  var viewDefinitionFields = ["id", "label", "section", "order", "createState", "render", "onEnter", "bind"];
  var optionalViewDefinitionFields = ["navigation"];

  var state = {
    view: "inbox",
    navSection: "招聘交付",
    contacts: {
      keyword: "",
      localType: "",
      page: 0,
      total: 0,
      items: [],
      selected: "",
      detail: null,
      loading: false,
      detailLoading: false,
      refreshing: false,
      error: "",
      success: ""
    },
    groups: {
      items: [],
      selected: "",
      members: [],
      selectedMember: "",
      memberDetail: null,
      loading: false,
      membersLoading: false,
      detailLoading: false,
      error: "",
      success: ""
    },
    rules: {
      items: [],
      loading: false,
      saving: false,
      deleting: "",
      toggling: "",
      error: "",
      success: "",
      editingId: "",
      form: {
        group_id: "",
        keywords: "",
        cooldown_seconds: 0,
        enabled: true
      }
    }
  };

  function escapeHTML(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function encodeQuery(params) {
    var query = new URLSearchParams();
    Object.keys(params).forEach(function (key) {
      var value = params[key];
      if (value !== "" && value != null) {
        query.set(key, value);
      }
    });
    var text = query.toString();
    return text ? "?" + text : "";
  }

  function displayName(item) {
    if (!item) {
      return "未知";
    }
    return item.display_name || item.remark || item.nick_name || item.alias || item.username || "未命名";
  }

  function isChatroomContact(item) {
    return !!(item && item.username && item.username.endsWith("@chatroom"));
  }

  function initials(item) {
    var name = displayName(item).trim();
    if (!name) {
      return "?";
    }
    var compact = name.replace(/\s+/g, "");
    return compact.slice(0, 2).toUpperCase();
  }

  function avatar(item, label) {
    var name = escapeHTML(displayName(item));
    var url = item && (item.head_img_url || item.big_head_url || item.small_head_url);
    var image = url ? '<img src="' + escapeHTML(url) + '" alt="" loading="lazy">' : "";
    var ariaLabel = escapeHTML(label || (displayName(item) + "的头像"));
    return '<span class="avatar" role="img" aria-label="' + ariaLabel + '">' + image + '<span class="avatar-fallback">' + escapeHTML(initials(item)) + "</span></span>";
  }

  function setBusyText(isBusy, text, fallback) {
    return isBusy ? text : fallback;
  }

  function api(path, options) {
    options = options || {};
    var headers = new Headers(options.headers || {});
    var init = {
      method: options.method || "GET",
      headers: headers
    };
    if (options.signal) {
      init.signal = options.signal;
    }
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
      init.body = JSON.stringify(options.body);
    }
    return fetch(path, init).then(function (response) {
      if (response.status === 204) {
        if (!response.ok) {
          throw new Error(response.status + " " + response.statusText);
        }
        return null;
      }
      return response.text().then(function (text) {
        var data = null;
        if (text) {
          try {
            data = JSON.parse(text);
          } catch (err) {
            data = text;
          }
        }
        if (!response.ok) {
          var message = response.status + " " + response.statusText;
          if (data && typeof data === "object" && data.error) {
            message = data.error;
          } else if (typeof data === "string" && data.trim()) {
            message = data;
          }
          var requestError = new Error(message);
          requestError.status = response.status;
          requestError.payload = data;
          throw requestError;
        }
        return data;
      });
    });
  }

  function apiWithTimeout(path, options, timeoutMs, timeoutMessage) {
    var controller = new AbortController();
    var timer;
    var requestOptions = Object.assign({}, options || {}, {
      signal: controller.signal
    });
    var timeout = new Promise(function (_, reject) {
      timer = window.setTimeout(function () {
        reject(new Error(timeoutMessage));
        controller.abort();
      }, timeoutMs);
    });
    return Promise.race([
      api(path, requestOptions),
      timeout
    ]).finally(function () {
      window.clearTimeout(timer);
    });
  }

  function automationFetch(path, options) {
    var target = new URL(path, window.location.href);
    if (target.origin !== window.location.origin) {
      return Promise.reject(new TypeError("automation API path must be same-origin"));
    }

    var callerOptions = options || {};
    var init = Object.assign({}, callerOptions);
    var headers = new Headers(callerOptions.headers || {});
    var token = window.sessionStorage.getItem(AUTOMATION_TOKEN_KEY);
    if (!token) {
      token = window.prompt("请输入 AI 自动化操作员令牌");
      if (!token) {
        var tokenError = new Error("需要 AI 自动化操作员令牌");
        tokenError.status = 0;
        tokenError.code = "operator_token_required";
        return Promise.reject(tokenError);
      }
      window.sessionStorage.setItem(AUTOMATION_TOKEN_KEY, token);
    }

    headers.set("Authorization", "Bearer " + token);
    init.headers = headers;
    return fetch(target.pathname + target.search, init).then(function (response) {
      if (response.status === 401) {
        window.sessionStorage.removeItem(AUTOMATION_TOKEN_KEY);
      }
      return response;
    });
  }

  function automationAPI(path, options) {
    var target = new URL(path, window.location.href);
    if (target.origin !== window.location.origin) {
      return Promise.reject(new TypeError("automation API path must be same-origin"));
    }

    var callerOptions = options || {};
    var init = Object.assign({}, callerOptions);
    var headers = new Headers(callerOptions.headers || {});
    var tokenKey = "onebot.automation.operator_token";
    var token = window.sessionStorage.getItem(tokenKey);
    if (!token) {
      token = window.prompt("请输入 AI 自动化操作员令牌");
      if (!token) {
        var tokenError = new Error("需要 AI 自动化操作员令牌");
        tokenError.status = 0;
        tokenError.code = "operator_token_required";
        return Promise.reject(tokenError);
      }
      window.sessionStorage.setItem(tokenKey, token);
    }

    headers.set("Authorization", "Bearer " + token);
    init.headers = headers;
    if (callerOptions.body !== undefined) {
      if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
      init.body = JSON.stringify(callerOptions.body);
    }

    return fetch(target.pathname + target.search, init).then(function (response) {
      if (response.status === 401) {
        window.sessionStorage.removeItem(tokenKey);
      }
      if (response.status === 204) {
        if (!response.ok) {
          var emptyError = new Error(response.status + " " + response.statusText);
          emptyError.status = response.status;
          emptyError.code = "";
          throw emptyError;
        }
        return null;
      }
      return response.text().then(function (text) {
        var data = null;
        if (text) {
          try {
            data = JSON.parse(text);
          } catch (err) {
            data = text;
          }
        }
        if (!response.ok) {
          var message = response.status + " " + response.statusText;
          if (data && typeof data === "object" && data.error) {
            message = data.error;
          } else if (typeof data === "string" && data.trim()) {
            message = data;
          }
          var requestError = new Error(message);
          requestError.status = response.status;
          requestError.code = data && typeof data === "object" && data.code ? data.code : "";
          throw requestError;
        }
        return data;
      });
    });
  }

  function navigationItems() {
    return Object.keys(views).map(function (id) {
      var definition = views[id];
      return {
        id: definition.id,
        label: definition.label,
        order: definition.order,
        section: definition.section,
        ready: true
      };
    }).filter(function (item) {
      return views[item.id].navigation;
    }).sort(function (left, right) {
      var sectionDifference = navigationSectionRank(left.section) - navigationSectionRank(right.section);
      return sectionDifference || left.order - right.order || left.id.localeCompare(right.id);
    });
  }

  function navigationSectionRank(section) {
    var rank = navigationSections.indexOf(section);
    return rank === -1 ? navigationSections.length : rank;
  }

  function navCount(id) {
    if (id === "contacts") {
      return state.contacts.total || state.contacts.items.length;
    }
    if (id === "groups") {
      return state.groups.items.length;
    }
    if (id === "rules") {
      return state.rules.items.length;
    }
    return null;
  }

  function navButton(item) {
    var count = navCount(item.id);
    var countHTML = count == null ? "" : '<span class="nav-count">' + escapeHTML(count) + "</span>";
    var active = item.id === state.view;
    return '<button class="nav-button" type="button" data-view="' + item.id + '" data-module-ready="' + item.ready + '" aria-current="' + (active ? "page" : "false") + '">' +
      '<span class="nav-label">' + escapeHTML(item.label) + "</span>" + countHTML + "</button>";
  }

  function renderShell(content) {
    var activeID = escapeHTML(state.view);
    app.innerHTML = '<div class="layout">' +
      '<aside class="sidebar" aria-label="主导航">' +
      '<div class="brand"><h1>工作台</h1><p>招聘与微信运营</p></div>' +
      '<nav class="nav-list">' + renderDesktopNav() + "</nav>" +
      "</aside>" +
      '<header class="mobile-header"><div class="mobile-brand"><strong>工作台</strong><span>招聘与微信运营</span></div>' +
      '<nav class="mobile-tabs" aria-label="主导航">' + renderMobileNav() + "</nav></header>" +
      '<main class="main" tabindex="-1"><div class="view-root" data-view-root="' + activeID + '">' + content + "</div></main>" +
      "</div>";
    bindAvatarFallbacks();
  }

  function renderMobileNav() {
    return navigationItems().map(navButton).join("");
  }

  function renderDesktopNav() {
    var groups = [];
    var groupByName = Object.create(null);
    navigationItems().forEach(function (item) {
      var group = groupByName[item.section];
      if (!group) {
        group = { section: item.section, items: [] };
        groupByName[item.section] = group;
        groups.push(group);
      }
      group.items.push(item);
    });
    return groups.map(function (group) {
      var expanded = state.navSection === group.section;
      return '<section class="nav-group"><button class="nav-group-button" type="button" data-nav-section="' +
        escapeHTML(group.section) + '" aria-expanded="' + expanded + '"><span>' +
        escapeHTML(group.section) + '</span><span class="nav-group-indicator" aria-hidden="true">' +
        (expanded ? "-" : "+") + '</span></button><div class="nav-group-items"' +
        (expanded ? "" : " hidden") + ">" + group.items.map(navButton).join("") + "</div></section>";
    }).join("");
  }

  function navigationSection(viewID) {
    var definition = views[viewID];
    return definition ? definition.section : "";
  }

  function renderStatus(error, success, busyText) {
    var html = "";
    if (busyText) {
      html += '<div class="status" aria-live="polite">' + escapeHTML(busyText) + "</div>";
    }
    if (success) {
      html += '<div class="notice success" role="status">' + escapeHTML(success) + "</div>";
    }
    if (error) {
      html += '<div class="notice error" role="alert">' + escapeHTML(error) + "</div>";
    }
    return html;
  }

  function validateViewDefinitionShape(definition) {
    var fields = Object.keys(definition);
    var allowedFields = viewDefinitionFields.concat(optionalViewDefinitionFields);
    var exact = viewDefinitionFields.every(function (field) {
      return Object.prototype.hasOwnProperty.call(definition, field);
    }) && fields.every(function (field) {
      return allowedFields.indexOf(field) !== -1;
    });
    if (!exact) {
      throw new TypeError("view definition must contain: " + viewDefinitionFields.join(", "));
    }
  }

  function registerView(definition) {
    if (!definition || typeof definition !== "object" || Array.isArray(definition)) {
      throw new TypeError("view definition is required");
    }
    validateViewDefinitionShape(definition);
    var id = String(definition.id || "").trim();
    if (!/^[a-z][a-z0-9_-]*$/.test(id)) {
      throw new TypeError("view id must be a lowercase identifier");
    }
    if (views[id] || pendingViewIDs[id]) {
      throw new Error("view already registered: " + id);
    }
    if (typeof definition.render !== "function") {
      throw new TypeError("view render must be a function");
    }
    if (typeof definition.createState !== "function") {
      throw new TypeError("view createState must be a function");
    }
    if (definition.onEnter !== null && typeof definition.onEnter !== "function") {
      throw new TypeError("view onEnter must be a function or null");
    }
    if (definition.bind !== null && typeof definition.bind !== "function") {
      throw new TypeError("view bind must be a function or null");
    }
    if (definition.navigation !== undefined && typeof definition.navigation !== "boolean") {
      throw new TypeError("view navigation must be a boolean");
    }
    var label = String(definition.label || "").trim();
    if (!label) {
      throw new TypeError("view label is required");
    }
    var order = Number(definition.order);
    if (!Number.isFinite(order)) {
      throw new TypeError("view order must be finite");
    }
    var section = String(definition.section || "").trim();
    if (navigationSections.indexOf(section) === -1) {
      throw new TypeError("unknown view section: " + section);
    }
    var orderKey = String(order);
    var pendingOrders = pendingViewOrders[section];
    var duplicateOrder = (pendingOrders &&
      Object.prototype.hasOwnProperty.call(pendingOrders, orderKey)) ||
      Object.keys(views).some(function (existingID) {
        return views[existingID].section === section && views[existingID].order === order;
      });
    if (duplicateOrder) {
      throw new Error("view order already registered in section " + section + ": " + order);
    }
    if (!pendingOrders) {
      pendingOrders = Object.create(null);
      pendingViewOrders[section] = pendingOrders;
    }
    pendingViewIDs[id] = true;
    pendingOrders[orderKey] = true;
    try {
      var viewState = definition.createState();
      if (!viewState || typeof viewState !== "object" || Array.isArray(viewState)) {
        throw new TypeError("view state must be an object");
      }
      views[id] = {
        id: id,
        label: label,
        section: section,
        order: order,
        navigation: definition.navigation !== false,
        createState: definition.createState,
        render: definition.render,
        onEnter: definition.onEnter,
        bind: definition.bind
      };
      viewStates[id] = viewState;
    } finally {
      delete pendingViewIDs[id];
      delete pendingOrders[orderKey];
      if (Object.keys(pendingOrders).length === 0) {
        delete pendingViewOrders[section];
      }
    }
    requestRender();
  }

  function requestRender() {
    if (!bootstrapped || renderQueued) {
      return;
    }
    renderQueued = true;
    Promise.resolve().then(function () {
      renderQueued = false;
      render();
    });
  }

  function navigate(id) {
    var target = String(id || "").trim();
    if (!views[target]) {
      return false;
    }
    state.view = target;
    state.navSection = navigationSection(target) || state.navSection;
    render();
    return true;
  }

  function on(eventName, selector, handler) {
    if (typeof eventName !== "string" || typeof selector !== "string" || typeof handler !== "function") {
      throw new TypeError("on requires an event name, selector, and handler");
    }
    var listener = function (event) {
      var target = event.target && event.target.closest ? event.target.closest(selector) : null;
      if (!target || !app.contains(target)) {
        return;
      }
      handler(event, target, viewStates[state.view] || null, workbenchAPI);
    };
    app.addEventListener(eventName, listener);
    return function () {
      app.removeEventListener(eventName, listener);
    };
  }

  function renderContacts() {
    var s = state.contacts;
    var totalPages = Math.max(1, Math.ceil(s.total / PAGE_SIZE));
    var start = s.total === 0 ? 0 : s.page * PAGE_SIZE + 1;
    var end = Math.min(s.total, (s.page + 1) * PAGE_SIZE);
    var content = '<section class="topbar">' +
      '<div><h2>直接联系人</h2><p>检索一对一微信联系人；群聊及其成员单独归入“群聊联系人”。</p></div>' +
      '<button class="primary" type="button" data-action="refresh-contacts" aria-label="刷新联系人缓存" ' + disabled(s.refreshing || s.loading) + ">" + setBusyText(s.refreshing, "刷新中...", "刷新缓存") + "</button>" +
      "</section>" +
      '<section class="panel-grid" aria-label="直接联系人工作区">' +
      '<div class="panel"><div class="panel-header"><h3>直接联系人目录</h3><span class="status" aria-live="polite">' + escapeHTML("匹配 " + s.total + " 个") + "</span></div>" +
      '<div class="panel-body">' +
      '<form class="toolbar" data-form="contact-search">' +
      '<label>关键词<input name="keyword" value="' + escapeHTML(s.keyword) + '" autocomplete="off" placeholder="昵称、备注、别名、wxid" aria-label="直接联系人搜索关键词" ' + disabled(s.loading) + "></label>" +
      '<label>本地类型<select name="local_type" aria-label="联系人本地类型" ' + disabled(s.loading) + ">" + localTypeOptions(s.localType) + "</select></label>" +
      '<button class="primary" type="submit" ' + disabled(s.loading) + ">" + setBusyText(s.loading, "搜索中...", "搜索") + "</button>" +
      "</form>" +
      renderStatus(s.error, s.success, s.loading ? "正在加载直接联系人..." : "") +
      renderContactList(s.items, s.selected, s.loading) +
      '<div class="pager"><span>显示 ' + start + "-" + end + " / 共 " + s.total + " 个</span>" +
      '<div class="split-actions"><button type="button" data-action="prev-contacts" aria-label="上一页联系人" ' + disabled(s.loading || s.page === 0) + ">上一页</button>" +
      '<button type="button" data-action="next-contacts" aria-label="下一页联系人" ' + disabled(s.loading || s.page >= totalPages - 1) + ">下一页</button></div></div>" +
      "</div></div>" +
      '<div class="panel"><div class="panel-header"><h3>直接联系人详情</h3></div><div class="panel-body">' + renderContactDetail(s.detail, s.detailLoading, s.selected) + "</div></div>" +
      "</section>";
    return content;
  }

  function localTypeOptions(selected) {
    var options = [
      ["", "全部"],
      ["1", "1"],
      ["2", "2"],
      ["3", "3"],
      ["4", "4"]
    ];
    return options.map(function (item) {
      return '<option value="' + item[0] + '"' + (String(selected) === item[0] ? " selected" : "") + ">" + item[1] + "</option>";
    }).join("");
  }

  function renderContactList(items, selected, loading) {
    if (loading && !items.length) {
      return '<div class="notice">正在从本地缓存加载直接联系人...</div>';
    }
    if (!items.length) {
      return '<div class="notice">当前筛选条件下没有直接联系人。</div>';
    }
    return '<div class="list" role="list">' + items.map(function (item) {
      return '<button class="row-button" type="button" role="listitem" data-contact="' + escapeHTML(item.username) + '" aria-label="查看联系人' + escapeHTML(displayName(item)) + '详情" aria-current="' + (selected === item.username ? "true" : "false") + '">' +
        avatar(item, displayName(item) + "的联系人头像") + '<span class="row-main"><span class="row-title">' + escapeHTML(displayName(item)) + '</span><span class="row-meta">' + escapeHTML(item.username || "") + "</span></span>" +
        '<span class="row-badge">类型 ' + escapeHTML(item.local_type) + "</span></button>";
    }).join("") + "</div>";
  }

  function renderContactDetail(item, loading, selected) {
    if (loading) {
      return '<div class="notice">正在加载联系人详情...</div>';
    }
    if (!selected) {
      return '<div class="notice">选择一个联系人后查看资料字段和头像数据。</div>';
    }
    if (!item) {
      return '<div class="notice">当前联系人还没有加载详情。</div>';
    }
    return '<article class="detail-card">' +
      '<div class="detail-head">' + avatar(item, displayName(item) + "的资料头像") + '<div><h3>' + escapeHTML(displayName(item)) + "</h3><p>" + escapeHTML(item.username || "") + "</p></div></div>" +
      '<dl class="kv">' +
      kv("备注", item.remark) + kv("昵称", item.nick_name) + kv("别名", item.alias) + kv("本地类型", item.local_type) +
      kv("是否在群聊", item.is_in_chat_room) + kv("描述", item.description) + kv("头像地址", item.head_img_url) +
      "</dl></article>";
  }

  function kv(label, value) {
    var shown = value === 0 || value ? value : "无";
    return "<dt>" + escapeHTML(label) + "</dt><dd>" + escapeHTML(shown) + "</dd>";
  }

  function renderGroups() {
    var s = state.groups;
    var refreshing = state.contacts.refreshing;
    var selectedGroup = s.items.find(function (group) { return group.username === s.selected; });
    var content = '<section class="topbar">' +
      '<div><h2>群聊联系人</h2><p>按群聊查看成员数量、联系人列表和成员资料。</p></div>' +
      '<button class="primary" type="button" data-action="reload-groups" aria-label="切换微信账号后刷新联系人和群聊" ' +
      disabled(s.loading || refreshing) + ">" +
      setBusyText(refreshing, "刷新中...", "切换微信后刷新") + "</button>" +
      "</section>" +
      '<section class="panel-grid" aria-label="群聊联系人工作区">' +
      '<div class="panel"><div class="panel-header"><h3>群聊目录</h3><span class="status" aria-live="polite">' + escapeHTML(s.items.length + " 个群") + "</span></div>" +
      '<div class="panel-body">' + renderStatus(s.error, s.success, s.loading ? "正在加载群聊..." : "") + renderGroupList(s.items, s.selected, s.loading) + "</div></div>" +
      '<div class="panel"><div class="panel-header"><h3>群成员</h3><span class="status">' + escapeHTML(selectedGroup ? displayName(selectedGroup) : "未选择群聊") + "</span></div>" +
      '<div class="panel-body">' + renderMembersPane(s, selectedGroup) + "</div></div>" +
      "</section>";
    return content;
  }

  function renderGroupList(items, selected, loading) {
    if (loading && !items.length) {
      return '<div class="notice">正在从本地缓存加载群聊...</div>';
    }
    if (!items.length) {
      return '<div class="notice">缓存里暂无可用群聊。</div>';
    }
    return '<div class="list" role="list">' + items.map(function (item) {
      return '<button class="row-button" type="button" role="listitem" data-group="' + escapeHTML(item.username) + '" aria-label="查看群聊' + escapeHTML(displayName(item)) + '成员" aria-current="' + (selected === item.username ? "true" : "false") + '">' +
        avatar(item, displayName(item) + "的群聊头像") + '<span class="row-main"><span class="row-title">' + escapeHTML(displayName(item)) + '</span><span class="row-meta">' + escapeHTML(item.username || "") + "</span></span>" +
        '<span class="row-badge">' + escapeHTML(item.member_count || 0) + "</span></button>";
    }).join("") + "</div>";
  }

  function renderMembersPane(s, selectedGroup) {
    if (!selectedGroup) {
      return '<div class="notice">选择一个群聊后加载成员列表。</div>';
    }
    var memberList = "";
    if (s.membersLoading) {
      memberList = '<div class="notice">正在加载群成员...</div>';
    } else if (!s.members.length) {
      memberList = '<div class="notice">这个群聊暂无缓存成员。</div>';
    } else {
      memberList = '<div class="list" role="list">' + s.members.map(function (item) {
        return '<button class="row-button" type="button" role="listitem" data-member="' + escapeHTML(item.username) + '" aria-label="查看群成员' + escapeHTML(displayName(item)) + '详情" aria-current="' + (s.selectedMember === item.username ? "true" : "false") + '">' +
          avatar(item, displayName(item) + "的群成员头像") + '<span class="row-main"><span class="row-title">' + escapeHTML(displayName(item)) + '</span><span class="row-meta">' + escapeHTML(item.username || "") + "</span></span>" +
          '<span class="row-badge">类型 ' + escapeHTML(item.local_type) + "</span></button>";
      }).join("") + "</div>";
    }
    return '<div class="detail-card">' +
      '<div class="detail-head">' + avatar(selectedGroup, displayName(selectedGroup) + "的群聊头像") + '<div><h3>' + escapeHTML(displayName(selectedGroup)) + "</h3><p>" + escapeHTML(selectedGroup.username) + "</p></div></div>" +
      '<dl class="kv">' + kv("群主", selectedGroup.owner) + kv("成员数", selectedGroup.member_count || 0) + kv("备注", selectedGroup.remark) + "</dl>" +
      memberList +
      '<section class="subsection" aria-label="群成员详情"><div class="subsection-header"><h3>成员详情</h3></div><div class="subsection-body">' + renderContactDetail(s.memberDetail, s.detailLoading, s.selectedMember) + "</div></section>" +
      "</div>";
  }

  function renderRules() {
    var s = state.rules;
    var content = '<section class="topbar">' +
      '<div><h2>转发规则</h2><p>为入站群消息配置内存过滤规则，命中后再投递到当前 sink。</p></div>' +
      '<button class="primary" type="button" data-action="reload-rules" aria-label="刷新转发规则" ' + disabled(s.loading) + ">" + setBusyText(s.loading, "加载中...", "刷新规则") + "</button>" +
      "</section>" +
      '<section class="rules-layout" aria-label="转发规则工作区">' +
      '<div class="panel"><div class="panel-header"><h3>规则列表</h3><span class="status" aria-live="polite">' + escapeHTML(s.items.length + " 条规则") + "</span></div>" +
      '<div class="panel-body">' + renderStatus(s.error, s.success, s.loading ? "正在加载规则..." : "") + renderRuleList(s) + "</div></div>" +
      '<div class="panel"><div class="panel-header"><h3>' + (s.editingId ? "编辑规则" : "新建规则") + '</h3></div><div class="panel-body">' + renderRuleForm(s) + "</div></div>" +
      "</section>";
    return content;
  }

  function renderRuleList(s) {
    if (s.loading && !s.items.length) {
      return '<div class="notice">正在加载转发规则...</div>';
    }
    if (!s.items.length) {
      return '<div class="notice">尚未配置规则。没有规则时，会保留原有转发行为。</div>';
    }
    return '<div class="list" role="list">' + s.items.map(function (rule) {
      var keywords = rule.keywords && rule.keywords.length ? rule.keywords : ["任意关键词"];
      return '<article class="rule-card" role="listitem">' +
        '<div class="rule-top"><strong>' + escapeHTML(rule.group_id || "全部群聊") + '</strong><span class="chip ' + (rule.enabled ? "enabled" : "disabled") + '">' + (rule.enabled ? "已启用" : "已停用") + "</span></div>" +
        '<div class="chips">' + keywords.map(function (keyword) { return '<span class="chip">' + escapeHTML(keyword) + "</span>"; }).join("") + "</div>" +
        '<div class="row-meta">冷却时间：' + escapeHTML(rule.cooldown_seconds || 0) + " 秒</div>" +
        '<div class="actions">' +
        '<button type="button" data-action="edit-rule" data-rule="' + escapeHTML(rule.id) + '" aria-label="编辑规则' + escapeHTML(rule.id) + '" ' + disabled(s.saving) + ">编辑</button>" +
        '<button type="button" data-action="toggle-rule" data-rule="' + escapeHTML(rule.id) + '" aria-label="' + (rule.enabled ? "停用" : "启用") + '规则' + escapeHTML(rule.id) + '" ' + disabled(s.toggling === rule.id || s.saving) + ">" + (s.toggling === rule.id ? "保存中..." : rule.enabled ? "停用" : "启用") + "</button>" +
        '<button class="danger" type="button" data-action="delete-rule" data-rule="' + escapeHTML(rule.id) + '" aria-label="删除规则' + escapeHTML(rule.id) + '" ' + disabled(s.deleting === rule.id || s.saving) + ">" + (s.deleting === rule.id ? "删除中..." : "删除") + "</button>" +
        "</div></article>";
    }).join("") + "</div>";
  }

  function renderRuleForm(s) {
    var f = s.form;
    return '<form class="form-grid" data-form="rule">' +
      '<label>群 ID<input name="group_id" value="' + escapeHTML(f.group_id) + '" placeholder="留空匹配全部群聊" aria-label="规则群 ID" ' + disabled(s.saving || s.editingId) + "></label>" +
      '<label>关键词<textarea name="keywords" placeholder="每行一个，或用逗号分隔" aria-label="规则关键词" ' + disabled(s.saving) + ">" + escapeHTML(f.keywords) + "</textarea></label>" +
      '<label>冷却秒数<input name="cooldown_seconds" type="number" min="0" step="1" value="' + escapeHTML(f.cooldown_seconds) + '" aria-label="规则冷却秒数" ' + disabled(s.saving) + "></label>" +
      '<label class="checkbox-label"><input name="enabled" type="checkbox" aria-label="规则是否启用" ' + (f.enabled ? "checked" : "") + " " + disabled(s.saving) + "> 启用</label>" +
      '<div class="form-actions"><button class="primary" type="submit" ' + disabled(s.saving) + ">" + setBusyText(s.saving, "保存中...", s.editingId ? "保存修改" : "创建规则") + "</button>" +
      '<button type="button" data-action="reset-rule-form" ' + disabled(s.saving) + ">重置</button></div>" +
      "</form>";
  }

  function disabled(value) {
    return value ? "disabled" : "";
  }

  function render() {
    var definition = views[state.view] || null;
    var viewState = definition ? viewStates[state.view] : null;
    var content;
    if (!definition) {
      content = '<section class="topbar"><div><h2>工作台</h2><p>模块资源尚未注册。</p></div></section>' +
        '<section class="empty-module" aria-live="polite"><strong>等待本地模块</strong><span>工作台会在资源可用后自动启用此导航。</span></section>';
    } else {
      try {
        content = definition.render(viewState, workbenchAPI);
      } catch (err) {
        console.error("workbench view render failed", err);
        content = '<section class="topbar"><div><h2>' + escapeHTML(definition.label) + '</h2><p>模块渲染失败。</p></div></section>';
      }
    }
    renderShell(content);
    if (definition && definition.bind) {
      try {
        definition.bind(app.querySelector(".view-root"), viewState, workbenchAPI);
      } catch (err) {
        console.error("workbench view bind failed", err);
      }
    }
    if (enteredView !== state.view || enteredDefinition !== definition) {
      enteredView = state.view;
      enteredDefinition = definition;
      if (definition && definition.onEnter) {
        Promise.resolve().then(function () {
          if (state.view === definition.id && views[definition.id] === definition) {
            return definition.onEnter(viewState, workbenchAPI);
          }
          return null;
        }).catch(function (err) {
          console.error("workbench view enter failed", err);
        });
      }
    }
  }

  function bindAvatarFallbacks() {
    Array.prototype.forEach.call(document.querySelectorAll(".avatar img"), function (img) {
      img.addEventListener("error", function () {
        img.hidden = true;
      }, { once: true });
    });
  }

  function loadContacts(propagateError) {
    var s = state.contacts;
    var epoch = contactDataEpoch;
    s.loading = true;
    s.error = "";
    render();
    return loadAllMatchingContacts(0, [], epoch).then(function (items) {
      if (epoch !== contactDataEpoch) {
        return;
      }
      var directContacts = items.filter(function (item) {
        return !isChatroomContact(item);
      });
      s.total = directContacts.length;
      var totalPages = Math.max(1, Math.ceil(s.total / PAGE_SIZE));
      if (s.page >= totalPages) {
        s.page = totalPages - 1;
      }
      var start = s.page * PAGE_SIZE;
      s.items = directContacts.slice(start, start + PAGE_SIZE);
      if (s.selected && !s.items.some(function (item) { return item.username === s.selected; })) {
        s.selected = "";
        s.detail = null;
      }
    }).catch(function (err) {
      if (err === contactDataStale || epoch !== contactDataEpoch) {
        return;
      }
      s.error = err.message;
      s.items = [];
      s.total = 0;
      if (propagateError) {
        throw err;
      }
    }).finally(function () {
      if (epoch === contactDataEpoch) {
        s.loading = false;
        render();
      }
    });
  }

  function loadAllMatchingContacts(offset, collected, epoch) {
    if (epoch !== contactDataEpoch) {
      return Promise.reject(contactDataStale);
    }
    var s = state.contacts;
    return api("/api/contacts" + encodeQuery({
      keyword: s.keyword,
      local_type: s.localType,
      limit: CONTACT_BATCH_SIZE,
      offset: offset
    })).then(function (data) {
      if (epoch !== contactDataEpoch) {
        throw contactDataStale;
      }
      var batch = Array.isArray(data.contacts) ? data.contacts : [];
      var all = collected.concat(batch);
      var total = Number(data.total || 0);
      if (batch.length && offset + batch.length < total) {
        return loadAllMatchingContacts(offset + batch.length, all, epoch);
      }
      return all;
    });
  }

  function loadContactDetail(username) {
    var s = state.contacts;
    var epoch = contactDataEpoch;
    s.selected = username;
    s.detailLoading = true;
    s.error = "";
    render();
    return api("/api/contact_detail" + encodeQuery({ username: username })).then(function (data) {
      if (epoch !== contactDataEpoch || s.selected !== username) {
        return;
      }
      s.detail = data;
    }).catch(function (err) {
      if (epoch !== contactDataEpoch || s.selected !== username) {
        return;
      }
      s.detail = null;
      s.error = err.message;
    }).finally(function () {
      if (epoch === contactDataEpoch && s.selected === username) {
        s.detailLoading = false;
        render();
      }
    });
  }

  function clearContactSelections() {
    state.contacts.selected = "";
    state.contacts.detail = null;
    state.contacts.detailLoading = false;
    state.groups.selected = "";
    state.groups.members = [];
    state.groups.selectedMember = "";
    state.groups.memberDetail = null;
    state.groups.membersLoading = false;
    state.groups.detailLoading = false;
  }

  function refreshContactData() {
    if (contactRefreshPromise) {
      return contactRefreshPromise;
    }
    var refreshEpoch = ++contactDataEpoch;
    state.contacts.loading = false;
    state.contacts.detailLoading = false;
    state.groups.loading = false;
    state.groups.membersLoading = false;
    state.groups.detailLoading = false;
    state.contacts.refreshing = true;
    state.contacts.error = "";
    state.contacts.success = "";
    state.groups.error = "";
    state.groups.success = "";
    render();
    contactRefreshPromise = apiWithTimeout(
      "/api/contacts/refresh",
      { method: "POST" },
      CONTACT_REFRESH_TIMEOUT_MS,
      "刷新联系人缓存超时，请检查 wechat-agent 日志后重试。"
    ).then(function (data) {
      if (refreshEpoch !== contactDataEpoch) {
        throw contactDataStale;
      }
      clearContactSelections();
      state.contacts.items = [];
      state.contacts.total = 0;
      state.groups.items = [];
      render();
      return Promise.all([
        loadContacts(true),
        loadGroups(true)
      ]).then(function () {
        var message = "缓存已刷新：" + Number(data.contacts || 0) + " 个联系人，" +
          Number(data.groups || 0) + " 个群聊。";
        state.contacts.success = message;
        state.groups.success = message;
        return data;
      });
    }).catch(function (err) {
      if (err === contactDataStale || refreshEpoch !== contactDataEpoch) {
        throw err;
      }
      state.contacts.success = "";
      state.groups.success = "";
      state.contacts.error = err.message;
      state.groups.error = err.message;
      throw err;
    }).finally(function () {
      if (refreshEpoch === contactDataEpoch) {
        state.contacts.refreshing = false;
        contactRefreshPromise = null;
        render();
      }
    });
    return contactRefreshPromise;
  }

  function refreshContacts() {
    return refreshContactData().catch(function () {
      return null;
    });
  }

  function loadGroups(propagateError) {
    var s = state.groups;
    var epoch = contactDataEpoch;
    s.loading = true;
    s.error = "";
    render();
    return api("/api/groups").then(function (data) {
      if (epoch !== contactDataEpoch) {
        return;
      }
      s.items = Array.isArray(data.groups) ? data.groups : [];
    }).catch(function (err) {
      if (epoch !== contactDataEpoch) {
        return;
      }
      s.error = err.message;
      s.items = [];
      if (propagateError) {
        throw err;
      }
    }).finally(function () {
      if (epoch === contactDataEpoch) {
        s.loading = false;
        render();
      }
    });
  }

  function loadMembers(groupID) {
    var s = state.groups;
    var epoch = contactDataEpoch;
    s.selected = groupID;
    s.membersLoading = true;
    s.selectedMember = "";
    s.memberDetail = null;
    s.error = "";
    render();
    return api("/api/group_members" + encodeQuery({ group_id: groupID })).then(function (data) {
      if (epoch !== contactDataEpoch || s.selected !== groupID) {
        return;
      }
      s.members = Array.isArray(data.members) ? data.members : [];
    }).catch(function (err) {
      if (epoch !== contactDataEpoch || s.selected !== groupID) {
        return;
      }
      s.members = [];
      s.error = err.message;
    }).finally(function () {
      if (epoch === contactDataEpoch && s.selected === groupID) {
        s.membersLoading = false;
        render();
      }
    });
  }

  function loadMemberDetail(username) {
    var s = state.groups;
    var epoch = contactDataEpoch;
    s.selectedMember = username;
    s.detailLoading = true;
    s.error = "";
    render();
    return api("/api/contact_detail" + encodeQuery({ username: username })).then(function (data) {
      if (epoch !== contactDataEpoch || s.selectedMember !== username) {
        return;
      }
      s.memberDetail = data;
    }).catch(function (err) {
      if (epoch !== contactDataEpoch || s.selectedMember !== username) {
        return;
      }
      s.memberDetail = null;
      s.error = err.message;
    }).finally(function () {
      if (epoch === contactDataEpoch && s.selectedMember === username) {
        s.detailLoading = false;
        render();
      }
    });
  }

  function loadRules() {
    var s = state.rules;
    s.loading = true;
    s.error = "";
    render();
    return api("/api/forward_rules").then(function (data) {
      s.items = Array.isArray(data) ? data : [];
    }).catch(function (err) {
      s.items = [];
      s.error = err.message;
    }).finally(function () {
      s.loading = false;
      render();
    });
  }

  function parseKeywords(value) {
    return value.split(/[\n,]/).map(function (item) {
      return item.trim();
    }).filter(Boolean);
  }

  function resetRuleForm() {
    state.rules.editingId = "";
    state.rules.form = {
      group_id: "",
      keywords: "",
      cooldown_seconds: 0,
      enabled: true
    };
  }

  function saveRule(form) {
    var s = state.rules;
    var cooldown = Number(form.cooldown_seconds.value || 0);
    if (!Number.isFinite(cooldown) || cooldown < 0) {
      s.error = "冷却秒数必须大于或等于 0。";
      render();
      return;
    }
    var body = {
      group_id: form.group_id.value.trim(),
      keywords: parseKeywords(form.keywords.value),
      cooldown_seconds: Math.floor(cooldown),
      enabled: form.enabled.checked
    };
    s.saving = true;
    s.error = "";
    s.success = "";
    render();
    var request;
    if (s.editingId) {
      delete body.group_id;
      request = api("/api/forward_rules" + encodeQuery({ id: s.editingId }), {
        method: "PUT",
        body: body
      });
    } else {
      request = api("/api/forward_rules", {
        method: "POST",
        body: body
      });
    }
    return request.then(function () {
      s.success = s.editingId ? "规则已更新。" : "规则已创建。";
      resetRuleForm();
      return loadRules();
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.saving = false;
      render();
    });
  }

  function editRule(id) {
    var rule = state.rules.items.find(function (item) { return item.id === id; });
    if (!rule) {
      return;
    }
    state.rules.editingId = id;
    state.rules.form = {
      group_id: rule.group_id || "",
      keywords: (rule.keywords || []).join("\n"),
      cooldown_seconds: rule.cooldown_seconds || 0,
      enabled: !!rule.enabled
    };
    state.rules.error = "";
    state.rules.success = "";
    render();
  }

  function toggleRule(id) {
    var s = state.rules;
    var rule = s.items.find(function (item) { return item.id === id; });
    if (!rule) {
      return;
    }
    s.toggling = id;
    s.error = "";
    s.success = "";
    render();
    return api("/api/forward_rules" + encodeQuery({ id: id }), {
      method: "PUT",
      body: { enabled: !rule.enabled }
    }).then(function () {
      s.success = !rule.enabled ? "规则已启用。" : "规则已停用。";
      return loadRules();
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.toggling = "";
      render();
    });
  }

  function deleteRule(id) {
    if (!window.confirm("确认删除这条转发规则？")) {
      return;
    }
    var s = state.rules;
    s.deleting = id;
    s.error = "";
    s.success = "";
    render();
    return api("/api/forward_rules" + encodeQuery({ id: id }), {
      method: "DELETE"
    }).then(function () {
      s.success = "规则已删除。";
      if (s.editingId === id) {
        resetRuleForm();
      }
      return loadRules();
    }).catch(function (err) {
      s.error = err.message;
    }).finally(function () {
      s.deleting = "";
      render();
    });
  }

  function isLocalModuleAsset(assetPath, extension) {
    if (typeof assetPath !== "string" || !assetPath.startsWith("/assets/") || !assetPath.endsWith(extension)) {
      return false;
    }
    var basename = assetPath.slice("/assets/".length);
    return basename !== "" && basename.indexOf("/") === -1 && basename.indexOf("\\") === -1;
  }

  function loadStyleAsset(href) {
    return new Promise(function (resolve, reject) {
      var link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = href;
      link.setAttribute("data-workbench-module", "style");
      link.addEventListener("load", resolve, { once: true });
      link.addEventListener("error", function () {
        reject(new Error("failed to load module style"));
      }, { once: true });
      document.head.appendChild(link);
    });
  }

  function loadScriptAsset(src) {
    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.src = src;
      script.async = false;
      script.setAttribute("data-workbench-module", "script");
      script.addEventListener("load", resolve, { once: true });
      script.addEventListener("error", function () {
        reject(new Error("failed to load module script"));
      }, { once: true });
      document.head.appendChild(script);
    });
  }

  function loadWorkbenchModules() {
    return api("/workbench/modules.json").then(function (manifest) {
      var styles = manifest && Array.isArray(manifest.styles) ? manifest.styles.filter(function (assetPath) {
        return isLocalModuleAsset(assetPath, ".css");
      }).slice().sort() : [];
      var scripts = manifest && Array.isArray(manifest.scripts) ? manifest.scripts.filter(function (assetPath) {
        return isLocalModuleAsset(assetPath, ".js");
      }).slice().sort() : [];
      return Promise.all(styles.map(loadStyleAsset)).then(function () {
        return scripts.reduce(function (sequence, src) {
          return sequence.then(function () {
            return loadScriptAsset(src);
          });
        }, Promise.resolve());
      });
    }).then(function () {
      requestRender();
    }).catch(function (err) {
      console.error("workbench modules failed to load", err);
    });
  }

  on("click", "button", function (event, target) {
    var navSection = target.getAttribute("data-nav-section");
    if (navSection) {
      state.navSection = navSection;
      render();
      return;
    }
    var view = target.getAttribute("data-view");
    if (view) {
      navigate(view);
      return;
    }
    var contact = target.getAttribute("data-contact");
    if (contact) {
      loadContactDetail(contact);
      return;
    }
    var group = target.getAttribute("data-group");
    if (group) {
      loadMembers(group);
      return;
    }
    var member = target.getAttribute("data-member");
    if (member) {
      loadMemberDetail(member);
      return;
    }
    var action = target.getAttribute("data-action");
    var ruleID = target.getAttribute("data-rule");
    if (action === "prev-contacts" && state.contacts.page > 0) {
      state.contacts.page -= 1;
      loadContacts();
    } else if (action === "next-contacts") {
      state.contacts.page += 1;
      loadContacts();
    } else if (action === "refresh-contacts") {
      refreshContacts();
    } else if (action === "reload-groups") {
      refreshContacts();
    } else if (action === "reload-rules") {
      loadRules();
    } else if (action === "reset-rule-form") {
      resetRuleForm();
      render();
    } else if (action === "edit-rule") {
      editRule(ruleID);
    } else if (action === "toggle-rule") {
      toggleRule(ruleID);
    } else if (action === "delete-rule") {
      deleteRule(ruleID);
    }
  });

  on("submit", "form[data-form]", function (event, form) {
    var kind = form.getAttribute("data-form");
    if (kind === "contact-search") {
      event.preventDefault();
      state.contacts.keyword = form.keyword.value.trim();
      state.contacts.localType = form.local_type.value;
      state.contacts.page = 0;
      state.contacts.success = "";
      loadContacts();
    } else if (kind === "rule") {
      event.preventDefault();
      saveRule(form);
    }
  });

  workbenchAPI = Object.freeze({
    registerView: registerView,
    api: api,
    automationFetch: automationFetch,
    automationAPI: automationAPI,
    escapeHTML: escapeHTML,
    encodeQuery: encodeQuery,
    renderStatus: renderStatus,
    requestRender: requestRender,
    navigate: navigate,
    refreshContactData: refreshContactData,
    on: on
  });
  window.OneBotWorkbench = workbenchAPI;

  registerView({
    id: "contacts",
    label: "直接联系人",
    section: "联系人",
    order: 110,
    createState: function () { return state.contacts; },
    render: renderContacts,
    onEnter: function (viewState) {
      if (!viewState.items.length && !viewState.loading) {
        return loadContacts();
      }
      return null;
    },
    bind: null
  });
  registerView({
    id: "groups",
    label: "群聊联系人",
    section: "联系人",
    order: 120,
    createState: function () { return state.groups; },
    render: renderGroups,
    onEnter: function (viewState) {
      if (!viewState.items.length && !viewState.loading) {
        return loadGroups();
      }
      return null;
    },
    bind: null
  });
  registerView({
    id: "rules",
    label: "转发规则",
    section: "联系人",
    order: 130,
    createState: function () { return state.rules; },
    render: renderRules,
    onEnter: function (viewState) {
      if (!viewState.items.length && !viewState.loading) {
        return loadRules();
      }
      return null;
    },
    bind: null
  });

  bootstrapped = true;
  render();
  loadGroups();
  loadWorkbenchModules();
})();
