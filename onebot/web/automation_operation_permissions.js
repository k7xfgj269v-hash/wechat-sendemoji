(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.automationAPI !== "function") {
    return;
  }

  var PATH = "/api/automation/operation-permissions";
  var TOOL_LABELS = {
    "workbench.query": "查询(只读 · R0 自动放行)",
    "workbench.mutate": "写入(可逆 · R1)",
    "workbench.allow_once": "高影响(含发送 · R3 人工复核)"
  };
  var TOOL_ORDER = ["workbench.query", "workbench.mutate", "workbench.allow_once"];

  function createState() {
    return {
      view: null,
      denied: {},
      serverDenied: {},
      loading: false,
      saving: false,
      error: "",
      success: ""
    };
  }

  function opKey(tool, operation) {
    return tool + "|" + operation;
  }

  function applyView(state, view) {
    state.view = view;
    state.denied = {};
    state.serverDenied = {};
    (view && view.operations ? view.operations : []).forEach(function (op) {
      if (op.implemented && op.denied) {
        var key = opKey(op.tool, op.operation);
        state.denied[key] = true;
        state.serverDenied[key] = true;
      }
    });
  }

  function isDirty(state) {
    var keys = {};
    Object.keys(state.denied).forEach(function (k) { keys[k] = true; });
    Object.keys(state.serverDenied).forEach(function (k) { keys[k] = true; });
    return Object.keys(keys).some(function (k) {
      return !!state.denied[k] !== !!state.serverDenied[k];
    });
  }

  function load(state, message) {
    state.loading = true;
    state.error = "";
    state.success = message || "";
    Workbench.requestRender();
    return Workbench.automationAPI(PATH).then(function (view) {
      applyView(state, view);
    }).catch(function (err) {
      state.error = err && err.message ? err.message : String(err);
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function save(state) {
    if (state.saving || state.loading || !state.view) {
      return;
    }
    var denied = Object.keys(state.denied).filter(function (k) {
      return state.denied[k];
    }).map(function (k) {
      var parts = k.split("|");
      return { tool: parts[0], operation: parts[1] };
    });
    state.saving = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    Workbench.automationAPI(PATH, {
      method: "PUT",
      body: { expected_version: state.view.version, denied: denied }
    }).then(function (view) {
      applyView(state, view);
      state.success = "已保存,权限即时生效";
    }).catch(function (err) {
      state.error = err && err.message ? err.message : String(err);
    }).finally(function () {
      state.saving = false;
      Workbench.requestRender();
    });
  }

  function renderOperation(state, op) {
    var key = opKey(op.tool, op.operation);
    var allowed = !state.denied[key];
    var info = '<div class="opPerm-opInfo"><code>' + Workbench.escapeHTML(op.operation) + '</code>' +
      '<span class="opPerm-opLabel">' + Workbench.escapeHTML(op.label || "") + '</span>' +
      '<span class="opPerm-risk" data-risk="' + Workbench.escapeHTML(op.risk) + '">' +
      Workbench.escapeHTML(op.risk) + '</span></div>';
    if (!op.implemented) {
      return '<div class="opPerm-op" data-implemented="false">' + info +
        '<span class="opPerm-unimpl" title="派发器未实现此操作,不能开启">未实现</span></div>';
    }
    var disabled = state.saving || state.loading ? " disabled" : "";
    return '<label class="opPerm-op" data-implemented="true" data-allowed="' + (allowed ? "true" : "false") + '">' +
      info + '<span class="opPerm-toggle"><input type="checkbox" data-op-toggle="' +
      Workbench.escapeHTML(key) + '"' + (allowed ? " checked" : "") + disabled +
      '><span>' + (allowed ? "放行" : "禁止") + '</span></span></label>';
  }

  function renderGroup(state, tool, ops) {
    if (!ops.length) {
      return "";
    }
    return '<section class="opPerm-group"><div class="opPerm-groupHead"><h3>' +
      Workbench.escapeHTML(TOOL_LABELS[tool] || tool) + '</h3><code>' +
      Workbench.escapeHTML(tool) + '</code></div>' +
      ops.map(function (op) { return renderOperation(state, op); }).join("") + '</section>';
  }

  function render(state) {
    var view = state.view;
    var ops = view && Array.isArray(view.operations) ? view.operations : [];
    var grouped = {};
    TOOL_ORDER.forEach(function (t) { grouped[t] = []; });
    ops.forEach(function (op) {
      (grouped[op.tool] || (grouped[op.tool] = [])).push(op);
    });
    var implementedCount = ops.filter(function (op) { return op.implemented; }).length;
    var deniedCount = Object.keys(state.denied).filter(function (k) { return state.denied[k]; }).length;
    var dirty = isDirty(state);
    var body = TOOL_ORDER.map(function (t) { return renderGroup(state, t, grouped[t]); }).join("");

    return '<div class="opPerm"><section class="topbar opPerm-topbar"><div>' +
      '<h2>操作权限</h2><p>控制 AI 自动化可执行的操作(放行/禁止)。仅已实现的操作可切换,未实现的不能开启。</p></div>' +
      '<div class="opPerm-actions"><button type="button" data-op-refresh' +
      (state.loading || state.saving ? " disabled" : "") + '>' +
      (state.loading ? "刷新中..." : "刷新") + '</button>' +
      '<button type="button" class="opPerm-save" data-op-save' +
      (!dirty || state.saving || state.loading ? " disabled" : "") + '>' +
      (state.saving ? "保存中..." : "保存") + '</button></div></section>' +
      Workbench.renderStatus(state.error, state.success, state.loading ? "正在读取操作权限..." : "") +
      '<section class="opPerm-summary"><div><span>已实现</span><strong>' +
      Workbench.escapeHTML(implementedCount) + '</strong></div><div><span>当前禁止</span><strong>' +
      Workbench.escapeHTML(deniedCount) + '</strong></div><div><span>配置版本</span><strong>' +
      Workbench.escapeHTML(view ? view.version : "-") + '</strong></div></section>' +
      body + '</div>';
  }

  function bind(root, state) {
    var refresh = root.querySelector("[data-op-refresh]");
    if (refresh) {
      refresh.addEventListener("click", function () { load(state); });
    }
    var saveBtn = root.querySelector("[data-op-save]");
    if (saveBtn) {
      saveBtn.addEventListener("click", function () { save(state); });
    }
    Array.prototype.forEach.call(root.querySelectorAll("[data-op-toggle]"), function (input) {
      input.addEventListener("change", function () {
        var key = input.getAttribute("data-op-toggle");
        if (input.checked) {
          delete state.denied[key];
        } else {
          state.denied[key] = true;
        }
        Workbench.requestRender();
      });
    });
  }

  Workbench.registerView({
    id: "automation-operation-permissions",
    label: "操作权限",
    section: "AI 中心",
    order: 110,
    createState: createState,
    render: render,
    onEnter: function (state) {
      return load(state);
    },
    bind: bind
  });
})();
