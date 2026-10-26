(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench) {
    return;
  }

  var FRIDA_STATUS_PATH = "/api/frida/status";
  var FRIDA_INITIALIZE_PATH = "/api/frida/initialize";
  var FRIDA_RESCAN_PATH = "/api/frida/rescan";
  var FRIDA_DETACH_PATH = "/api/frida/detach";
  var WORKBENCH_HEADER = { "X-OneBot-Workbench": "1" };
  var FRIDA_STATUS_TIMEOUT_MS = 10000;
  var FRIDA_ACTION_TIMEOUT_MS = 55000;
  var FRIDA_DETACH_TIMEOUT_MS = 15000;

  function fridaControlCreateState() {
    return {
      status: null,
      form: {
        type: "gadget",
        gadgetAddr: "127.0.0.1:27042",
        wechatPID: ""
      },
      loaded: false,
      loading: false,
      action: "",
      lastOperation: "",
      lastOperationAt: "",
      error: "",
      success: ""
    };
  }

  function fridaControlDisabled(value) {
    return value ? " disabled" : "";
  }

  function fridaControlChecked(value) {
    return value ? " checked" : "";
  }

  function fridaControlFormatTime(value) {
    if (!value) {
      return "无";
    }
    var date = new Date(value);
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString("zh-CN", { hour12: false });
  }

  function fridaControlRecordOperation(state, message) {
    state.lastOperation = message;
    state.lastOperationAt = new Date().toISOString();
  }

  function fridaControlActionName(action) {
    var names = {
      initialize: "初始化并附加",
      retry: "重试附加",
      rescan: "重新扫描并附加",
      detach: "Detach"
    };
    return names[action] || "Frida 操作";
  }

  function fridaControlBusyText(state) {
    if (state.loading) {
      return "正在读取 Frida 状态...";
    }
    var messages = {
      initialize: "正在初始化并附加 Frida...",
      retry: "正在重试 Frida 附加...",
      rescan: "正在重新扫描并附加 Frida...",
      detach: "正在 Detach Frida 会话..."
    };
    return messages[state.action] || "";
  }

  function fridaControlTimeoutMessage(action) {
    return fridaControlActionName(action) + "超时，请检查 wechat-agent 日志后重试。";
  }

  function fridaControlStateLabel(status) {
    var labels = {
      detached: "已分离",
      detaching: "正在分离",
      attaching: "正在附加",
      reconnecting: "正在重连",
      attached: "已附加",
      error: "连接失败"
    };
    return labels[String(status && status.state || "")] || "未知";
  }

  function fridaControlSyncForm(state, status) {
    state.form.type = status && status.type === "local" ? "local" : "gadget";
    state.form.gadgetAddr = String(status && status.gadget_addr || state.form.gadgetAddr || "127.0.0.1:27042");
    state.form.wechatPID = status && status.wechat_pid > 0 ? String(status.wechat_pid) : "";
  }

  function fridaControlAPI(path, options, timeoutMs, timeoutMessage, authenticated) {
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
    var request = authenticated ?
      Workbench.automationAPI(path, requestOptions) :
      Workbench.api(path, requestOptions);
    return Promise.race([
      request,
      timeout
    ]).finally(function () {
      window.clearTimeout(timer);
    });
  }

  function fridaControlLoad(state, syncForm) {
    if (state.loading || state.action) {
      return null;
    }
    state.loading = true;
    state.error = "";
    state.success = "";
    fridaControlRecordOperation(state, "正在读取 Frida 状态。");
    Workbench.requestRender();
    return fridaControlAPI(
      FRIDA_STATUS_PATH,
      undefined,
      FRIDA_STATUS_TIMEOUT_MS,
      "读取 Frida 状态超时，请检查 wechat-agent 是否在线。"
    ).then(function (status) {
      state.status = status || {};
      state.loaded = true;
      if (syncForm) {
        fridaControlSyncForm(state, state.status);
      }
      fridaControlRecordOperation(state, "Frida 状态已更新。");
    }).catch(function (err) {
      state.error = err.message;
      fridaControlRecordOperation(state, "读取 Frida 状态失败：" + err.message);
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function fridaControlRequest(state, action, path, body, success) {
    if (state.loading || state.action) {
      return null;
    }
    state.action = action;
    state.error = "";
    state.success = "";
    fridaControlRecordOperation(state, "正在" + fridaControlActionName(action) + "。");
    Workbench.requestRender();
    var options = {
      method: "POST",
      headers: WORKBENCH_HEADER
    };
    if (body !== undefined) {
      options.body = body;
    }
    return fridaControlAPI(
      path,
      options,
      action === "detach" ? FRIDA_DETACH_TIMEOUT_MS : FRIDA_ACTION_TIMEOUT_MS,
      fridaControlTimeoutMessage(action),
      true
    ).then(function (status) {
      state.status = status || {};
      state.loaded = true;
      fridaControlSyncForm(state, state.status);
      state.success = success;
      fridaControlRecordOperation(state, success);
    }).catch(function (err) {
      state.error = err.message;
      fridaControlRecordOperation(state, fridaControlActionName(action) + "失败：" + err.message);
      if (err.payload && err.payload.status) {
        state.status = err.payload.status;
        state.loaded = true;
        fridaControlSyncForm(state, state.status);
      }
    }).finally(function () {
      state.action = "";
      Workbench.requestRender();
    });
  }

  function fridaControlReadForm(state, form) {
    state.form.type = form.elements.type.value === "local" ? "local" : "gadget";
    state.form.gadgetAddr = form.elements.gadget_addr.value.trim();
    state.form.wechatPID = form.elements.wechat_pid.value.trim();
  }

  function fridaControlInitialize(state, form) {
    fridaControlReadForm(state, form);
    var pid = Number(state.form.wechatPID || 0);
    if (!Number.isInteger(pid) || pid < 0) {
      state.error = "微信 PID 必须是非负整数。";
      Workbench.requestRender();
      return;
    }
    var action = state.status && state.status.state === "error" ? "retry" : "initialize";
    fridaControlRequest(state, action, FRIDA_INITIALIZE_PATH, {
      type: state.form.type,
      gadget_addr: state.form.type === "gadget" ? state.form.gadgetAddr : "",
      wechat_pid: state.form.type === "local" ? pid : 0
    }, action === "retry" ? "Frida 附加重试成功。" : "Frida 配置已应用并完成附加。");
  }

  function fridaControlStatusRows(status, state) {
    var rows = [
      ["运行状态", fridaControlStateLabel(status)],
      ["附加类型", status && status.type === "gadget" ? "Frida Gadget" : "本地进程"],
      ["微信 PID", status && status.wechat_pid > 0 ? status.wechat_pid : "未发现"],
      ["Gadget 地址", status && status.gadget_addr || "未配置"],
      ["脚本", status && status.script_loaded ? "已加载" : "未加载"],
      ["自动重连", status && status.auto_reconnect ? "开启" : "关闭"],
      ["更新时间", fridaControlFormatTime(status && status.updated_at)],
      ["最近操作", state.lastOperation || "无"],
      ["操作时间", fridaControlFormatTime(state.lastOperationAt)]
    ];
    return '<dl class="fridaControl-statusList">' + rows.map(function (row) {
      return "<dt>" + Workbench.escapeHTML(row[0]) + "</dt><dd>" + Workbench.escapeHTML(row[1]) + "</dd>";
    }).join("") + "</dl>";
  }

  function fridaControlRender(state) {
    var status = state.status || {};
    var busy = state.loading || state.action !== "";
    var local = state.form.type === "local";
    var canDetach = !!(status.attached || status.auto_reconnect || status.state === "reconnecting");
    var statusTone = status.attached ? "attached" : (status.state === "error" ? "error" : "idle");
    var retry = status.state === "error";
    var actionText = state.action === "initialize" ? "正在初始化..." :
      state.action === "retry" ? "正在重试..." :
      retry ? "重试附加" : "应用配置并附加";
    var content = '<section class="fridaControl">' +
      '<header class="fridaControl-header"><div><h2>Hook 初始化</h2><p>OneBot Frida 会话与微信进程连接状态</p></div>' +
      '<button type="button" data-frida-action="refresh"' + fridaControlDisabled(busy) + '>刷新状态</button></header>' +
      Workbench.renderStatus(state.error, state.success, fridaControlBusyText(state)) +
      '<div class="fridaControl-layout">' +
      '<section class="fridaControl-statusPane"><div class="fridaControl-sectionHeading"><h3>当前会话</h3>' +
      '<span class="fridaControl-badge" data-tone="' + Workbench.escapeHTML(statusTone) + '">' +
      Workbench.escapeHTML(fridaControlStateLabel(status)) + "</span></div>" +
      fridaControlStatusRows(status, state) +
      (status.last_error ? '<div class="fridaControl-lastError" role="alert"><strong>最后错误</strong><span>' +
        Workbench.escapeHTML(status.last_error) + "</span></div>" : "") + "</section>" +
      '<section class="fridaControl-configPane"><div class="fridaControl-sectionHeading"><h3>初始化配置</h3><span>运行时</span></div>' +
      '<form class="fridaControl-form" data-frida-form="initialize">' +
      '<div class="fridaControl-mode" role="radiogroup" aria-label="Frida 附加类型">' +
      '<label><input type="radio" name="type" value="gadget"' + fridaControlChecked(!local) +
      fridaControlDisabled(busy) + '><span>Frida Gadget</span></label>' +
      '<label><input type="radio" name="type" value="local"' + fridaControlChecked(local) +
      fridaControlDisabled(busy) + '><span>本地进程</span></label></div>' +
      '<label>Gadget 地址<input name="gadget_addr" value="' + Workbench.escapeHTML(state.form.gadgetAddr) +
      '" autocomplete="off" placeholder="127.0.0.1:27042"' + fridaControlDisabled(busy || local) + "></label>" +
      '<label>微信 PID<input name="wechat_pid" type="number" min="0" step="1" inputmode="numeric" value="' +
      Workbench.escapeHTML(state.form.wechatPID) + '" placeholder="留空自动扫描"' +
      fridaControlDisabled(busy || !local) + "></label>" +
      '<div class="fridaControl-actions"><button class="fridaControl-primary" type="submit"' +
      fridaControlDisabled(busy) + ">" + actionText + "</button>" +
      '<button type="button" data-frida-action="rescan"' + fridaControlDisabled(busy) + ">" +
      (state.action === "rescan" ? "正在扫描..." : "重新扫描并附加") + "</button>" +
      '<button class="fridaControl-danger" type="button" data-frida-action="detach"' +
      fridaControlDisabled(busy || !canDetach) + ">" +
      (state.action === "detach" ? "正在分离..." : "Detach") + "</button></div></form></section></div></section>";
    return content;
  }

  function fridaControlBind(root, state) {
    var form = root.querySelector('[data-frida-form="initialize"]');
    if (form) {
      form.addEventListener("change", function (event) {
        fridaControlReadForm(state, form);
        if (event.target && event.target.name === "type") {
          Workbench.requestRender();
        }
      });
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        fridaControlInitialize(state, form);
      });
    }
    root.querySelectorAll("[data-frida-action]").forEach(function (button) {
      button.addEventListener("click", function () {
        var action = button.getAttribute("data-frida-action");
        if (action === "refresh") {
          fridaControlLoad(state, true);
        } else if (action === "rescan") {
          fridaControlRequest(state, "rescan", FRIDA_RESCAN_PATH, undefined, "已重新扫描并附加 Frida 会话。");
        } else if (action === "detach") {
          fridaControlRequest(state, "detach", FRIDA_DETACH_PATH, undefined, "Frida 会话已分离，自动重连已关闭。");
        }
      });
    });
  }

  Workbench.registerView({
    id: "frida-control",
    label: "Hook 初始化",
    section: "系统",
    order: 140,
    createState: fridaControlCreateState,
    render: fridaControlRender,
    onEnter: function (state) {
      if (!state.loading && !state.action) {
        return fridaControlLoad(state, true);
      }
      return null;
    },
    bind: fridaControlBind
  });
})();
