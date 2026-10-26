package main

import (
	"strings"
	"testing"
)

func TestWebAutomationSupervisionDependencyAndResultContract(t *testing.T) {
	script := readEmbeddedWebAsset(t, "web/automation_supervision.js")
	for _, marker := range []string{
		`id: "ai-supervision"`,
		`label: "实时监督"`,
		"Workbench.automationAPI",
		"Workbench.automationFetch",
		"Workbench.escapeHTML",
		"depends_on_action_ids",
		"action.scheduler",
		"scheduler.not_ready_reason",
		"action.result_entity",
		`BASE_PATH + "/actions/"`,
		"traceDependencyLabel",
		"mergeTurnEvents",
		"pendingTraceCalls",
		"projectedActionStatus",
		"CHAT_TRACE_RELOAD_DELAY_MS",
		"readSSEStream",
		"scheduleSSERecovery",
		"permission_requests",
		"results",
		`class="automationSupervision-dependencies"`,
		`class="automationSupervision-traceStages"`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("automation_supervision.js missing marker %q", marker)
		}
	}
	if strings.Count(script, "readSSEStream(") < 3 {
		t.Fatal("runtime and chat streams do not share the SSE reader")
	}
	if strings.Count(script, "scheduleSSERecovery(") < 3 {
		t.Fatal("runtime and chat streams do not share reconnect backoff")
	}
	for _, forbidden := range []string{"localStorage", "document.cookie", "https://", "http://"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("automation_supervision.js contains forbidden marker %q", forbidden)
		}
	}
	for _, inferred := range []string{
		"action.dependency_state",
		"action.execution_state",
		"action.blocked_reason",
		"action.ready === true",
		"result && result.data",
		"action.result ||",
	} {
		if strings.Contains(script, inferred) {
			t.Fatalf("automation_supervision.js derives projection state with %q", inferred)
		}
	}
}

func TestWebAutomationSupervisionRendersProjectedTrace(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI() { return Promise.resolve({}); },
    automationFetch() { return Promise.resolve({}); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
`,
		`
const view = views["ai-chat"];
const state = view.createState();
state.chatSession = { id: "session_1", title: "Projection", version: 1 };
state.chatSessions = [state.chatSession];
state.chatTrace = {
  turns: [],
  permission_requests: [],
  results: [],
  actions: [{
    id: "action_1",
    tool: "job.update",
    status: "succeeded",
    dependency_state: "ready",
    blocked_reason: "legacy blocked reason",
    scheduler: {
      state: "blocked",
      not_ready_reason: "authoritative supervision reason"
    },
    result: { entity_id: "legacy_result" },
    result_entity: { entity_type: "job", entity_id: "job_supervision_projection" },
    payload: {},
    depends_on_action_ids: [],
    version: 1
  }]
};
const html = view.render(state);
for (const expected of [
  "依赖阻塞",
  "authoritative supervision reason",
  "job_supervision_projection"
]) {
  if (!html.includes(expected)) throw new Error("missing supervision projection " + expected);
}
for (const forbidden of ["legacy blocked reason", "legacy_result"]) {
  if (html.includes(forbidden)) throw new Error("rendered inferred supervision field " + forbidden);
}
`,
	)
}

func TestWebAutomationSupervisionRendersLiveToolCallProjection(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI() { return Promise.resolve({}); },
    automationFetch() { return Promise.resolve({}); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
`,
		`
const view = views["ai-chat"];
const state = view.createState();
state.chatSession = { id: "session_1", title: "Live trace", version: 1 };
state.chatSessions = [state.chatSession];
state.chatTurnID = "turn_1";
state.chatTrace = {
  turns: [{ id: "turn_1", user_text: "更新职位", status: "running" }],
  permission_requests: [],
  results: [],
  actions: []
};
state.chatTurnEvents = [
  {
    sequence: 3,
    turn_id: "turn_1",
    type: "tool.call.proposed",
    data: {
      action_id: "action_live",
      provider_call_id: "call_live",
      tool: "workbench.mutate",
      status: "approved",
      risk: "R1"
    }
  },
  {
    sequence: 1,
    turn_id: "turn_1",
    type: "tool.call.delta",
    data: {
      call_id: "call_live",
      tool: "workbench.mutate",
      arguments_fragment: "{\"operation\":\"jobs."
    }
  },
  {
    sequence: 2,
    turn_id: "turn_1",
    type: "tool.call.delta",
    data: {
      call_id: "call_live",
      arguments_fragment: "update\"}"
    }
  },
  {
    sequence: 2,
    turn_id: "turn_1",
    type: "tool.call.delta",
    data: {
      call_id: "call_live",
      arguments_fragment: "duplicate"
    }
  }
];
const html = view.render(state);
for (const expected of [
  "workbench.mutate",
  "action_live",
  "生成参数",
  "2 个片段",
  "调用已提案",
  "jobs.update"
]) {
  if (!html.includes(expected)) throw new Error("missing live trace projection " + expected);
}
if (html.includes("duplicate")) throw new Error("duplicate sequence was rendered");
`,
	)
}

