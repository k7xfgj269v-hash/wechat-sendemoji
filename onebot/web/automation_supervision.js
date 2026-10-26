(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench) {
    return;
  }

  var BASE_PATH = "/api/automation";
  var EVENT_PATH = BASE_PATH + "/runtime/events";
  var PERMISSION_REQUESTS_PATH = BASE_PATH + "/permission_requests";
  var PERMISSIONS_PATH = BASE_PATH + "/permissions";
  var AUDIT_PATH = BASE_PATH + "/audit";
  var AUDIT_EXPORT_PATH = BASE_PATH + "/audit/export.jsonl";
  var CHAT_PATH = BASE_PATH + "/chat";
  var CHAT_SESSIONS_PATH = CHAT_PATH + "/sessions";
  var CHAT_TURNS_PATH = CHAT_PATH + "/turns";
  var PAGE_SIZE = 100;
  var CHAT_TRACE_RELOAD_DELAY_MS = 80;
  var CHAT_STREAM_RETRY_BASE_MS = 250;
  var CHAT_STREAM_RETRY_MAX_MS = 4000;
  var DECISIONS = [
    ["allow_once", "仅本次允许", "primary"],
    ["allow_run", "本次运行允许", "primary"],
    ["allow_persistent", "持续允许", "primary"],
    ["deny_once", "仅本次拒绝", "danger"],
    ["deny_persistent", "持续拒绝", "danger"]
  ];
  var TABS = [
    ["live", "实时事件"],
    ["permissions", "权限规则"],
    ["audit", "审计"]
  ];

  function createState() {
    return {
      tab: "live",
      events: [],
      lastSequence: 0,
      streamController: null,
      streamAttempted: false,
      streamStatus: "idle",
      streamError: "",
      streamRetryTimer: null,
      streamRetryAttempt: 0,
      permissionRequests: [],
      permissionRequestsLoading: false,
      permissionRequestsLoaded: false,
      permissionRefreshQueued: false,
      permissionRequestError: "",
      permissionDecisionError: "",
      permissionSuccess: "",
      deciding: "",
      operatorId: "",
      decisionReason: "",
      chatTurns: [],
      chatText: "",
      chatPreviews: [],
      chatProvider: "",
      chatDryRun: null,
      chatMode: "persistent",
      chatSessions: [],
      chatSessionsLoaded: false,
      chatSession: null,
      chatTrace: null,
      chatTurnEvents: [],
      chatTurnController: null,
      chatTurnID: "",
      chatStreamText: "",
      chatStreamRetryTimer: null,
      chatStreamRetryAttempt: 0,
      chatTraceLoadGeneration: 0,
      chatTraceReloadTimer: null,
      chatTraceReloadInFlight: false,
      chatTraceReloadQueued: false,
      chatCancelling: false,
      chatLoading: false,
      chatError: "",
      permissionRules: [],
      permissionRulesLoading: false,
      permissionRulesLoaded: false,
      permissionRulesError: "",
      permissionRulesSuccess: "",
      deletingRule: "",
      auditFilters: {
        event_type: "",
        run_id: "",
        action_id: "",
        actor_type: "",
        actor_id: "",
        created_after: "",
        created_before: ""
      },
      auditEvents: [],
      auditTotal: 0,
      auditLoading: false,
      auditLoaded: false,
      auditExporting: false,
      auditError: ""
    };
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function asArray(value) {
    return Array.isArray(value) ? value : [];
  }

  function formatTime(value) {
    if (!value) {
      return "无时间";
    }
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function formatJSON(value) {
    if (value == null || value === "") {
      return "{}";
    }
    if (typeof value === "string") {
      try {
        return JSON.stringify(JSON.parse(value), null, 2);
      } catch (err) {
        return value;
      }
    }
    try {
      return JSON.stringify(value, null, 2);
    } catch (err) {
      return String(value);
    }
  }

  function eventTone(type) {
    if (/failed|denied|unknown/.test(type)) {
      return "danger";
    }
    if (/succeeded|completed|allowed/.test(type)) {
      return "success";
    }
    if (/requested|proposed|executing|started/.test(type)) {
      return "active";
    }
    return "neutral";
  }

  function traceDependencyState(action) {
    return String(action && action.scheduler && action.scheduler.state || "pending");
  }

  function traceBlockedReason(action) {
    return String(action && action.scheduler && action.scheduler.not_ready_reason ||
      action && action.not_ready_reason || "");
  }

  function traceDependencyLabel(state) {
    return {
      ready: "依赖就绪",
      blocked: "依赖阻塞",
      awaiting_review: "等待审核",
      completed: "已有结果",
      executing: "执行中",
      pending: "等待依赖"
    }[state] || state;
  }

  function riskTone(risk) {
    if (risk === "R4" || risk === "R3") {
      return "danger";
    }
    if (risk === "R2") {
      return "warning";
    }
    return "neutral";
  }

  function renderBadge(text, tone) {
    return '<span class="automationSupervision-badge automationSupervision-tone-' +
      Workbench.escapeHTML(tone || "neutral") + '">' + Workbench.escapeHTML(text || "无") + "</span>";
  }

  function responseError(response) {
    return response.text().then(function (text) {
      var message = response.status + " " + response.statusText;
      if (text) {
        try {
          var data = JSON.parse(text);
          message = data && data.error || message;
        } catch (err) {
          message = text;
        }
      }
      var error = new Error(message);
      error.status = response.status;
      throw error;
    });
  }

  function addRuntimeEvent(state, event) {
    var sequence = Number(event && event.sequence || 0);
    if (sequence && state.lastSequence && sequence < state.lastSequence) {
      state.events = [];
      state.lastSequence = 0;
    }
    if (sequence && state.events.some(function (item) {
      return Number(item.sequence || 0) === sequence;
    })) {
      return;
    }
    state.events.unshift(event || {});
    state.events = state.events.slice(0, 200);
    if (sequence > state.lastSequence) {
      state.lastSequence = sequence;
    }
    state.streamRetryAttempt = 0;
    Workbench.requestRender();
    var type = String(event && event.type || "");
    if (type === "permission.requested" || type === "permission.allowed" || type === "permission.denied") {
      loadPermissionRequests(state);
    }
  }

  function parseSSEDataBlock(block, onEvent, onError) {
    var data = [];
    block.split(/\r?\n/).forEach(function (line) {
      if (line.indexOf("data:") === 0) {
        data.push(line.slice(5).replace(/^ /, ""));
      }
    });
    if (!data.length) {
      return;
    }
    try {
      onEvent(JSON.parse(data.join("\n")));
    } catch (err) {
      onError(err);
    }
  }

  function readSSEStream(response, controller, onEvent, onConnected, onParseError) {
    if (!response.ok) {
      return responseError(response);
    }
    if (!response.body || typeof response.body.getReader !== "function") {
      return Promise.reject(new Error("当前浏览器不支持流式响应"));
    }
    onConnected();

    var reader = response.body.getReader();
    var decoder = new TextDecoder();
    var buffer = "";
    function read() {
      return reader.read().then(function (chunk) {
        if (chunk.done) {
          buffer += decoder.decode();
          if (buffer.trim()) {
            parseSSEDataBlock(buffer, onEvent, onParseError);
          }
          return;
        }
        buffer += decoder.decode(chunk.value, { stream: true });
        var blocks = buffer.split(/\r?\n\r?\n/);
        buffer = blocks.pop() || "";
        blocks.forEach(function (block) {
          parseSSEDataBlock(block, onEvent, onParseError);
        });
        return read();
      });
    }
    return read().finally(function () {
      reader.releaseLock();
    });
  }

  function cancelSSERecoveryTimer(state, timerField) {
    if (state[timerField]) {
      window.clearTimeout(state[timerField]);
      state[timerField] = null;
    }
  }

  function clearSSERecovery(state, timerField, attemptField) {
    cancelSSERecoveryTimer(state, timerField);
    state[attemptField] = 0;
  }

  function scheduleSSERecovery(state, timerField, attemptField, shouldRecover, recover) {
    if (state[timerField] || !shouldRecover()) {
      return;
    }
    state[attemptField]++;
    var exponent = Math.max(0, state[attemptField] - 1);
    var delay = Math.min(CHAT_STREAM_RETRY_BASE_MS * Math.pow(2, exponent), CHAT_STREAM_RETRY_MAX_MS);
    state[timerField] = window.setTimeout(function () {
      state[timerField] = null;
      if (shouldRecover()) {
        recover();
      }
    }, delay);
  }

  function scheduleEventStreamRecovery(state) {
    scheduleSSERecovery(
      state,
      "streamRetryTimer",
      "streamRetryAttempt",
      function () { return state.streamAttempted; },
      function () {
        if (!state.streamController) {
          connectEventStream(state);
        }
      }
    );
  }

  function connectEventStream(state) {
    if (state.streamController) {
      return Promise.resolve();
    }
    var controller = new AbortController();
    var path = EVENT_PATH + Workbench.encodeQuery({ after: state.lastSequence || "" });
    state.streamController = controller;
    state.streamStatus = "connecting";
    state.streamError = "";
    Workbench.requestRender();

    return Workbench.automationFetch(path, {
      method: "GET",
      headers: { Accept: "text/event-stream" },
      signal: controller.signal
    }).then(function (response) {
      return readSSEStream(
        response,
        controller,
        function (event) { addRuntimeEvent(state, event); },
        function () {
          state.streamStatus = "connected";
          Workbench.requestRender();
        },
        function () {
          state.streamError = "收到无法解析的运行时事件";
          Workbench.requestRender();
        }
      );
    }).then(function () {
      if (!controller.signal.aborted) {
        state.streamStatus = "disconnected";
        scheduleEventStreamRecovery(state);
        Workbench.requestRender();
      }
    }).catch(function (err) {
      if (!controller.signal.aborted) {
        state.streamStatus = "error";
        state.streamError = err.message;
        scheduleEventStreamRecovery(state);
        Workbench.requestRender();
      }
    }).finally(function () {
      if (state.streamController === controller) {
        state.streamController = null;
        if (controller.signal.aborted) {
          state.streamStatus = "disconnected";
        }
        Workbench.requestRender();
      }
    });
  }

  function reconnectEventStream(state) {
    if (state.streamController) {
      state.streamController.abort();
      state.streamController = null;
    }
    clearSSERecovery(state, "streamRetryTimer", "streamRetryAttempt");
    state.streamError = "";
    return connectEventStream(state);
  }

  function loadPermissionRequests(state) {
    if (state.permissionRequestsLoading) {
      state.permissionRefreshQueued = true;
      return Promise.resolve();
    }
    state.permissionRequestsLoading = true;
    state.permissionRefreshQueued = false;
    state.permissionRequestError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(PERMISSION_REQUESTS_PATH + Workbench.encodeQuery({
      status: "pending",
      limit: PAGE_SIZE,
      offset: 0
    })).then(function (data) {
      state.permissionRequests = data && asArray(data.permission_requests);
      state.permissionRequestsLoaded = true;
    }).catch(function (err) {
      state.permissionRequestError = err.message;
    }).finally(function () {
      state.permissionRequestsLoading = false;
      if (state.permissionRefreshQueued) {
        state.permissionRefreshQueued = false;
        return loadPermissionRequests(state);
      }
      Workbench.requestRender();
    });
  }

  function decidePermission(state, requestID, decision) {
    var request = state.permissionRequests.find(function (item) {
      return String(item && item.id || "") === requestID;
    });
    var operatorID = state.operatorId.trim();
    if (!request) {
      return;
    }
    if (!operatorID) {
      state.permissionRequestError = "请输入操作员 ID。";
      Workbench.requestRender();
      return;
    }
    state.deciding = requestID + ":" + decision;
    state.permissionRequestError = "";
    state.permissionDecisionError = "";
    state.permissionSuccess = "";
    Workbench.requestRender();
    return Workbench.automationAPI(
      PERMISSION_REQUESTS_PATH + "/" + encodeURIComponent(requestID) + "/decide",
      {
        method: "POST",
        body: {
          expected_version: Number(request.version),
          decision: decision,
          operator_id: operatorID,
          reason: state.decisionReason.trim()
        }
      }
    ).then(function () {
      state.permissionSuccess = "权限决定已提交。";
      return loadPermissionRequests(state);
    }).catch(function (err) {
      state.permissionDecisionError = err.message;
      return loadPermissionRequests(state);
    }).finally(function () {
      state.deciding = "";
      Workbench.requestRender();
    });
  }

  function recentChatTurns(turns, currentText) {
    var selected = [];
    var length = 0;
    var budget = Math.max(0, 3600 - currentText.length);
    for (var index = turns.length - 1; index >= 0 && selected.length < 16; index -= 1) {
      var turn = turns[index];
      var content = String(turn && turn.content || "");
      if (length + content.length > budget) {
        break;
      }
      selected.unshift({ role: turn.role, content: content });
      length += content.length;
    }
    return selected;
  }

  function sendTestChat(state) {
    var text = state.chatText.trim();
    if (!text || state.chatLoading) {
      return;
    }
    var context = recentChatTurns(state.chatTurns, text);
    state.chatLoading = true;
    state.chatError = "";
    state.chatDryRun = null;
    Workbench.requestRender();
    return Workbench.automationAPI(CHAT_PATH, {
      method: "POST",
      body: { text: text, turns: context }
    }).then(function (data) {
      state.chatDryRun = data && data.dry_run === false ? false : data && data.dry_run === true ? true : null;
      if (state.chatDryRun !== true) {
        throw new Error(state.chatDryRun === false ?
          "测试对话返回 dry_run=false，已拒绝作为安全预览。" :
          "测试对话响应缺少 dry_run=true。");
      }
      state.chatTurns.push({ role: "user", content: text });
      if (data && data.assistant_message) {
        state.chatTurns.push({ role: "assistant", content: data.assistant_message });
      }
      state.chatTurns = state.chatTurns.slice(-32);
      state.chatProvider = data && data.provider_name || "";
      var previewedAt = new Date().toISOString();
      var previews = data && asArray(data.tool_previews).map(function (preview) {
        return Object.assign({}, preview || {}, {
          _provider: state.chatProvider,
          _created_at: previewedAt
        });
      });
      state.chatPreviews = previews.concat(state.chatPreviews).slice(0, 100);
      state.chatText = "";
    }).catch(function (err) {
      state.chatError = err.message;
    }).finally(function () {
      state.chatLoading = false;
      Workbench.requestRender();
    });
  }

  function activeChatTurn(state) {
    var turns = asArray(state.chatTrace && state.chatTrace.turns);
    for (var index = turns.length - 1; index >= 0; index -= 1) {
      if (["queued", "running", "awaiting_review"].indexOf(turns[index].status) !== -1) {
        return turns[index];
      }
    }
    return null;
  }

  function turnEventData(event) {
    return event && event.data && typeof event.data === "object" ? event.data : {};
  }

  function mergeTurnEvents(persisted, live) {
    var merged = [];
    var seen = Object.create(null);
    asArray(persisted).concat(asArray(live)).forEach(function (event) {
      var sequence = Number(event && event.sequence || 0);
      var key = sequence ? "sequence:" + sequence : [
        event && event.turn_id,
        event && event.type,
        event && event.created_at,
        formatJSON(event && event.data)
      ].join(":");
      if (seen[key]) {
        return;
      }
      seen[key] = true;
      merged.push(event || {});
    });
    merged.sort(function (left, right) {
      return Number(left.sequence || 0) - Number(right.sequence || 0);
    });
    return merged;
  }

  function streamedTurnText(events, turnID) {
    return asArray(events).reduce(function (text, event) {
      if (!event || event.turn_id !== turnID || event.type !== "assistant.text.delta") {
        return text;
      }
      return text + String(turnEventData(event).text_delta || "");
    }, "");
  }

  function clearChatStreamRetry(state) {
    clearSSERecovery(state, "chatStreamRetryTimer", "chatStreamRetryAttempt");
  }

  function resetChatSessionTransport(state) {
    if (state.chatTurnController) {
      state.chatTurnController.abort();
    }
    state.chatTurnController = null;
    state.chatTurnID = "";
    state.chatTurnEvents = [];
    state.chatStreamText = "";
    clearChatStreamRetry(state);
    state.chatTraceReloadQueued = false;
    if (state.chatTraceReloadTimer) {
      window.clearTimeout(state.chatTraceReloadTimer);
      state.chatTraceReloadTimer = null;
    }
  }

  function loadChatSessions(state, preferredID) {
    return Workbench.automationAPI(CHAT_SESSIONS_PATH + Workbench.encodeQuery({
      limit: PAGE_SIZE,
      offset: 0
    })).then(function (data) {
      state.chatSessions = data && asArray(data.sessions);
      state.chatSessionsLoaded = true;
      var sessionID = preferredID || state.chatSession && state.chatSession.id ||
        state.chatSessions[0] && state.chatSessions[0].id;
      if (sessionID) {
        return loadChatSession(state, sessionID);
      }
      resetChatSessionTransport(state);
      state.chatSession = null;
      state.chatTrace = null;
    }).catch(function (err) {
      state.chatError = err.message;
    }).finally(function () {
      Workbench.requestRender();
    });
  }

  function loadChatSession(state, sessionID) {
    var switchingSession = state.chatSession && state.chatSession.id !== sessionID;
    if (switchingSession) {
      resetChatSessionTransport(state);
    }
    var generation = ++state.chatTraceLoadGeneration;
    return Workbench.automationAPI(CHAT_SESSIONS_PATH + "/" + encodeURIComponent(sessionID)).then(function (trace) {
      var loadedTrace = trace || {};
      var actions = asArray(loadedTrace.actions);
      return Promise.all(actions.map(function (action) {
        return Workbench.automationAPI(BASE_PATH + "/actions/" + encodeURIComponent(action && action.id || ""));
      })).then(function (details) {
        loadedTrace.actions = details.map(function (detail, index) {
          return detail && detail.action || actions[index];
        });
        return loadedTrace;
      });
    }).then(function (trace) {
      if (generation !== state.chatTraceLoadGeneration) {
        return;
      }
      trace.turn_events = mergeTurnEvents(trace && trace.turn_events, state.chatTurnEvents);
      state.chatTrace = trace;
      state.chatSession = trace && trace.session || null;
      state.chatTurnEvents = trace && asArray(trace.turn_events);
      var turn = activeChatTurn(state);
      state.chatStreamText = turn ? streamedTurnText(state.chatTurnEvents, turn.id) : "";
      if (turn && state.chatTurnID !== turn.id) {
        connectTurnStream(state, turn.id);
      } else if (!turn) {
        if (state.chatTurnController) {
          state.chatTurnController.abort();
        }
        state.chatTurnController = null;
        state.chatTurnID = "";
        state.chatStreamText = "";
        clearChatStreamRetry(state);
      }
    }).catch(function (err) {
      if (generation === state.chatTraceLoadGeneration) {
        state.chatError = err.message;
      }
    }).finally(function () {
      if (generation === state.chatTraceLoadGeneration) {
        Workbench.requestRender();
      }
    });
  }

  function queueChatTraceReload(state) {
    if (!state.chatSession) {
      return;
    }
    state.chatTraceReloadQueued = true;
    if (state.chatTraceReloadTimer || state.chatTraceReloadInFlight) {
      return;
    }
    state.chatTraceReloadTimer = window.setTimeout(function () {
      state.chatTraceReloadTimer = null;
      if (!state.chatTraceReloadQueued || !state.chatSession) {
        return;
      }
      state.chatTraceReloadQueued = false;
      state.chatTraceReloadInFlight = true;
      loadChatSession(state, state.chatSession.id).finally(function () {
        state.chatTraceReloadInFlight = false;
        if (state.chatTraceReloadQueued) {
          queueChatTraceReload(state);
        }
      });
    }, CHAT_TRACE_RELOAD_DELAY_MS);
  }

  function createChatSession(state, title) {
    return Workbench.automationAPI(CHAT_SESSIONS_PATH, {
      method: "POST",
      body: { title: title || "新对话" }
    }).then(function (data) {
      var session = data && data.session;
      if (!session) {
        throw new Error("创建会话响应缺少 session。");
      }
      resetChatSessionTransport(state);
      state.chatTraceLoadGeneration++;
      state.chatSession = session;
      state.chatTrace = {
        session: session,
        turns: [],
        turn_events: [],
        runs: [],
        actions: [],
        permission_requests: [],
        results: [],
        audit_events: []
      };
      return loadChatSessions(state, session.id).then(function () {
        return session;
      });
    });
  }

  function appendTurnEvent(state, event) {
    if (state.chatTurnID && event && event.turn_id && event.turn_id !== state.chatTurnID) {
      return;
    }
    var before = state.chatTurnEvents.length;
    state.chatTurnEvents = mergeTurnEvents(state.chatTurnEvents, [event || {}]);
    if (state.chatTurnEvents.length === before) {
      return;
    }
    state.chatStreamRetryAttempt = 0;
    state.chatStreamText = streamedTurnText(state.chatTurnEvents, state.chatTurnID);
    if (event && [
      "turn.started", "tool.call.proposed", "permission.requested", "permission.allowed", "permission.denied",
      "tool.executing", "tool.succeeded", "tool.failed", "turn.completed",
      "turn.failed", "turn.cancelled"
    ].indexOf(event.type) !== -1 && state.chatSession) {
      queueChatTraceReload(state);
    }
    Workbench.requestRender();
  }

  function scheduleTurnStreamRecovery(state, turnID) {
    scheduleSSERecovery(
      state,
      "chatStreamRetryTimer",
      "chatStreamRetryAttempt",
      function () {
        return !!state.chatSession && state.chatTurnID === turnID;
      },
      function () {
        var sessionID = state.chatSession.id;
        loadChatSession(state, sessionID).then(function () {
          var turn = activeChatTurn(state);
          if (turn && turn.id === turnID && !state.chatTurnController) {
            connectTurnStream(state, turnID);
          }
        });
      }
    );
  }

  function connectTurnStream(state, turnID) {
    if (state.chatTurnController) {
      state.chatTurnController.abort();
    }
    cancelSSERecoveryTimer(state, "chatStreamRetryTimer");
    var controller = new AbortController();
    var after = state.chatTurnEvents.reduce(function (latest, event) {
      return event && event.turn_id === turnID ? Math.max(latest, Number(event.sequence || 0)) : latest;
    }, 0);
    state.chatTurnController = controller;
    state.chatTurnID = turnID;
    state.chatStreamText = streamedTurnText(state.chatTurnEvents, turnID);
    return Workbench.automationFetch(
      CHAT_TURNS_PATH + "/" + encodeURIComponent(turnID) + "/events" +
        Workbench.encodeQuery({ after: after || "" }),
      { method: "GET", headers: { Accept: "text/event-stream" }, signal: controller.signal }
    ).then(function (response) {
      return readSSEStream(
        response,
        controller,
        function (event) { appendTurnEvent(state, event); },
        function () {},
        function () {
          state.chatError = "收到无法解析的回合事件";
          Workbench.requestRender();
        }
      );
    }).then(function () {
      if (!controller.signal.aborted) {
        scheduleTurnStreamRecovery(state, turnID);
      }
    }).catch(function (err) {
      if (!controller.signal.aborted) {
        state.chatError = err.message;
        scheduleTurnStreamRecovery(state, turnID);
        Workbench.requestRender();
      }
    }).finally(function () {
      if (state.chatTurnController === controller) {
        state.chatTurnController = null;
      }
    });
  }

  function sendPersistentChat(state) {
    var text = state.chatText.trim();
    if (!text || state.chatLoading) {
      return;
    }
    state.chatLoading = true;
    state.chatError = "";
    Workbench.requestRender();
    var ensureSession = state.chatSession ?
      Promise.resolve(state.chatSession) :
      createChatSession(state, text.slice(0, 48));
    return ensureSession.then(function (session) {
      return Workbench.automationAPI(
        CHAT_SESSIONS_PATH + "/" + encodeURIComponent(session.id) + "/turns",
        {
          method: "POST",
          body: { expected_version: Number(session.version), text: text }
        }
      );
    }).then(function (data) {
      var turn = data && data.turn;
      if (!turn) {
        throw new Error("提交回合响应缺少 turn。");
      }
      state.chatText = "";
      state.chatTurnEvents = [];
      state.chatStreamText = "";
      connectTurnStream(state, turn.id);
      return loadChatSessions(state, turn.session_id);
    }).catch(function (err) {
      state.chatError = err.message;
    }).finally(function () {
      state.chatLoading = false;
      Workbench.requestRender();
    });
  }

  function cancelPersistentTurn(state) {
    var turn = activeChatTurn(state);
    var operatorID = state.operatorId.trim();
    if (!turn || state.chatCancelling) {
      return;
    }
    if (!operatorID) {
      state.chatError = "取消前请输入操作员 ID。";
      Workbench.requestRender();
      return;
    }
    state.chatCancelling = true;
    state.chatError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(
      CHAT_TURNS_PATH + "/" + encodeURIComponent(turn.id) + "/cancel",
      {
        method: "POST",
        body: { expected_version: Number(turn.version), operator_id: operatorID }
      }
    ).then(function () {
      return loadChatSession(state, state.chatSession.id);
    }).catch(function (err) {
      state.chatError = err.message;
    }).finally(function () {
      state.chatCancelling = false;
      Workbench.requestRender();
    });
  }

  function decideChatPermission(state, requestID, decision) {
    var requests = asArray(state.chatTrace && state.chatTrace.permission_requests);
    var actions = asArray(state.chatTrace && state.chatTrace.actions);
    var request = requests.find(function (item) { return item && item.id === requestID; });
    var action = request && actions.find(function (item) { return item && item.id === request.action_id; });
    var operatorID = state.operatorId.trim();
    if (!request || !action || request.status !== "pending") {
      return;
    }
    if (!operatorID) {
      state.chatError = "审批前请输入操作员 ID。";
      Workbench.requestRender();
      return;
    }
    state.deciding = requestID + ":" + decision;
    state.chatError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(
      PERMISSION_REQUESTS_PATH + "/" + encodeURIComponent(requestID) + "/decide",
      {
        method: "POST",
        body: {
          expected_version: Number(request.version),
          expected_action_version: Number(action.version),
          decision: decision,
          operator_id: operatorID,
          reason: state.decisionReason.trim()
        }
      }
    ).then(function () {
      return loadChatSession(state, state.chatSession.id);
    }).catch(function (err) {
      state.chatError = err.message;
    }).finally(function () {
      state.deciding = "";
      Workbench.requestRender();
    });
  }

  function loadPermissionRules(state) {
    if (state.permissionRulesLoading) {
      return Promise.resolve();
    }
    state.permissionRulesLoading = true;
    state.permissionRulesError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(PERMISSIONS_PATH + Workbench.encodeQuery({
      limit: PAGE_SIZE,
      offset: 0
    })).then(function (data) {
      state.permissionRules = data && asArray(data.permissions);
      state.permissionRulesLoaded = true;
    }).catch(function (err) {
      state.permissionRulesError = err.message;
    }).finally(function () {
      state.permissionRulesLoading = false;
      Workbench.requestRender();
    });
  }

  function deletePermissionRule(state, ruleID) {
    if (!window.confirm("确认停用这条权限规则？")) {
      return;
    }
    state.deletingRule = ruleID;
    state.permissionRulesError = "";
    state.permissionRulesSuccess = "";
    Workbench.requestRender();
    return Workbench.automationAPI(PERMISSIONS_PATH + "/" + encodeURIComponent(ruleID), {
      method: "DELETE"
    }).then(function () {
      state.permissionRulesSuccess = "权限规则已停用。";
      return loadPermissionRules(state);
    }).catch(function (err) {
      state.permissionRulesError = err.message;
    }).finally(function () {
      state.deletingRule = "";
      Workbench.requestRender();
    });
  }

  function auditQuery(state, includePagination) {
    var query = Object.assign({}, state.auditFilters);
    Object.keys(query).forEach(function (key) {
      query[key] = String(query[key] || "").trim();
    });
    query.created_after = localDateTimeToRFC3339(query.created_after);
    query.created_before = localDateTimeToRFC3339(query.created_before);
    if (includePagination) {
      query.limit = PAGE_SIZE;
      query.offset = 0;
    }
    return Workbench.encodeQuery(query);
  }

  function loadAudit(state) {
    if (state.auditLoading) {
      return Promise.resolve();
    }
    state.auditLoading = true;
    state.auditError = "";
    Workbench.requestRender();
    return Workbench.automationAPI(AUDIT_PATH + auditQuery(state, true)).then(function (data) {
      state.auditEvents = data && asArray(data.audit_events);
      state.auditTotal = Number(data && data.total || 0);
      state.auditLoaded = true;
    }).catch(function (err) {
      state.auditError = err.message;
    }).finally(function () {
      state.auditLoading = false;
      Workbench.requestRender();
    });
  }

  function exportAudit(state) {
    if (state.auditExporting) {
      return;
    }
    state.auditExporting = true;
    state.auditError = "";
    Workbench.requestRender();
    return Workbench.automationFetch(AUDIT_EXPORT_PATH + auditQuery(state, false), {
      method: "GET",
      headers: { Accept: "application/x-ndjson" }
    }).then(function (response) {
      if (!response.ok) {
        return responseError(response);
      }
      return response.blob();
    }).then(function (blob) {
      var url = URL.createObjectURL(blob);
      var anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "automation-audit.jsonl";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      Promise.resolve().then(function () {
        URL.revokeObjectURL(url);
      });
    }).catch(function (err) {
      state.auditError = err.message;
    }).finally(function () {
      state.auditExporting = false;
      Workbench.requestRender();
    });
  }

  function renderEvent(event) {
    var type = String(event && event.type || "message");
    var identity = [
      event && event.run_id,
      event && event.action_id,
      event && event.permission_request_id
    ].filter(Boolean).join(" / ");
    return '<li class="automationSupervision-event">' +
      '<div class="automationSupervision-eventRail"><span></span></div>' +
      '<div class="automationSupervision-eventBody"><div class="automationSupervision-rowHeading"><div>' +
      renderBadge(type, eventTone(type)) + '<strong>#' + Workbench.escapeHTML(event && event.sequence || "?") +
      '</strong></div><time>' + Workbench.escapeHTML(formatTime(event && event.created_at)) + "</time></div>" +
      (identity ? '<div class="automationSupervision-meta">' + Workbench.escapeHTML(identity) + "</div>" : "") +
      '<pre>' + Workbench.escapeHTML(formatJSON(event && event.data)) + "</pre></div></li>";
  }

  function renderDecisionButtons(state, request) {
    var requestID = String(request && request.id || "");
    return DECISIONS.map(function (entry) {
      var pending = state.deciding === requestID + ":" + entry[0];
      return '<button class="automationSupervision-decision automationSupervision-decision-' + entry[2] +
        '" type="button" data-supervision-decision="' + entry[0] + '" data-supervision-request="' +
        Workbench.escapeHTML(requestID) + '"' + disabled(!!state.deciding) + ">" +
        (pending ? "提交中..." : entry[1]) + "</button>";
    }).join("");
  }

  function renderPermissionRequest(state, request) {
    var action = request && request.action || {};
    var schedulerState = traceDependencyState(action);
    var blockedReason = traceBlockedReason(action);
    return '<li class="automationSupervision-request"><div class="automationSupervision-rowHeading"><div><strong>' +
      Workbench.escapeHTML(request && request.tool || "未知工具") + "</strong>" +
      renderBadge(request && request.risk || "无风险", riskTone(request && request.risk)) +
      renderBadge(traceDependencyLabel(schedulerState), schedulerState) +
      '</div><span>v' + Workbench.escapeHTML(request && request.version || 0) + "</span></div>" +
      '<dl class="automationSupervision-kv"><dt>请求</dt><dd>' + Workbench.escapeHTML(request && request.id || "无") +
      '</dd><dt>运行</dt><dd>' + Workbench.escapeHTML(request && request.run_id || "无") +
      '</dd><dt>动作</dt><dd>' + Workbench.escapeHTML(request && request.action_id || "无") +
      '</dd><dt>调度</dt><dd>' + Workbench.escapeHTML(traceDependencyLabel(schedulerState)) +
      (blockedReason ? " / " + Workbench.escapeHTML(blockedReason) : "") +
      '</dd><dt>结果实体</dt><dd><pre>' + Workbench.escapeHTML(formatJSON(action.result_entity)) + "</pre>" +
      '</dd><dt>创建</dt><dd>' + Workbench.escapeHTML(formatTime(request && request.created_at)) +
      '</dd></dl><div class="automationSupervision-decisionBar">' + renderDecisionButtons(state, request) + "</div></li>";
  }

  function renderLive(state) {
    var statusLabel = {
      idle: "尚未连接",
      connecting: "正在连接",
      connected: "实时连接",
      disconnected: "连接已结束",
      error: "连接失败"
    }[state.streamStatus] || state.streamStatus;
    var events = state.events.length ? state.events.map(renderEvent).join("") :
      '<li class="automationSupervision-empty">暂无运行时事件。</li>';
    var requests = state.permissionRequests.length ? state.permissionRequests.map(function (request) {
      return renderPermissionRequest(state, request || {});
    }).join("") : '<li class="automationSupervision-empty">' +
      (state.permissionRequestsLoading ? "正在加载权限请求..." : "没有待处理的权限请求。") + "</li>";
    return '<div class="automationSupervision-liveToolbar"><div class="automationSupervision-connection">' +
      '<span class="automationSupervision-connectionDot automationSupervision-connection-' +
      Workbench.escapeHTML(state.streamStatus) + '"></span><strong>' + Workbench.escapeHTML(statusLabel) +
      '</strong><span>最后序号 ' + Workbench.escapeHTML(state.lastSequence || 0) + "</span></div>" +
      '<button type="button" data-supervision-action="reconnect"' +
      disabled(state.streamStatus === "connecting" || state.streamStatus === "connected") + ">重新连接</button></div>" +
      Workbench.renderStatus(state.streamError, "", "") +
      '<div class="automationSupervision-liveGrid"><section class="automationSupervision-pane" aria-label="实时事件时间线">' +
      '<div class="automationSupervision-paneHeader"><h3>事件时间线</h3><span>' + state.events.length +
      ' 条</span></div><ol class="automationSupervision-timeline">' + events + "</ol></section>" +
      '<section class="automationSupervision-pane automationSupervision-reviewPane" aria-label="待处理权限请求">' +
      '<div class="automationSupervision-paneHeader"><h3>待处理权限</h3><button type="button" data-supervision-action="refresh-requests"' +
      disabled(state.permissionRequestsLoading) + '>刷新</button></div>' +
      '<div class="automationSupervision-reviewFields"><label>操作员 ID<input data-supervision-field="operator-id" value="' +
      Workbench.escapeHTML(state.operatorId) + '" autocomplete="off" placeholder="operator-local"></label>' +
      '<label>决定原因<input data-supervision-field="decision-reason" value="' +
      Workbench.escapeHTML(state.decisionReason) + '" autocomplete="off" placeholder="可选"></label></div>' +
      Workbench.renderStatus(state.permissionDecisionError || state.permissionRequestError, state.permissionSuccess, "") +
      '<ol class="automationSupervision-requestList">' + requests + "</ol></section></div>";
  }

  function renderChatTurn(turn, index) {
    var assistant = turn && turn.role === "assistant";
    return '<li class="automationSupervision-chatTurn automationSupervision-chatTurn-' +
      (assistant ? "assistant" : "user") + '"><span>' + (assistant ? "AI" : "用户") +
      '</span><div>' + Workbench.escapeHTML(turn && turn.content || "") + "</div></li>";
  }

  function renderPreview(preview) {
    var reasons = asArray(preview && preview.risk_reasons);
    var meta = [
      preview && preview.call_id,
      preview && preview._provider,
      preview && preview._created_at && formatTime(preview._created_at)
    ].filter(Boolean).join(" / ");
    return '<li class="automationSupervision-previewRow"><div class="automationSupervision-rowHeading"><div><strong>' +
      Workbench.escapeHTML(preview && preview.tool || "未知工具") + "</strong>" +
      renderBadge(preview && preview.risk || "无风险", riskTone(preview && preview.risk)) +
      renderBadge(preview && preview.policy_decision || "无策略结果", "neutral") +
      '</div><span>' + Math.round(Number(preview && preview.confidence || 0) * 100) + "%</span></div>" +
      (meta ? '<div class="automationSupervision-meta">' + Workbench.escapeHTML(meta) + "</div>" : "") +
      (reasons.length ? '<div class="automationSupervision-reasons">' + reasons.map(function (reason) {
        return "<span>" + Workbench.escapeHTML(reason) + "</span>";
      }).join("") + "</div>" : "") +
      '<pre>' + Workbench.escapeHTML(formatJSON(preview && preview.arguments)) + "</pre></li>";
  }

  function traceEventLabel(type) {
    return {
      "tool.call.delta": "生成参数",
      "tool.call.proposed": "调用已提案",
      "permission.requested": "等待批准",
      "permission.allowed": "批准执行",
      "permission.denied": "拒绝执行",
      "tool.executing": "执行中",
      "tool.succeeded": "执行成功",
      "tool.failed": "执行失败"
    }[type] || type;
  }

  function traceEventTone(type) {
    if (type === "tool.failed" || type === "permission.denied") {
      return "danger";
    }
    if (type === "tool.succeeded" || type === "permission.allowed") {
      return "success";
    }
    if (type === "tool.executing" || type === "tool.call.proposed") {
      return "active";
    }
    return "neutral";
  }

  function compactTraceEvents(events) {
    var compacted = [];
    var delta = null;
    asArray(events).forEach(function (event) {
      if (event && event.type === "tool.call.delta") {
        if (!delta) {
          delta = {
            type: event.type,
            sequence: event.sequence,
            created_at: event.created_at,
            count: 0
          };
        }
        delta.count++;
        delta.created_at = event.created_at || delta.created_at;
        return;
      }
      compacted.push(event || {});
    });
    if (delta) {
      compacted.push(delta);
    }
    compacted.sort(function (left, right) {
      return Number(left.sequence || 0) - Number(right.sequence || 0);
    });
    return compacted;
  }

  function renderTraceStages(events) {
    var stages = compactTraceEvents(events);
    if (!stages.length) {
      return "";
    }
    return '<ol class="automationSupervision-traceStages" aria-label="工具调用阶段">' +
      stages.map(function (event) {
        var detail = event.type === "tool.call.delta" ?
          String(event.count || 1) + " 个片段" :
          "#" + String(event.sequence || "?");
        return '<li class="automationSupervision-traceStage automationSupervision-traceStage-' +
          Workbench.escapeHTML(traceEventTone(event.type)) + '"><span>' +
          Workbench.escapeHTML(traceEventLabel(event.type)) + '</span><small>' +
          Workbench.escapeHTML(detail) + "</small></li>";
      }).join("") + "</ol>";
  }

  function actionTraceEvents(state, action, events) {
    var actionID = String(action && action.id || "");
    var callID = String(action && action.provider_call_id || "");
    return mergeTurnEvents(events || state.chatTurnEvents, []).filter(function (event) {
      var data = turnEventData(event);
      return actionID && String(data.action_id || "") === actionID ||
        callID && (
          String(data.call_id || "") === callID ||
          String(data.provider_call_id || "") === callID
        );
    });
  }

  function projectedActionStatus(state, action, events) {
    var status = String(action && action.status || "unknown");
    actionTraceEvents(state, action, events).forEach(function (event) {
      var data = turnEventData(event);
      if (event.type === "tool.call.proposed") {
        status = String(data.status || status);
      } else if (event.type === "permission.requested") {
        status = "awaiting_review";
      } else if (event.type === "permission.allowed") {
        status = "approved";
      } else if (event.type === "permission.denied") {
        status = "denied";
      } else if (event.type === "tool.executing") {
        status = "executing";
      } else if (event.type === "tool.succeeded") {
        status = "succeeded";
      } else if (event.type === "tool.failed") {
        status = "failed";
      }
    });
    return status;
  }

  function permissionSettledInEvents(state, action, events) {
    return actionTraceEvents(state, action, events).some(function (event) {
      return event && (event.type === "permission.allowed" || event.type === "permission.denied");
    });
  }

  function pendingTraceCalls(state, actions, events) {
    var persistedActionIDs = Object.create(null);
    var persistedCallIDs = Object.create(null);
    asArray(actions).forEach(function (action) {
      persistedActionIDs[String(action && action.id || "")] = true;
      persistedCallIDs[String(action && action.provider_call_id || "")] = true;
    });
    var calls = Object.create(null);
    mergeTurnEvents(events || state.chatTurnEvents, []).forEach(function (event) {
      if (!event || event.type !== "tool.call.delta" && event.type !== "tool.call.proposed") {
        return;
      }
      var data = turnEventData(event);
      var callID = String(data.call_id || data.provider_call_id || "");
      if (!callID) {
        return;
      }
      if (!calls[callID]) {
        calls[callID] = {
          call_id: callID,
          tool: "",
          action_id: "",
          status: "streaming",
          risk: "",
          arguments_fragment: "",
          events: [],
          first_sequence: Number(event.sequence || 0)
        };
      }
      var call = calls[callID];
      call.events.push(event);
      call.tool = String(data.tool || call.tool);
      if (event.type === "tool.call.delta") {
        call.arguments_fragment += String(data.arguments_fragment || "");
      } else {
        call.action_id = String(data.action_id || "");
        call.status = String(data.status || "proposed");
        call.risk = String(data.risk || "");
      }
    });
    return Object.keys(calls).map(function (callID) {
      return calls[callID];
    }).filter(function (call) {
      return !persistedCallIDs[call.call_id] &&
        (!call.action_id || !persistedActionIDs[call.action_id]);
    }).sort(function (left, right) {
      return left.first_sequence - right.first_sequence;
    });
  }

  function renderPendingTraceCall(call) {
    return '<li class="automationSupervision-traceAction automationSupervision-traceAction-live">' +
      '<div class="automationSupervision-rowHeading"><div><strong>' +
      Workbench.escapeHTML(call.tool || "正在识别工具") + "</strong>" +
      renderBadge(call.status === "streaming" ? "生成调用" : call.status, "active") +
      (call.risk ? renderBadge(call.risk, riskTone(call.risk)) : "") +
      '</div><span>实时事件</span></div><dl class="automationSupervision-kv"><dt>调用</dt><dd>' +
      Workbench.escapeHTML(call.call_id) + '</dd><dt>动作</dt><dd>' +
      Workbench.escapeHTML(call.action_id || "等待持久化") + "</dd></dl>" +
      renderTraceStages(call.events) +
      '<div class="automationSupervision-traceDraft"><span>参数片段</span><pre>' +
      Workbench.escapeHTML(formatJSON(call.arguments_fragment)) + "</pre></div></li>";
  }

  function renderTraceAction(state, action, events) {
    var requests = asArray(state.chatTrace && state.chatTrace.permission_requests);
    var results = asArray(state.chatTrace && state.chatTrace.results);
    var request = requests.find(function (item) { return item && item.action_id === action.id; });
    var result = results.find(function (item) { return item && item.action_id === action.id; });
    var dependencyState = traceDependencyState(action);
    var blockedReason = traceBlockedReason(action);
    var status = projectedActionStatus(state, action, events);
    var traceEvents = actionTraceEvents(state, action, events);
    var canDecide = request && request.status === "pending" &&
      !permissionSettledInEvents(state, action, events);
    var decisions = canDecide ? '<div class="automationSupervision-inlineDecision">' +
      '<button type="button" class="automationSupervision-primary" data-chat-permission="' +
      Workbench.escapeHTML(request.id) + '" data-chat-decision="allow_once"' + disabled(!!state.deciding) +
      '>批准</button><button type="button" class="danger" data-chat-permission="' +
      Workbench.escapeHTML(request.id) + '" data-chat-decision="deny_once"' + disabled(!!state.deciding) +
      '>拒绝</button></div>' : "";
    return '<li class="automationSupervision-traceAction"><div class="automationSupervision-rowHeading"><div><strong>' +
      Workbench.escapeHTML(action.tool || "未知工具") + "</strong>" +
      renderBadge(status || "未知", eventTone(status)) +
      renderBadge(traceDependencyLabel(dependencyState), dependencyState) +
      renderBadge(action.risk || "无风险", riskTone(action.risk)) +
      '</div><time>' + Workbench.escapeHTML(formatTime(action.updated_at)) + '</time></div>' +
      '<dl class="automationSupervision-kv"><dt>调用</dt><dd>' +
      Workbench.escapeHTML(action.provider_call_id || "无") + '</dd><dt>动作</dt><dd>' +
      Workbench.escapeHTML(action.id || "无") + " / v" + Workbench.escapeHTML(action.version || 0) +
      '</dd><dt>依赖</dt><dd class="automationSupervision-dependencies">' +
      Workbench.escapeHTML(asArray(action.depends_on_action_ids).join(", ") || "无") +
      '</dd><dt>调度</dt><dd>' + Workbench.escapeHTML(traceDependencyLabel(dependencyState)) +
      (blockedReason ? " / " + Workbench.escapeHTML(blockedReason) : "") +
      '</dd><dt>权限</dt><dd>' + Workbench.escapeHTML(request ?
        request.status + " / v" + request.version + " / " + (request.operator_id || "system") : "无") +
      '</dd><dt>结果</dt><dd>' + Workbench.escapeHTML(result ?
        result.status + " / " + (result.id || "") : "等待结果") +
      "</dd></dl>" + renderTraceStages(traceEvents) +
      '<div class="automationSupervision-tracePayload"><div><span>参数</span><pre>' +
      Workbench.escapeHTML(formatJSON(action.payload)) + '</pre></div><div><span>结果实体 / 错误</span><pre>' +
      Workbench.escapeHTML(formatJSON(action.result_entity || {
        error_code: action.error_code || result && result.error_code || "",
        error_message: action.error_message || result && result.error_message || ""
      })) + "</pre></div></div>" + decisions + "</li>";
  }

  function actionsForTurn(state, turnID) {
    var trace = state.chatTrace || {};
    var runIDs = Object.create(null);
    asArray(trace.runs).forEach(function (run) {
      if (run && run.turn_id === turnID) {
        runIDs[String(run.id || "")] = true;
      }
    });
    return asArray(trace.actions).filter(function (action) {
      return action && runIDs[String(action.run_id || "")];
    });
  }

  function eventsForTurn(state, turnID) {
    return mergeTurnEvents(state.chatTurnEvents, []).filter(function (event) {
      return event && event.turn_id === turnID;
    });
  }

  function renderTurnTrace(state, turnID) {
    var events = eventsForTurn(state, turnID);
    var actions = actionsForTurn(state, turnID);
    var pendingCalls = pendingTraceCalls(state, actions, events);
    if (!actions.length && !pendingCalls.length) {
      return "";
    }
    return '<li class="automationSupervision-chatTrace"><div class="automationSupervision-meta">工具调用轨迹</div>' +
      '<ol class="automationSupervision-traceList">' +
      (pendingCalls.length ? pendingCalls.map(renderPendingTraceCall).join("") : "") +
      (actions.length ? actions.map(function (action) {
        return renderTraceAction(state, action || {}, events);
      }).join("") : "") + "</ol></li>";
  }

  function renderPersistentChat(state) {
    var trace = state.chatTrace || {};
    var turns = asArray(trace.turns);
    var renderedTurns = [];
    turns.forEach(function (turn) {
      renderedTurns.push(renderChatTurn({ role: "user", content: turn.user_text }));
      var assistantText = turn.assistant_text || (turn.id === state.chatTurnID ? state.chatStreamText : "");
      if (assistantText) {
        renderedTurns.push(renderChatTurn({ role: "assistant", content: assistantText }));
      }
      var turnTrace = renderTurnTrace(state, turn.id);
      if (turnTrace) {
        renderedTurns.push(turnTrace);
      }
    });
    var actions = asArray(trace.actions);
    var pendingCalls = pendingTraceCalls(state, actions);
    var active = activeChatTurn(state);
    var sessionOptions = state.chatSessions.map(function (session) {
      return '<option value="' + Workbench.escapeHTML(session.id) + '"' +
        (state.chatSession && state.chatSession.id === session.id ? " selected" : "") + ">" +
        Workbench.escapeHTML(session.title || session.id) + "</option>";
    }).join("");
    return '<div class="automationSupervision-chatToolbar"><div class="automationSupervision-modeSwitch">' +
      '<button type="button" data-chat-mode="persistent" aria-pressed="true">持久执行</button>' +
      '<button type="button" data-chat-mode="dry-run" aria-pressed="false">Dry run</button></div>' +
      '<select data-chat-session aria-label="对话会话">' + sessionOptions + '</select>' +
      '<button type="button" data-chat-action="new-session">新建</button>' +
      '<button type="button" data-chat-action="cancel-turn"' +
      disabled(!active || state.chatCancelling) + '>' + (state.chatCancelling ? "取消中..." : "取消回合") +
      '</button></div><div class="automationSupervision-reviewFields"><label>操作员 ID<input data-supervision-field="operator-id" value="' +
      Workbench.escapeHTML(state.operatorId) + '" autocomplete="off" placeholder="operator-local"></label>' +
      '<label>审批原因<input data-supervision-field="decision-reason" value="' +
      Workbench.escapeHTML(state.decisionReason) + '" autocomplete="off" placeholder="可选"></label></div>' +
      '<div class="automationSupervision-chatGrid"><section class="automationSupervision-pane automationSupervision-chatPane">' +
      '<div class="automationSupervision-paneHeader"><div><h3>' +
      Workbench.escapeHTML(state.chatSession && state.chatSession.title || "新对话") + '</h3>' +
      renderBadge(active ? active.status : "ready", active ? "active" : "success") +
      '</div><span>' + Workbench.escapeHTML(state.chatSession ?
        state.chatSession.id + " / v" + state.chatSession.version : "尚未创建") +
      '</span></div><ol class="automationSupervision-transcript">' +
      (renderedTurns.length ? renderedTurns.join("") :
        '<li class="automationSupervision-empty">暂无对话。</li>') +
      '</ol><form class="automationSupervision-chatForm" data-supervision-form="chat">' +
      '<label>消息<textarea name="text" maxlength="4000" required placeholder="输入招聘或运营问题">' +
      Workbench.escapeHTML(state.chatText) + '</textarea></label><button class="automationSupervision-primary" type="submit"' +
      disabled(state.chatLoading || !!active) + ">" + (state.chatLoading ? "提交中..." : "发送") +
      '</button></form>' + Workbench.renderStatus(state.chatError, "", "") + "</section>" +
      '<section class="automationSupervision-pane"><div class="automationSupervision-paneHeader"><h3>完整执行轨迹</h3><span>' +
      (actions.length + pendingCalls.length) + ' 个工具调用</span></div><ol class="automationSupervision-traceList">' +
      (pendingCalls.length ? pendingCalls.map(renderPendingTraceCall).join("") : "") +
      (actions.length ? actions.map(function (action) {
        return renderTraceAction(state, action || {});
      }).join("") : pendingCalls.length ? "" : '<li class="automationSupervision-empty">尚无工具调用。</li>') +
      "</ol></section></div>";
  }

  function renderDryRunChat(state) {
    var dryRunStatus = state.chatDryRun === true ? renderBadge("安全预览", "success") :
      state.chatDryRun === false ? renderBadge("预览已拒绝", "danger") : renderBadge("等待发送", "neutral");
    var turns = state.chatTurns.length ? state.chatTurns.map(renderChatTurn).join("") :
      '<li class="automationSupervision-empty">暂无对话。</li>';
    var previews = state.chatPreviews.length ? state.chatPreviews.map(function (preview) {
      return renderPreview(preview || {});
    }).join("") : '<li class="automationSupervision-empty">当前会话还没有工具调用。</li>';
    return '<div class="automationSupervision-chatToolbar"><div class="automationSupervision-modeSwitch">' +
      '<button type="button" data-chat-mode="persistent" aria-pressed="false">持久执行</button>' +
      '<button type="button" data-chat-mode="dry-run" aria-pressed="true">Dry run</button></div></div>' +
      '<div class="automationSupervision-chatGrid"><section class="automationSupervision-pane automationSupervision-chatPane">' +
      '<div class="automationSupervision-paneHeader"><div><h3>安全预览</h3>' + dryRunStatus + '</div><span>' +
      Workbench.escapeHTML(state.chatProvider || "未调用 provider") + '</span></div><ol class="automationSupervision-transcript">' +
      turns + '</ol><form class="automationSupervision-chatForm" data-supervision-form="chat">' +
      '<label>消息<textarea name="text" maxlength="4000" required>' + Workbench.escapeHTML(state.chatText) +
      '</textarea></label><button class="automationSupervision-primary" type="submit"' +
      disabled(state.chatLoading) + ">" + (state.chatLoading ? "生成中..." : "预览") +
      '</button></form>' + Workbench.renderStatus(state.chatError, "", "") + "</section>" +
      '<section class="automationSupervision-pane"><div class="automationSupervision-paneHeader"><h3>工具预览</h3><span>' +
      state.chatPreviews.length + ' 次</span></div><ol class="automationSupervision-previewList">' +
      previews + "</ol></section></div>";
  }

  function renderChat(state) {
    return state.chatMode === "dry-run" ? renderDryRunChat(state) : renderPersistentChat(state);
  }

  function renderStandaloneChat(state) {
    return '<section class="automationSupervision"><header class="automationSupervision-header"><div><h2>实时对话</h2></div></header>' +
      renderChat(state) + "</section>";
  }

  function renderPermissionRule(state, rule) {
    return '<li class="automationSupervision-rule"><div class="automationSupervision-rowHeading"><div><strong>' +
      Workbench.escapeHTML(rule && rule.tool || "未知工具") + "</strong>" +
      renderBadge(rule && rule.effect === "deny" ? "拒绝" : "允许", rule && rule.effect === "deny" ? "danger" : "success") +
      renderBadge(rule && rule.enabled ? "已启用" : "已停用", rule && rule.enabled ? "active" : "neutral") +
      '</div><span>v' + Workbench.escapeHTML(rule && rule.version || 0) + "</span></div>" +
      '<dl class="automationSupervision-kv"><dt>规则</dt><dd>' + Workbench.escapeHTML(rule && rule.id || "无") +
      '</dd><dt>创建者</dt><dd>' + Workbench.escapeHTML(rule && rule.created_by || "无") +
      '</dd><dt>范围键</dt><dd>' + Workbench.escapeHTML(rule && rule.scope_key || "无") +
      '</dd><dt>更新时间</dt><dd>' + Workbench.escapeHTML(formatTime(rule && rule.updated_at)) +
      '</dd></dl><pre>' + Workbench.escapeHTML(formatJSON(rule && rule.scope)) + "</pre>" +
      '<div class="automationSupervision-ruleActions"><button class="danger" type="button" data-supervision-delete-rule="' +
      Workbench.escapeHTML(rule && rule.id || "") + '"' +
      disabled(state.deletingRule === String(rule && rule.id || "") || !rule || !rule.enabled) + ">" +
      (state.deletingRule === String(rule && rule.id || "") ? "停用中..." : "停用规则") + "</button></div></li>";
  }

  function renderPermissions(state) {
    var rules = state.permissionRules.length ? state.permissionRules.map(function (rule) {
      return renderPermissionRule(state, rule || {});
    }).join("") : '<li class="automationSupervision-empty">' +
      (state.permissionRulesLoading ? "正在加载权限规则..." : "尚无权限规则。") + "</li>";
    return '<section class="automationSupervision-pane"><div class="automationSupervision-paneHeader"><div><h3>权限规则</h3><span>' +
      state.permissionRules.length + ' 条</span></div><button type="button" data-supervision-action="refresh-rules"' +
      disabled(state.permissionRulesLoading) + '>刷新</button></div>' +
      Workbench.renderStatus(state.permissionRulesError, state.permissionRulesSuccess, "") +
      '<ol class="automationSupervision-ruleList">' + rules + "</ol></section>";
  }

  function renderAuditEvent(event) {
    return '<li class="automationSupervision-auditRow"><div class="automationSupervision-rowHeading"><div>' +
      renderBadge(event && event.event_type || "未知事件", eventTone(String(event && event.event_type || ""))) +
      '<strong>' + Workbench.escapeHTML(event && event.id || "无 ID") + '</strong></div><time>' +
      Workbench.escapeHTML(formatTime(event && event.created_at)) + "</time></div>" +
      '<div class="automationSupervision-meta">' + Workbench.escapeHTML([
        event && event.run_id,
        event && event.action_id,
        event && event.actor_type,
        event && event.actor_id
      ].filter(Boolean).join(" / ") || "无关联主体") + '</div><pre>' +
      Workbench.escapeHTML(formatJSON(event && event.data)) + "</pre></li>";
  }

  function renderAudit(state) {
    var filters = state.auditFilters;
    var events = state.auditEvents.length ? state.auditEvents.map(function (event) {
      return renderAuditEvent(event || {});
    }).join("") : '<li class="automationSupervision-empty">' +
      (state.auditLoading ? "正在加载审计事件..." : "没有匹配的审计事件。") + "</li>";
    return '<section class="automationSupervision-pane"><form class="automationSupervision-auditFilters" data-supervision-form="audit">' +
      '<label>事件类型<input name="event_type" value="' + Workbench.escapeHTML(filters.event_type) +
      '" placeholder="permission.requested"></label><label>运行 ID<input name="run_id" value="' +
      Workbench.escapeHTML(filters.run_id) + '" placeholder="run_..."></label><label>动作 ID<input name="action_id" value="' +
      Workbench.escapeHTML(filters.action_id) + '" placeholder="aiact_..."></label><label>主体类型<input name="actor_type" value="' +
      Workbench.escapeHTML(filters.actor_type) + '" placeholder="operator"></label><label>主体 ID<input name="actor_id" value="' +
      Workbench.escapeHTML(filters.actor_id) + '" placeholder="operator-local"></label><label>开始时间<input name="created_after" type="datetime-local" value="' +
      Workbench.escapeHTML(filters.created_after) + '"></label><label>结束时间<input name="created_before" type="datetime-local" value="' +
      Workbench.escapeHTML(filters.created_before) + '"></label><div class="automationSupervision-auditActions">' +
      '<button class="automationSupervision-primary" type="submit"' + disabled(state.auditLoading) + '>查询</button>' +
      '<button type="button" data-supervision-action="clear-audit">清除</button>' +
      '<button type="button" data-supervision-action="export-audit"' + disabled(state.auditExporting) + ">" +
      (state.auditExporting ? "导出中..." : "导出 JSONL") + "</button></div></form>" +
      Workbench.renderStatus(state.auditError, "", "") +
      '<div class="automationSupervision-paneHeader automationSupervision-auditHeader"><h3>审计事件</h3><span>' +
      state.auditEvents.length + " / " + state.auditTotal + ' 条</span></div><ol class="automationSupervision-auditList">' +
      events + "</ol></section>";
  }

  function render(state) {
    var tabs = TABS.map(function (entry) {
      return '<button type="button" role="tab" data-supervision-tab="' + entry[0] +
        '" aria-selected="' + (state.tab === entry[0]) + '">' + entry[1] + "</button>";
    }).join("");
    var content;
    if (state.tab === "chat") {
      content = renderChat(state);
    } else if (state.tab === "permissions") {
      content = renderPermissions(state);
    } else if (state.tab === "audit") {
      content = renderAudit(state);
    } else {
      content = renderLive(state);
    }
    return '<section class="automationSupervision"><header class="automationSupervision-header"><div><h2>AI 实时监督</h2></div></header>' +
      '<div class="automationSupervision-tabs" role="tablist" aria-label="AI 实时监督视图">' +
      tabs + "</div>" + content + "</section>";
  }

  function selectTab(state, tab) {
    state.tab = tab;
    Workbench.requestRender();
    if (tab === "chat" && !state.chatSessionsLoaded) {
      loadChatSessions(state);
    } else if (tab === "permissions" && !state.permissionRulesLoaded) {
      loadPermissionRules(state);
    } else if (tab === "audit" && !state.auditLoaded) {
      loadAudit(state);
    }
  }

  function localDateTimeToRFC3339(value) {
    if (!value) {
      return "";
    }
    var date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toISOString();
  }

  function updateAuditFilter(state, target) {
    var name = target && target.name;
    if (!name || !target.closest('[data-supervision-form="audit"]') ||
      !Object.prototype.hasOwnProperty.call(state.auditFilters, name)) {
      return false;
    }
    state.auditFilters[name] = target.value;
    return true;
  }

  Workbench.registerView({
    id: "ai-chat",
    label: "实时对话",
    section: "AI 中心",
    order: 60,
    createState: createState,
    render: renderStandaloneChat,
    onEnter: function (state) {
      if (!state.chatSessionsLoaded) {
        return loadChatSessions(state);
      }
      return null;
    },
    bind: function (root, state) {
      root.addEventListener("click", function (event) {
        var mode = event.target.closest("[data-chat-mode]");
        if (mode) {
          state.chatMode = mode.getAttribute("data-chat-mode");
          Workbench.requestRender();
          return;
        }
        var action = event.target.closest("[data-chat-action]");
        if (action) {
          if (action.getAttribute("data-chat-action") === "new-session") {
            createChatSession(state, "新对话");
          } else if (action.getAttribute("data-chat-action") === "cancel-turn") {
            cancelPersistentTurn(state);
          }
          return;
        }
        var permission = event.target.closest("[data-chat-permission]");
        if (permission) {
          decideChatPermission(
            state,
            permission.getAttribute("data-chat-permission"),
            permission.getAttribute("data-chat-decision")
          );
        }
      });
      root.addEventListener("input", function (event) {
        if (event.target.name === "text" && event.target.closest('[data-supervision-form="chat"]')) {
          state.chatText = event.target.value;
        } else if (event.target.getAttribute("data-supervision-field") === "operator-id") {
          state.operatorId = event.target.value;
        } else if (event.target.getAttribute("data-supervision-field") === "decision-reason") {
          state.decisionReason = event.target.value;
        }
      });
      root.addEventListener("change", function (event) {
        if (event.target.matches("[data-chat-session]")) {
          loadChatSession(state, event.target.value);
        }
      });
      root.addEventListener("submit", function (event) {
        var form = event.target.closest('[data-supervision-form="chat"]');
        if (!form) {
          return;
        }
        event.preventDefault();
        state.chatText = form.elements.text.value;
        if (state.chatMode === "dry-run") {
          sendTestChat(state);
        } else {
          sendPersistentChat(state);
        }
      });
    }
  });

  Workbench.registerView({
    id: "ai-supervision",
    label: "实时监督",
    section: "AI 中心",
    order: 70,
    createState: createState,
    render: render,
    onEnter: function (state) {
      loadPermissionRequests(state);
      if (!state.streamAttempted) {
        state.streamAttempted = true;
        return connectEventStream(state);
      }
      return null;
    },
    bind: function (root, state) {
      root.addEventListener("click", function (event) {
        var tab = event.target.closest("[data-supervision-tab]");
        if (tab) {
          selectTab(state, tab.getAttribute("data-supervision-tab"));
          return;
        }
        var action = event.target.closest("[data-supervision-action]");
        if (action) {
          var actionName = action.getAttribute("data-supervision-action");
          if (actionName === "reconnect") {
            reconnectEventStream(state);
          } else if (actionName === "refresh-requests") {
            loadPermissionRequests(state);
          } else if (actionName === "refresh-rules") {
            loadPermissionRules(state);
          } else if (actionName === "export-audit") {
            exportAudit(state);
          } else if (actionName === "clear-audit") {
            state.auditFilters = {
              event_type: "",
              run_id: "",
              action_id: "",
              actor_type: "",
              actor_id: "",
              created_after: "",
              created_before: ""
            };
            loadAudit(state);
          } else if (actionName === "new-session") {
            createChatSession(state, "新对话");
          }
          return;
        }
        var chatMode = event.target.closest("[data-chat-mode]");
        if (chatMode) {
          state.chatMode = chatMode.getAttribute("data-chat-mode");
          Workbench.requestRender();
          return;
        }
        var chatAction = event.target.closest("[data-chat-action]");
        if (chatAction) {
          if (chatAction.getAttribute("data-chat-action") === "new-session") {
            createChatSession(state, "新对话");
          } else if (chatAction.getAttribute("data-chat-action") === "cancel-turn") {
            cancelPersistentTurn(state);
          }
          return;
        }
        var chatPermission = event.target.closest("[data-chat-permission]");
        if (chatPermission) {
          decideChatPermission(
            state,
            chatPermission.getAttribute("data-chat-permission"),
            chatPermission.getAttribute("data-chat-decision")
          );
          return;
        }
        var decision = event.target.closest("[data-supervision-decision]");
        if (decision) {
          decidePermission(
            state,
            decision.getAttribute("data-supervision-request"),
            decision.getAttribute("data-supervision-decision")
          );
          return;
        }
        var deleteRule = event.target.closest("[data-supervision-delete-rule]");
        if (deleteRule) {
          deletePermissionRule(state, deleteRule.getAttribute("data-supervision-delete-rule"));
        }
      });

      root.addEventListener("input", function (event) {
        var field = event.target.getAttribute("data-supervision-field");
        if (field === "operator-id") {
          state.operatorId = event.target.value;
        } else if (field === "decision-reason") {
          state.decisionReason = event.target.value;
        } else if (event.target.name === "text" && event.target.closest('[data-supervision-form="chat"]')) {
          state.chatText = event.target.value;
        } else {
          updateAuditFilter(state, event.target);
        }
      });

      root.addEventListener("change", function (event) {
        if (event.target.matches("[data-chat-session]")) {
          loadChatSession(state, event.target.value);
          return;
        }
        updateAuditFilter(state, event.target);
      });

      root.addEventListener("submit", function (event) {
        var form = event.target.closest("[data-supervision-form]");
        if (!form) {
          return;
        }
        event.preventDefault();
        if (form.getAttribute("data-supervision-form") === "chat") {
          state.chatText = form.elements.text.value;
          if (state.chatMode === "dry-run") {
            sendTestChat(state);
          } else {
            sendPersistentChat(state);
          }
          return;
        }
        Object.keys(state.auditFilters).forEach(function (key) {
          var value = form.elements[key].value.trim();
          state.auditFilters[key] = value;
        });
        loadAudit(state);
      });
    }
  });
})();
