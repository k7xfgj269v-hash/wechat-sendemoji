package main

import (
	"strings"
	"testing"
)

func TestWebFridaControlRegistrationAndAPIContract(t *testing.T) {
	script := readEmbeddedWebAsset(t, "web/frida_control.js")
	for _, marker := range []string{
		"var Workbench = window.OneBotWorkbench;",
		`id: "frida-control"`,
		`label: "Hook 初始化"`,
		"order: 140",
		"createState: fridaControlCreateState",
		"render: fridaControlRender",
		"onEnter:",
		"bind: fridaControlBind",
		`var FRIDA_STATUS_PATH = "/api/frida/status";`,
		`var FRIDA_INITIALIZE_PATH = "/api/frida/initialize";`,
		`var FRIDA_RESCAN_PATH = "/api/frida/rescan";`,
		`var FRIDA_DETACH_PATH = "/api/frida/detach";`,
		`"X-OneBot-Workbench": "1"`,
		"Workbench.automationAPI(path, requestOptions)",
		"new AbortController()",
		"signal: controller.signal",
		"FRIDA_STATUS_TIMEOUT_MS",
		"FRIDA_ACTION_TIMEOUT_MS",
		"Promise.race",
		"fridaControlBusyText",
		"lastOperation",
		"state.loading || state.action",
		"err.payload && err.payload.status",
		`type: state.form.type`,
		`gadget_addr:`,
		`wechat_pid:`,
		`data-frida-action="rescan"`,
		`data-frida-action="detach"`,
		`status.attached || status.auto_reconnect || status.state === "reconnecting"`,
		`"重新扫描并附加"`,
		`"Detach"`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("frida_control.js does not contain %q", marker)
		}
	}
	if strings.Contains(script, "fetch(") || strings.Contains(script, "XMLHttpRequest") {
		t.Fatal("frida_control.js must use the workbench API")
	}
}

