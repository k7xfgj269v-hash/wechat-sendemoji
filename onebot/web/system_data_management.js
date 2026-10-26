(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.automationFetch !== "function") {
    return;
  }

  var STATUS_PATH = "/api/system/data/status";
  var EXPORT_PATH = "/api/system/data/export";
  var VALIDATE_PATH = "/api/system/data/validate";

  function createState() {
    return {
      status: null,
      includeAudit: true,
      validation: null,
      filename: "",
      loading: false,
      exporting: false,
      validating: false,
      error: "",
      success: ""
    };
  }

  function request(path, options, responseType) {
    return Workbench.automationFetch(path, options).then(function (response) {
      if (!response.ok) {
        return response.text().then(function (text) {
          var message = response.status + " " + response.statusText;
          try {
            var data = JSON.parse(text);
            message = data.error || message;
          } catch (err) {
            if (text.trim()) {
              message = text.trim();
            }
          }
          throw new Error(message);
        });
      }
      if (responseType === "blob") {
        return response.blob();
      }
      return response.json();
    });
  }

  function renderStatusDetails(state) {
    if (!state.status) {
      return '<div class="systemData-empty">状态尚未加载。</div>';
    }
    var status = state.status;
    return '<dl class="systemData-schema">' +
      '<div><dt>Bundle</dt><dd>v' + Workbench.escapeHTML(status.bundle_schema_version) + '</dd></div>' +
      '<div><dt>CRM</dt><dd>v' + Workbench.escapeHTML(status.crm_schema_version) + '</dd></div>' +
      '<div><dt>Automation</dt><dd>v' + Workbench.escapeHTML(status.automation_schema_version) + '</dd></div>' +
      '<div><dt>Provider</dt><dd>v' + Workbench.escapeHTML(status.provider_schema_version) + '</dd></div>' +
      '<div><dt>Audit</dt><dd>v' + Workbench.escapeHTML(status.audit_schema_version) + '</dd></div>' +
      '<div><dt>Validation limit</dt><dd>' +
      Workbench.escapeHTML(Math.round(Number(status.max_validation_bytes || 0) / 1048576)) + ' MiB</dd></div></dl>';
  }

  function renderValidation(state) {
    if (!state.validation) {
      return '<div class="systemData-empty">选择 ZIP 文件后执行只读校验。</div>';
    }
    var entries = state.validation.manifest && Array.isArray(state.validation.manifest.entries) ?
      state.validation.manifest.entries : [];
    return '<div class="systemData-validationResult"><div class="systemData-resultHead"><strong>校验通过</strong><span>' +
      Workbench.escapeHTML(state.filename) + '</span></div><div class="systemData-entryList" role="list">' +
      entries.map(function (entry) {
        return '<div role="listitem"><code>' + Workbench.escapeHTML(entry.name) +
          '</code><span>v' + Workbench.escapeHTML(entry.schema_version) + ' · ' +
          Workbench.escapeHTML(entry.size) + ' bytes</span></div>';
      }).join("") + "</div></div>";
  }

  function render(state) {
    var busy = state.loading || state.exporting || state.validating;
    return '<div class="systemData"><section class="topbar systemData-topbar"><div><h2>数据管理</h2>' +
      '<p>一致性导出与离线完整性校验</p></div><span data-ready="' +
      Workbench.escapeHTML(state.status && state.status.ready ? "true" : "false") + '">' +
      Workbench.escapeHTML(state.status && state.status.ready ? "Ready" : "Unavailable") +
      '</span></section>' + Workbench.renderStatus(
        state.error,
        state.success,
        state.loading ? "正在读取数据管理状态..." :
          state.exporting ? "正在生成导出文件..." :
            state.validating ? "正在校验数据包..." : ""
      ) + '<div class="systemData-layout"><section class="systemData-panel"><div class="systemData-heading">' +
      '<div><h3>导出</h3><p>CRM、automation 与已清理的 provider/prompt 状态</p></div></div>' +
      renderStatusDetails(state) +
      '<label class="systemData-toggle"><input type="checkbox" data-system-data-audit' +
      (state.includeAudit ? " checked" : "") + (busy ? " disabled" : "") +
      '><span><strong>包含 audit 分区</strong></span></label>' +
      '<div class="systemData-actions"><button class="primary" type="button" data-system-data-export' +
      (busy || !state.status || !state.status.ready ? " disabled" : "") + ">" +
      (state.exporting ? "导出中..." : "导出 ZIP") +
      '</button><button type="button" data-system-data-refresh' + (busy ? " disabled" : "") +
      '>刷新状态</button></div></section><section class="systemData-panel"><div class="systemData-heading">' +
      '<div><h3>校验</h3><p>校验 manifest、大小、路径、schema 与 SHA-256</p></div></div>' +
      '<label class="systemData-file"><span>ZIP 文件</span><input type="file" accept=".zip,application/zip"' +
      ' data-system-data-file' + (busy ? " disabled" : "") + '></label><div class="systemData-actions">' +
      '<button class="primary" type="button" data-system-data-validate' +
      (busy ? " disabled" : "") + ">" + (state.validating ? "校验中..." : "开始校验") +
      "</button></div>" + renderValidation(state) + "</section></div></div>";
  }

  function load(state) {
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return request(STATUS_PATH).then(function (status) {
      state.status = status;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function downloadBundle(state) {
    state.exporting = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return request(EXPORT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ include_audit: state.includeAudit })
    }, "blob").then(function (blob) {
      var url = URL.createObjectURL(blob);
      var link = document.createElement("a");
      link.href = url;
      link.download = "wechat-chatter-data.zip";
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      state.success = "导出文件已生成。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.exporting = false;
      Workbench.requestRender();
    });
  }

  function validateBundle(state, root) {
    var input = root.querySelector("[data-system-data-file]");
    var file = input && input.files ? input.files[0] : null;
    if (!file) {
      state.error = "请选择 ZIP 文件。";
      state.success = "";
      Workbench.requestRender();
      return;
    }
    state.validating = true;
    state.error = "";
    state.success = "";
    state.validation = null;
    state.filename = file.name;
    Workbench.requestRender();
    request(VALIDATE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/zip" },
      body: file
    }).then(function (validation) {
      state.validation = validation;
      state.success = "数据包完整且可识别。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.validating = false;
      Workbench.requestRender();
    });
  }

  function bind(root, state) {
    var audit = root.querySelector("[data-system-data-audit]");
    if (audit) {
      audit.addEventListener("change", function () {
        state.includeAudit = audit.checked;
      });
    }
    var refresh = root.querySelector("[data-system-data-refresh]");
    if (refresh) {
      refresh.addEventListener("click", function () {
        load(state);
      });
    }
    var exportButton = root.querySelector("[data-system-data-export]");
    if (exportButton) {
      exportButton.addEventListener("click", function () {
        downloadBundle(state);
      });
    }
    var validateButton = root.querySelector("[data-system-data-validate]");
    if (validateButton) {
      validateButton.addEventListener("click", function () {
        validateBundle(state, root);
      });
    }
  }

  Workbench.registerView({
    id: "system-data-management",
    label: "数据管理",
    section: "系统",
    order: 170,
    createState: createState,
    render: render,
    onEnter: function (state) {
      if (!state.status && !state.loading) {
        return load(state);
      }
      return null;
    },
    bind: bind
  });
})();
