(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.automationFetch !== "function") {
    return;
  }

  var METRICS_PATH = "/api/system/metrics";
  var QUEUE_LABELS = {
    automation: "自动化",
    permission: "权限审核",
    batch: "批处理",
    service: "服务请求"
  };

  function createState() {
    return {
      snapshot: null,
      loading: false,
      error: ""
    };
  }

  function requestMetrics() {
    return Workbench.automationFetch(METRICS_PATH).then(function (response) {
      if (!response.ok) {
        return response.text().then(function (text) {
          var message = response.status + " " + response.statusText;
          try {
            var body = JSON.parse(text);
            message = body.error || message;
          } catch (err) {
            if (text.trim()) {
              message = text.trim();
            }
          }
          throw new Error(message);
        });
      }
      return response.json();
    });
  }

  function formatDuration(seconds) {
    var value = Number(seconds || 0);
    if (!Number.isFinite(value) || value <= 0) {
      return "0 秒";
    }
    if (value < 60) {
      return Math.round(value) + " 秒";
    }
    if (value < 3600) {
      return Math.round(value / 60) + " 分钟";
    }
    return (value / 3600).toFixed(value < 36000 ? 1 : 0) + " 小时";
  }

  function formatLatency(provider) {
    var count = Number(provider && provider.latency_count || 0);
    var sum = Number(provider && provider.latency_sum_ms || 0);
    return count > 0 ? Math.round(sum / count) + " ms" : "无样本";
  }

  function renderProviders(snapshot) {
    var providers = snapshot && Array.isArray(snapshot.providers) ? snapshot.providers : [];
    if (!providers.length) {
      return '<div class="automationMetrics-empty">尚无 provider 请求样本。</div>';
    }
    return '<div class="automationMetrics-table" role="table"><div class="automationMetrics-tableHead" role="row">' +
      '<span>Provider</span><span>请求</span><span>错误</span><span>取消</span><span>平均延迟</span></div>' +
      providers.map(function (provider) {
        return '<div class="automationMetrics-tableRow" role="row"><strong>' +
          Workbench.escapeHTML(provider.provider) + '</strong><span>' +
          Workbench.escapeHTML(provider.requests) + '</span><span>' +
          Workbench.escapeHTML(provider.errors) + '</span><span>' +
          Workbench.escapeHTML(provider.cancellations) + '</span><span>' +
          Workbench.escapeHTML(formatLatency(provider)) + "</span></div>";
      }).join("") + "</div>";
  }

  function renderQueues(snapshot) {
    var queues = snapshot && Array.isArray(snapshot.queues) ? snapshot.queues : [];
    return '<div class="automationMetrics-queueGrid">' + queues.map(function (queue) {
      return '<div class="automationMetrics-queue"><span>' +
        Workbench.escapeHTML(QUEUE_LABELS[queue.kind] || queue.kind) +
        '</span><strong>' + Workbench.escapeHTML(queue.depth) +
        '</strong><small>最久等待 ' +
        Workbench.escapeHTML(formatDuration(queue.oldest_age_seconds)) +
        "</small></div>";
    }).join("") + "</div>";
  }

  function renderActions(snapshot) {
    var actions = snapshot && Array.isArray(snapshot.actions) ? snapshot.actions : [];
    if (!actions.length) {
      return '<div class="automationMetrics-empty">尚无终态 action。</div>';
    }
    return '<div class="automationMetrics-actionList">' + actions.map(function (action) {
      return '<div><code>' + Workbench.escapeHTML(action.tool) + '</code><span>' +
        Workbench.escapeHTML(action.outcome) + '</span><strong>' +
        Workbench.escapeHTML(action.count) + "</strong></div>";
    }).join("") + "</div>";
  }

  function cancellationCount(snapshot, kind) {
    var entries = snapshot && Array.isArray(snapshot.cancellations) ? snapshot.cancellations : [];
    var item = entries.find(function (entry) {
      return entry.kind === kind;
    });
    return item ? item.count : 0;
  }

  function render(state) {
    var snapshot = state.snapshot || {};
    var connected = !!(snapshot.agent && snapshot.agent.connected);
    var collectedAt = snapshot.collected_at ? new Date(snapshot.collected_at).toLocaleString("zh-CN", {
      hour12: false
    }) : "尚未采集";
    return '<div class="automationMetrics"><section class="topbar automationMetrics-topbar"><div>' +
      '<h2>运行指标</h2><p>自动化、队列与 agent 运行状态</p></div>' +
      '<button type="button" data-automation-metrics-refresh' + (state.loading ? " disabled" : "") + ">" +
      (state.loading ? "刷新中..." : "刷新") + '</button></section>' +
      Workbench.renderStatus(state.error, "", state.loading ? "正在读取运行指标..." : "") +
      '<section class="automationMetrics-summary"><div><span>Agent</span><strong data-connected="' +
      Workbench.escapeHTML(connected) + '">' + (connected ? "已连接" : "未连接") +
      '</strong></div><div><span>Chat 取消</span><strong>' +
      Workbench.escapeHTML(cancellationCount(snapshot, "chat")) +
      '</strong></div><div><span>Provider 取消</span><strong>' +
      Workbench.escapeHTML(cancellationCount(snapshot, "provider")) +
      '</strong></div><div><span>采集时间</span><strong>' +
      Workbench.escapeHTML(collectedAt) + '</strong></div></section>' +
      '<section class="automationMetrics-section"><div class="automationMetrics-heading"><h3>队列</h3>' +
      '<span>深度与最久等待</span></div>' + renderQueues(snapshot) + '</section>' +
      '<section class="automationMetrics-section"><div class="automationMetrics-heading"><h3>Provider</h3>' +
      '<span>请求、失败、取消与延迟</span></div>' + renderProviders(snapshot) + '</section>' +
      '<section class="automationMetrics-section"><div class="automationMetrics-heading"><h3>Action 结果</h3>' +
      '<span>闭合工具枚举与终态</span></div>' + renderActions(snapshot) + "</section></div>";
  }

  function load(state) {
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return requestMetrics().then(function (snapshot) {
      state.snapshot = snapshot;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function bind(root, state) {
    var refresh = root.querySelector("[data-automation-metrics-refresh]");
    if (refresh) {
      refresh.addEventListener("click", function () {
        load(state);
      });
    }
  }

  Workbench.registerView({
    id: "system-metrics",
    label: "运行指标",
    section: "系统",
    order: 165,
    createState: createState,
    render: render,
    onEnter: function (state) {
      return load(state);
    },
    bind: bind
  });
})();