func TestWebAutomationSupervisionDefersAuthoritativeTraceReload(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
const timers = [];
const apiCalls = [];
const session = { id: "session_1", title: "SSE trace", version: 1 };
let traceLoads = 0;
let streamAborts = 0;
const initialTrace = {
  session,
  turns: [{ id: "turn_1", session_id: "session_1", user_text: "执行", status: "running", version: 1 }],
  turn_events: [],
  runs: [],
  actions: [],
  permission_requests: [],
  results: [],
  audit_events: []
};
const reloadedTrace = {
  session: { ...session, version: 2 },
  turns: [{ id: "turn_1", session_id: "session_1", user_text: "执行", status: "completed", version: 2 }],
  turn_events: [{
    sequence: 2,
    turn_id: "turn_1",
    type: "tool.call.proposed",
    data: {
      action_id: "action_1",
      provider_call_id: "call_1",
      tool: "workbench.query",
      status: "approved",
      risk: "R0"
    }
  }],
  runs: [],
  actions: [{
    id: "action_1",
    provider_call_id: "call_1",
    tool: "workbench.query",
    status: "approved",
    version: 1,
    payload: {},
    depends_on_action_ids: []
  }],
  permission_requests: [],
  results: [],
  audit_events: []
};
const projectedAction = {
  ...reloadedTrace.actions[0],
  scheduler: { state: "ready", not_ready_reason: "" },
  result_entity: { entity_type: "candidate", entity_id: "candidate_1" }
};
const streamText = [
  "data: " + JSON.stringify({
    sequence: 1,
    turn_id: "turn_1",
    type: "tool.call.delta",
    data: {
      call_id: "call_1",
      tool: "workbench.query",
      arguments_fragment: "{\"operation\":\"candidates.get\"}"
    }
  }),
  "",
  "data: " + JSON.stringify(reloadedTrace.turn_events[0]),
  "",
  ""
].join("\n");
global.window = {
  setTimeout(callback) {
    timers.push(callback);
    return timers.length;
  },
  clearTimeout() {},
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path) {
      apiCalls.push(path);
      if (path.startsWith("/api/automation/chat/sessions?")) {
        return Promise.resolve({ sessions: [session] });
      }
      if (path === "/api/automation/chat/sessions/session_1") {
        traceLoads++;
        return Promise.resolve(traceLoads === 1 ? initialTrace : reloadedTrace);
      }
      if (path === "/api/automation/actions/action_1") {
        return Promise.resolve({ action: projectedAction });
      }
      return Promise.reject(new Error("unexpected API path " + path));
    },
    automationFetch() {
      let reads = 0;
      return Promise.resolve({
        ok: true,
        body: {
          getReader() {
            return {
              read() {
                reads++;
                if (reads === 1) {
                  return Promise.resolve({
                    done: false,
                    value: new TextEncoder().encode(streamText)
                  });
                }
                return new Promise(() => {});
              },
              releaseLock() {}
            };
          }
        }
      });
    },
    encodeQuery(values) {
      const query = new URLSearchParams();
      Object.keys(values).forEach((key) => {
        if (values[key] !== "") query.set(key, values[key]);
      });
      return query.toString() ? "?" + query.toString() : "";
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() {
    this.signal.aborted = true;
    streamAborts++;
  }
};
`,
		`
(async () => {
  const view = views["ai-chat"];
  const state = view.createState();
  await view.onEnter(state);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (traceLoads !== 1) throw new Error("SSE caused an immediate authoritative reload");
  if (timers.length !== 1) throw new Error("trace reload was not queued once");
  const liveHTML = view.render(state);
  for (const expected of ["workbench.query", "生成参数", "调用已提案", "candidates.get"]) {
    if (!liveHTML.includes(expected)) throw new Error("missing immediate SSE trace " + expected);
  }

  timers.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (traceLoads !== 2) throw new Error("queued trace reload did not run exactly once");
  if (state.chatTurnID !== "" || state.chatTurnController !== null || streamAborts !== 1) {
    throw new Error("terminal trace did not close the active stream");
  }
  const settledHTML = view.render(state);
  for (const expected of ["candidate_1", "生成参数", "调用已提案"]) {
    if (!settledHTML.includes(expected)) throw new Error("trace merge lost " + expected);
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationSupervisionIgnoresStaleSessionTrace(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
const listeners = {};
const pending = {};
const clearedTimers = [];
function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}
pending.session_2 = deferred();
pending.session_3 = deferred();
global.window = {
  setTimeout() { return 99; },
  clearTimeout(timer) { clearedTimers.push(timer); },
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path) {
      const id = path.split("/").pop();
      if (pending[id]) return pending[id].promise;
      return Promise.reject(new Error("unexpected API path " + path));
    },
    automationFetch() { return Promise.reject(new Error("stream should not connect")); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
const root = {
  addEventListener(type, listener) { listeners[type] = listener; }
};
function sessionTrace(id) {
  return {
    session: { id, title: id, version: 1 },
    turns: [],
    turn_events: [],
    runs: [],
    actions: [],
    permission_requests: [],
    results: [],
    audit_events: []
  };
}
`,
		`
(async () => {
  const view = views["ai-chat"];
  const state = view.createState();
  state.chatSession = { id: "session_1", title: "old", version: 1 };
  state.chatSessions = [
    state.chatSession,
    { id: "session_2", title: "middle", version: 1 },
    { id: "session_3", title: "latest", version: 1 }
  ];
  state.chatTrace = sessionTrace("session_1");
  state.chatTurnID = "turn_old";
  state.chatTurnEvents = [{
    sequence: 1,
    turn_id: "turn_old",
    type: "tool.call.delta",
    data: { call_id: "old_call", arguments_fragment: "old" }
  }];
  state.chatTraceReloadTimer = 17;
  state.chatTraceReloadQueued = true;
  view.bind(root, state);

  const target = (value) => ({
    value,
    matches(selector) { return selector === "[data-chat-session]"; }
  });
  listeners.change({ target: target("session_2") });
  listeners.change({ target: target("session_3") });

  pending.session_3.resolve(sessionTrace("session_3"));
  await new Promise((resolve) => setImmediate(resolve));
  pending.session_2.resolve(sessionTrace("session_2"));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (!state.chatSession || state.chatSession.id !== "session_3") {
    throw new Error("stale session trace replaced the latest selection");
  }
  if (state.chatTurnEvents.length !== 0 || state.chatTurnID !== "") {
    throw new Error("old session events survived the switch");
  }
  if (clearedTimers.length !== 1 || clearedTimers[0] !== 17) {
    throw new Error("old trace reload timer was not cancelled");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationSupervisionNewSessionClearsOldTransport(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
const listeners = {};
const clearedTimers = [];
let streamAborts = 0;
const newSession = { id: "session_new", title: "新对话", version: 1 };
function sessionTrace(session) {
  return {
    session,
    turns: [],
    turn_events: [],
    runs: [],
    actions: [],
    permission_requests: [],
    results: [],
    audit_events: []
  };
}
global.window = {
  setTimeout() { return 99; },
  clearTimeout(timer) { clearedTimers.push(timer); },
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path, options) {
      if (path === "/api/automation/chat/sessions" && options && options.method === "POST") {
        return Promise.resolve({ session: newSession });
      }
      if (path === "/api/automation/chat/sessions" || path.startsWith("/api/automation/chat/sessions?")) {
        return Promise.resolve({ sessions: [newSession] });
      }
      if (path === "/api/automation/chat/sessions/session_new") {
        return Promise.resolve(sessionTrace(newSession));
      }
      return Promise.reject(new Error("unexpected API path " + path));
    },
    automationFetch() { return Promise.reject(new Error("stream should not connect")); },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
const root = {
  addEventListener(type, listener) { listeners[type] = listener; }
};
`,
		`
(async () => {
  const view = views["ai-chat"];
  const state = view.createState();
  state.chatSession = { id: "session_old", title: "旧对话", version: 1 };
  state.chatSessions = [state.chatSession];
  state.chatTrace = {
    session: state.chatSession,
    turns: [{ id: "turn_old", status: "running" }],
    turn_events: [],
    actions: [],
    permission_requests: [],
    results: []
  };
  state.chatTurnController = {
    signal: { aborted: false },
    abort() {
      this.signal.aborted = true;
      streamAborts++;
    }
  };
  state.chatTurnID = "turn_old";
  state.chatTurnEvents = [{
    sequence: 1,
    turn_id: "turn_old",
    type: "tool.call.delta",
    data: { call_id: "old_call", arguments_fragment: "old" }
  }];
  state.chatStreamRetryTimer = 18;
  state.chatTraceReloadTimer = 17;
  state.chatTraceReloadQueued = true;
  view.bind(root, state);

  const action = {
    getAttribute(name) {
      return name === "data-chat-action" ? "new-session" : "";
    }
  };
  listeners.click({
    target: {
      closest(selector) {
        return selector === "[data-chat-action]" ? action : null;
      }
    }
  });
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (!state.chatSession || state.chatSession.id !== "session_new") {
    throw new Error("new session was not selected");
  }
  if (streamAborts !== 1 || state.chatTurnController !== null || state.chatTurnID !== "") {
    throw new Error("old session stream was not closed");
  }
  if (state.chatTurnEvents.length !== 0 || state.chatStreamText !== "") {
    throw new Error("old session events survived new-session creation");
  }
  if (!clearedTimers.includes(17) || !clearedTimers.includes(18)) {
    throw new Error("old session timers were not cancelled");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationSupervisionRecoversUnexpectedTurnStreamEOF(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
const timers = [];
const session = { id: "session_1", title: "Recover", version: 1 };
let traceLoads = 0;
let streamConnections = 0;
const trace = {
  session,
  turns: [{ id: "turn_1", session_id: "session_1", user_text: "继续", status: "running", version: 1 }],
  turn_events: [],
  runs: [],
  actions: [],
  permission_requests: [],
  results: [],
  audit_events: []
};
global.window = {
  setTimeout(callback, delay) {
    timers.push({ callback, delay });
    return timers.length;
  },
  clearTimeout() {},
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path) {
      if (path === "/api/automation/chat/sessions" || path.startsWith("/api/automation/chat/sessions?")) {
        return Promise.resolve({ sessions: [session] });
      }
      if (path === "/api/automation/chat/sessions/session_1") {
        traceLoads++;
        return Promise.resolve(trace);
      }
      return Promise.reject(new Error("unexpected API path " + path));
    },
    automationFetch() {
      streamConnections++;
      const connection = streamConnections;
      return Promise.resolve({
        ok: true,
        body: {
          getReader() {
            return {
              read() {
                if (connection === 1) {
                  return Promise.resolve({ done: true });
                }
                return new Promise(() => {});
              },
              releaseLock() {}
            };
          }
        }
      });
    },
    encodeQuery() { return ""; },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
`,
		`
(async () => {
  const view = views["ai-chat"];
  const state = view.createState();
  await view.onEnter(state);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (streamConnections !== 1 || timers.length !== 1) {
    throw new Error(
      "unexpected EOF did not schedule one recovery: connections=" + streamConnections +
      " timers=" + timers.length + " turn=" + state.chatTurnID
    );
  }
  if (timers[0].delay !== 250) {
    throw new Error("first recovery did not use bounded base delay");
  }
  timers.shift().callback();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (traceLoads !== 2 || streamConnections !== 2) {
    throw new Error("recovery did not reload authority and reconnect");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationSupervisionRecoversRuntimeStreamAfterCursor(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/automation_supervision.js",
		`
const views = {};
const timers = [];
const streamPaths = [];
let streamConnections = 0;
global.window = {
  setTimeout(callback, delay) {
    timers.push({ callback, delay });
    return timers.length;
  },
  clearTimeout() {},
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    automationAPI(path) {
      if (path.startsWith("/api/automation/permission_requests")) {
        return Promise.resolve({ permission_requests: [] });
      }
      return Promise.reject(new Error("unexpected API path " + path));
    },
    automationFetch(path) {
      streamPaths.push(path);
      streamConnections++;
      const connection = streamConnections;
      let reads = 0;
      return Promise.resolve({
        ok: true,
        body: {
          getReader() {
            return {
              read() {
                reads++;
                if (connection === 1 && reads === 1) {
                  const event = {
                    sequence: 7,
                    type: "permission.requested",
                    created_at: "2026-08-04T00:00:00Z"
                  };
                  return Promise.resolve({
                    done: false,
                    value: new TextEncoder().encode("data: " + JSON.stringify(event) + "\n\n")
                  });
                }
                if (connection === 1) {
                  return Promise.resolve({ done: true });
                }
                return new Promise(() => {});
              },
              releaseLock() {}
            };
          }
        }
      });
    },
    encodeQuery(values) {
      const query = new URLSearchParams();
      Object.keys(values).forEach((key) => {
        if (values[key] !== "") query.set(key, values[key]);
      });
      return query.toString() ? "?" + query.toString() : "";
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {},
    navigate() { return true; }
  }
};
global.AbortController = class {
  constructor() { this.signal = { aborted: false }; }
  abort() { this.signal.aborted = true; }
};
`,
		`
(async () => {
  const view = views["ai-supervision"];
  const state = view.createState();
  await view.onEnter(state);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (state.lastSequence !== 7 || state.events.length !== 1) {
    throw new Error("runtime cursor or replay state was not retained");
  }
  if (timers.length !== 1 || timers[0].delay !== 250) {
    throw new Error("runtime EOF did not schedule bounded recovery");
  }
  timers.shift().callback();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  if (streamConnections !== 2 || !streamPaths[1].includes("after=7")) {
    throw new Error("runtime stream did not resume after the last cursor: " + streamPaths.join(","));
  }
  if (state.events.length !== 1 || state.lastSequence !== 7) {
    throw new Error("runtime reconnect cleared retained events");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebAutomationSupervisionDependencyResponsiveStyles(t *testing.T) {
	style := readEmbeddedWebAsset(t, "web/automation_supervision.css")
	for _, marker := range []string{
		".automationSupervision",
		".automationSupervision-tone-ready",
		".automationSupervision-tone-review",
		".automationSupervision-tone-blocked",
		".automationSupervision-tone-result",
		".automationSupervision-dependencies",
		".automationSupervision-traceStages",
		".automationSupervision-traceAction-live",
		"overflow-wrap: anywhere",
		"@media (max-width: 560px)",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("automation_supervision.css missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"@import", "url(", "https://", "http://", "gradient("} {
		if strings.Contains(strings.ToLower(style), strings.ToLower(forbidden)) {
			t.Fatalf("automation_supervision.css contains forbidden marker %q", forbidden)
		}
	}
}