func TestWebFridaControlPreservesFailureStatusAndPreventsDuplicateActions(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/frida_control.js",
		`
const views = {};
const calls = [];
let rejectAction;
global.window = {
  setTimeout,
  clearTimeout,
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    api(path, options) {
      calls.push({ path, options: options || {} });
      if (path === "/api/frida/status") {
        return Promise.resolve({
          state: "detached",
          type: "local",
          attached: false,
          auto_reconnect: false
        });
      }
      return new Promise((resolve, reject) => { rejectAction = reject; });
    },
    automationAPI(path, options) {
      calls.push({ path, options: options || {} });
      return new Promise((resolve, reject) => { rejectAction = reject; });
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus(error, success, busy) {
      return [error, success, busy].filter(Boolean).join("|");
    },
    requestRender() {}
  }
};
`,
		`
(async () => {
  const view = views["frida-control"];
  const state = view.createState();
  state.loaded = true;
  state.status = {
    state: "attached",
    type: "gadget",
    attached: true,
    auto_reconnect: true
  };
  const button = {
    getAttribute(name) {
      return name === "data-frida-action" ? "rescan" : "";
    },
    addEventListener(name, handler) {
      if (name === "click") this.click = handler;
    }
  };
  view.bind({
    querySelector() { return null; },
    querySelectorAll() { return [button]; }
  }, state);

  button.click();
  button.click();
  if (state.action !== "rescan" ||
      !view.render(state).includes("正在重新扫描并附加 Frida...")) {
    throw new Error("rescan progress was not visible");
  }
  const error = new Error("扫描 Frida Gadget 进程失败");
  error.payload = {
    status: {
      state: "error",
      type: "gadget",
      gadget_addr: "127.0.0.1:27042",
      attached: false,
      auto_reconnect: false,
      last_error: "扫描 Frida Gadget 进程失败"
    }
  };
  rejectAction(error);
  await new Promise((resolve) => setTimeout(resolve, 0));

  if (calls.length !== 1 ||
      calls[0].path !== "/api/frida/rescan" ||
      !calls[0].options.signal) {
    throw new Error("duplicate Frida actions were dispatched");
  }
  if (state.error !== "扫描 Frida Gadget 进程失败" ||
      state.status.state !== "error" ||
      state.status.auto_reconnect !== false ||
      state.action !== "" ||
      state.success !== "" ||
      !state.lastOperation.includes("重新扫描并附加失败")) {
    throw new Error("Frida failure state was lost");
  }
  if (!view.render(state).includes("重试附加")) {
    throw new Error("retry affordance was not visible after failure");
  }
  for (const [action, marker] of [
    ["initialize", "正在初始化并附加 Frida..."],
    ["retry", "正在重试 Frida 附加..."],
    ["rescan", "正在重新扫描并附加 Frida..."],
    ["detach", "正在 Detach Frida 会话..."]
  ]) {
    state.action = action;
    if (!view.render(state).includes(marker)) {
      throw new Error(action + " progress was not visible");
    }
  }
  state.action = "";

  const element = (value) => ({
    value,
    addEventListener() {}
  });
  const form = {
    elements: {
      type: element("gadget"),
      gadget_addr: element("127.0.0.1:27042"),
      wechat_pid: element("")
    },
    addEventListener(name, handler) {
      if (name === "submit") this.submit = handler;
    }
  };
  view.bind({
    querySelector() { return form; },
    querySelectorAll() { return []; }
  }, state);
  form.submit({ preventDefault() {} });
  if (calls.length !== 2 ||
      calls[1].path !== "/api/frida/initialize" ||
      state.action !== "retry" ||
      !view.render(state).includes("正在重试 Frida 附加...")) {
    throw new Error("failed status did not enter the retry flow");
  }
  rejectAction(error);
  await new Promise((resolve) => setTimeout(resolve, 0));

  await view.onEnter(state);
  if (calls.length !== 3 || calls[2].path !== "/api/frida/status") {
    throw new Error("re-entering the Hook view did not refresh status");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebFridaControlReportsStatusAndActionTimeouts(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/frida_control.js",
		`
const views = {};
const calls = [];
const delays = [];
global.window = {
  setTimeout(callback, delay) {
    delays.push(delay);
    queueMicrotask(callback);
    return delays.length;
  },
  clearTimeout() {},
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    api(path, options) {
      calls.push({ path, options: options || {} });
      return new Promise(() => {});
    },
    automationAPI(path, options) {
      calls.push({ path, options: options || {} });
      return new Promise(() => {});
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus(error, success, busy) {
      return [error, success, busy].filter(Boolean).join("|");
    },
    requestRender() {}
  }
};
`,
		`
(async () => {
  const view = views["frida-control"];
  const state = view.createState();
  await view.onEnter(state);
  if (delays[0] !== 10000 ||
      state.error !== "读取 Frida 状态超时，请检查 wechat-agent 是否在线。" ||
      state.loading ||
      !state.lastOperation.includes("读取 Frida 状态失败")) {
    throw new Error("status timeout was not surfaced");
  }

  const button = {
    getAttribute(name) {
      return name === "data-frida-action" ? "rescan" : "";
    },
    addEventListener(name, handler) {
      if (name === "click") this.click = handler;
    }
  };
  view.bind({
    querySelector() { return null; },
    querySelectorAll() { return [button]; }
  }, state);
  button.click();
  await new Promise((resolve) => setImmediate(resolve));

  if (delays[1] !== 55000 ||
      state.error !== "重新扫描并附加超时，请检查 wechat-agent 日志后重试。" ||
      state.action !== "" ||
      !state.lastOperation.includes("重新扫描并附加失败") ||
      !view.render(state).includes("最近操作")) {
    throw new Error("action timeout was not surfaced");
  }
  if (!calls[0].options.signal.aborted || !calls[1].options.signal.aborted) {
    throw new Error("timed out requests were not aborted");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebFridaControlScopedResponsiveCSS(t *testing.T) {
	style := readEmbeddedWebAsset(t, "web/frida_control.css")
	for _, marker := range []string{
		".fridaControl {",
		".fridaControl .fridaControl-layout",
		"grid-template-columns: minmax(300px, 0.8fr) minmax(420px, 1.2fr)",
		".fridaControl .fridaControl-statusList",
		".fridaControl .fridaControl-actions",
		`@media (max-width: 430px)`,
		"grid-template-columns: minmax(0, 1fr)",
		"overflow-wrap: anywhere",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("frida_control.css does not contain %q", marker)
		}
	}
	lower := strings.ToLower(style)
	for _, forbidden := range []string{
		"http://",
		"https://",
		"@import",
		"url(",
		"linear-gradient",
		"radial-gradient",
		"conic-gradient",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("frida_control.css contains forbidden marker %q", forbidden)
		}
	}
}

func TestWebFridaControlRegisteredNavigationAndCustomHeaders(t *testing.T) {
	appJS := readEmbeddedWebAsset(t, "web/app.js")
	fridaJS := readEmbeddedWebAsset(t, "web/frida_control.js")
	for _, marker := range []string{
		`id: "frida-control"`,
		`label: "Hook 初始化"`,
		`section: "系统"`,
		"order: 140",
		"Workbench.registerView({",
	} {
		if !strings.Contains(fridaJS, marker) {
			t.Fatalf("frida_control.js does not contain %q", marker)
		}
	}
	for _, marker := range []string{
		"var headers = new Headers(options.headers || {});",
		`headers.set("Content-Type", "application/json");`,
		"return Object.keys(views).map(function (id)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("app.js does not contain %q", marker)
		}
	}
	if strings.Contains(appJS, `{ id: "frida-control"`) {
		t.Fatal("app.js must not duplicate the Frida control view registration")
	}
}
