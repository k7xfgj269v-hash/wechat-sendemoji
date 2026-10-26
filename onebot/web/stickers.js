(function () {
  "use strict";

  // 表情库面板：列出收集/上传的表情，支持搜索、改名打标签、删除、手工上传。
  // 后端见 sticker_routes.go（GET /api/stickers 等），鉴权与 AI 中心其它面板一致
  // （operator token），所以统一走 Workbench.automationAPI / automationFetch。

  var Workbench = window.OneBotWorkbench;
  if (!Workbench || typeof Workbench.registerView !== "function") {
    return;
  }

  var LIST_PATH = "/api/stickers";
  var IMAGE_PATH = "/api/stickers/image";
  var RENAME_PATH = "/api/stickers/rename";
  var DELETE_PATH = "/api/stickers/delete";
  var UPLOAD_PATH = "/api/stickers/upload";

  var NAME_MAX_LENGTH = 12;
  var TAG_MAX_COUNT = 5;
  var THUMB_SIZE = 132;
  var CARD_STYLE = "border:1px solid var(--border);border-radius:var(--radius);background:var(--surface);padding:10px;display:grid;gap:8px;align-content:start";
  var THUMB_STYLE = "display:flex;align-items:center;justify-content:center;height:" + THUMB_SIZE +
    "px;border-radius:calc(var(--radius) - 2px);background:var(--surface-hover);overflow:hidden";
  var IMAGE_STYLE = "max-width:100%;max-height:" + THUMB_SIZE + "px;object-fit:contain";
  var GRID_STYLE = "display:grid;grid-template-columns:repeat(auto-fill,minmax(168px,1fr));gap:12px";
  var META_STYLE = "font-size:12px;color:var(--text-muted, #6b7280);overflow-wrap:anywhere";

  function createState() {
    return {
      items: [],
      settings: null,
      loading: false,
      error: "",
      success: "",
      query: "",
      busy: "",
      editing: "",
      uploadFile: null,
      uploadFileName: "",
      imageURLs: {},
      imagePending: {},
      imageFailed: {}
    };
  }

  function escapeHTML(value) {
    return Workbench.escapeHTML(value);
  }

  function disabled(value) {
    return value ? " disabled" : "";
  }

  function normalizeNumber(value, fallback) {
    var numeric = Number(value);
    return Number.isFinite(numeric) && numeric > 0 ? numeric : fallback;
  }

  function formatBytes(bytes) {
    var value = Number(bytes);
    if (!Number.isFinite(value) || value <= 0) {
      return "0 KB";
    }
    if (value < 1024) {
      return value + " B";
    }
    if (value < 1024 * 1024) {
      return (value / 1024).toFixed(value < 10 * 1024 ? 1 : 0) + " KB";
    }
    return (value / (1024 * 1024)).toFixed(1) + " MB";
  }

  function formatTime(value) {
    var numeric = Number(value);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return "-";
    }
    var date = new Date(numeric);
    if (Number.isNaN(date.getTime())) {
      return "-";
    }
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function itemTitle(item) {
    var name = String(item && item.name || "").trim();
    return name || "未命名表情";
  }

  function itemSearchHaystack(item) {
    var tags = Array.isArray(item && item.tags) ? item.tags : [];
    return [
      String(item && item.name || ""),
      tags.join(" "),
      String(item && item.md5 || ""),
      String(item && item.from || ""),
      String(item && item.summary || "")
    ].join(" ").toLowerCase();
  }

  function matchesQuery(item, query) {
    var needle = String(query || "").trim().toLowerCase();
    if (!needle) {
      return true;
    }
    return itemSearchHaystack(item).indexOf(needle) !== -1;
  }

  function visibleItems(state) {
    return state.items.filter(function (item) {
      return matchesQuery(item, state.query);
    });
  }

  function tagsLabel(item) {
    var tags = Array.isArray(item && item.tags) ? item.tags : [];
    if (!tags.length) {
      return "";
    }
    return tags.map(function (tag) {
      return "#" + tag;
    }).join(" ");
  }

  function readJSONResponse(response) {
    if (!response.ok) {
      return response.text().then(function (text) {
        var message = response.status + " " + response.statusText;
        if (text) {
          try {
            var body = JSON.parse(text);
            if (body && body.error) {
              message = body.error;
            }
          } catch (err) {
            if (text.trim()) {
              message = text.trim();
            }
          }
        }
        throw new Error(message);
      });
    }
    return response.json().catch(function () {
      return null;
    });
  }

  function parseTags(value) {
    return String(value || "")
      .split(/[\s,，、#]+/)
      .map(function (tag) {
        return tag.trim();
      })
      .filter(Boolean)
      .slice(0, TAG_MAX_COUNT);
  }

  function renderSettings(state) {
    var settings = state.settings || {};
    var collectText = settings.enabled === false
      ? "总开关关闭"
      : (settings.collect === false ? "自动收集关闭" : "自动收集开启");
    return "共 " + escapeHTML(state.items.length) + " 张 · " + escapeHTML(collectText) +
      " · 每小时上限 " + escapeHTML(normalizeNumber(settings.max_per_hour, 20)) +
      " · 库上限 " + escapeHTML(normalizeNumber(settings.max_items, 300));
  }

  function renderUploadPanel(state) {
    var uploading = state.busy === "upload";
    var fileName = state.uploadFileName ? escapeHTML(state.uploadFileName) : "未选择文件";
    return '<section class="panel"><div class="panel-header"><h3>上传表情</h3>' +
      '<span>支持 png / gif / jpeg / webp，建议 400×400 以内、1.5 MiB 以内</span></div>' +
      '<div class="panel-body"><form class="form-grid" data-sticker-form="upload">' +
      '<label>图片文件<input type="file" name="file" accept="image/png,image/gif,image/jpeg,image/webp" data-sticker-file' +
      disabled(uploading) + "></label>" +
      '<label>备注（最多 ' + NAME_MAX_LENGTH + ' 个字）<input name="name" maxlength="' + NAME_MAX_LENGTH +
      '" placeholder="例如：委屈巴巴的小猫" autocomplete="off"' + disabled(uploading) + "></label>" +
      '<label>标签（最多 ' + TAG_MAX_COUNT + ' 个，空格或逗号分隔）<input name="tags" autocomplete="off" ' +
      'placeholder="开心 卖萌"' + disabled(uploading) + "></label>" +
      '<div class="form-actions"><button class="primary" type="submit"' + disabled(uploading) + ">" +
      (uploading ? "上传中..." : "上传到表情库") + "</button>" +
      '<span class="status" data-sticker-upload-name>' + fileName + "</span></div>" +
      "</form></div></section>";
  }

  function renderImage(item, state) {
    var md5 = String(item.md5 || "");
    var url = state.imageURLs[md5];
    if (url) {
      return '<img alt="" style="' + IMAGE_STYLE + '" src="' + escapeHTML(url) + '">';
    }
    if (state.imageFailed[md5]) {
      return '<span style="' + META_STYLE + '">读图失败</span>';
    }
    return '<span style="' + META_STYLE + '" data-sticker-thumb="' + escapeHTML(md5) + '">载入中...</span>';
  }

  function renderRenameForm(item, state) {
    var md5 = escapeHTML(item.md5 || "");
    var tags = Array.isArray(item.tags) ? item.tags : [];
    var saving = state.busy === item.md5;
    return '<form class="form-grid" data-sticker-form="rename" data-md5="' + md5 + '">' +
      '<label>备注<input name="name" maxlength="' + NAME_MAX_LENGTH + '" autocomplete="off" value="' +
      escapeHTML(String(item.name || "")) + '"' + disabled(saving) + "></label>" +
      '<label>标签<input name="tags" autocomplete="off" value="' + escapeHTML(tags.join(" ")) + '"' +
      disabled(saving) + "></label>" +
      '<div class="form-actions"><button class="primary" type="submit"' + disabled(saving) + ">" +
      (saving ? "保存中..." : "保存") + "</button>" +
      '<button type="button" data-sticker-action="cancel-edit"' + disabled(saving) + ">取消</button>" +
      "</div></form>";
  }

  function renderCard(item, state) {
    var md5 = String(item.md5 || "");
    var busy = state.busy === md5;
    var meta = escapeHTML(item.width || 0) + "×" + escapeHTML(item.height || 0) +
      " · " + escapeHTML(formatBytes(item.bytes)) +
      " · " + escapeHTML(item.from === "manual" ? "手工" : "收集") +
      " · 用过 " + escapeHTML(item.uses || 0) + " 次";
    var body = state.editing === md5
      ? renderRenameForm(item, state)
      : '<div style="display:flex;gap:6px;flex-wrap:wrap">' +
        '<button type="button" data-sticker-action="edit" data-md5="' + escapeHTML(md5) + '"' +
        disabled(busy) + ">改名</button>" +
        '<button class="danger" type="button" data-sticker-action="delete" data-md5="' +
        escapeHTML(md5) + '"' + disabled(busy) + ">" + (busy ? "处理中..." : "删除") + "</button></div>";
    var summary = String(item.summary || "").trim();
    return '<div data-sticker-card data-sticker-search="' + escapeHTML(itemSearchHaystack(item)) +
      '" style="' + CARD_STYLE + '">' +
      '<div style="' + THUMB_STYLE + '">' + renderImage(item, state) + "</div>" +
      "<strong>" + escapeHTML(itemTitle(item)) + "</strong>" +
      (tagsLabel(item) ? '<div style="' + META_STYLE + '">' + escapeHTML(tagsLabel(item)) + "</div>" : "") +
      '<div style="' + META_STYLE + '">' + meta + "</div>" +
      (summary ? '<div style="' + META_STYLE + '">' + escapeHTML(summary) + "</div>" : "") +
      '<div style="' + META_STYLE + '">加入 ' + escapeHTML(formatTime(item.at)) +
      (item.lastUsed ? " · 最近发送 " + escapeHTML(formatTime(item.lastUsed)) : "") + "</div>" +
      body + "</div>";
  }

  function render(state) {
    var items = visibleItems(state);
    var cards = items.map(function (item) {
      return renderCard(item, state);
    }).join("");
    var empty = state.items.length === 0
      ? '<section class="panel"><div class="panel-body">' +
        '<div class="empty-module" role="status"><strong>表情库还是空的</strong>' +
        "<span>把收到的表情自动收进来，或在上面手工上传一张。</span></div></div></section>"
      : "";
    return '<div class="stickersView">' +
      '<section class="topbar"><div><h2>表情库</h2><p>收到的表情自动入库，可改名打标签；发送时会按备注/标签挑一张。</p></div>' +
      '<button type="button" data-sticker-action="refresh"' + disabled(state.loading) + ">" +
      (state.loading ? "刷新中..." : "刷新") + "</button></section>" +
      Workbench.renderStatus(state.error, state.success, state.loading ? "正在读取表情库..." : "") +
      renderUploadPanel(state) +
      empty +
      '<section class="panel"><div class="panel-header"><h3>已收录</h3>' +
      '<span data-sticker-settings>' + renderSettings(state) + "</span></div>" +
      '<div class="panel-body">' +
      '<div class="toolbar"><label>搜索<input data-sticker-query autocomplete="off" placeholder="备注 / 标签 / md5" value="' +
      escapeHTML(state.query) + '"></label>' +
      '<div class="actions"><span class="status">当前显示 <span data-sticker-visible>' + escapeHTML(items.length) +
      "</span> / " + escapeHTML(state.items.length) + " 张</span></div></div>" +
      (items.length ? '<div style="' + GRID_STYLE + '">' + cards + "</div>"
        : '<div class="empty-module" role="status" data-sticker-noresult><strong>没有匹配的表情</strong>' +
          "<span>换个关键词试试。</span></div>") +
      "</div></section></div>";
  }

  function loadLibrary(state) {
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.automationAPI(LIST_PATH).then(function (body) {
      state.items = body && Array.isArray(body.items) ? body.items : [];
      state.settings = body && body.settings ? body.settings : null;
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function loadImage(state, md5) {
    if (!md5 || state.imageURLs[md5] || state.imagePending[md5] || state.imageFailed[md5]) {
      return Promise.resolve();
    }
    state.imagePending[md5] = true;
    return Workbench.automationFetch(IMAGE_PATH + "?md5=" + encodeURIComponent(md5)).then(function (response) {
      if (!response.ok) {
        throw new Error(response.status + " " + response.statusText);
      }
      return response.blob();
    }).then(function (blob) {
      state.imageURLs[md5] = URL.createObjectURL(blob);
    }).catch(function (err) {
      state.imageFailed[md5] = true;
      console.error("sticker image load failed", md5, err);
    }).finally(function () {
      delete state.imagePending[md5];
      Workbench.requestRender();
    });
  }

  function queueVisibleImages(root, state) {
    var thumbs = root.querySelectorAll("[data-sticker-thumb]");
    if (!thumbs.length) {
      return;
    }
    var load = function (md5) {
      loadImage(state, md5);
    };
    if (typeof window.IntersectionObserver !== "function") {
      Array.prototype.forEach.call(thumbs, function (thumb) {
        load(thumb.getAttribute("data-sticker-thumb"));
      });
      return;
    }
    var observer = new window.IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) {
          return;
        }
        observer.unobserve(entry.target);
        load(entry.target.getAttribute("data-sticker-thumb"));
      });
    }, { rootMargin: "240px 0px" });
    Array.prototype.forEach.call(thumbs, function (thumb) {
      observer.observe(thumb);
    });
  }

  function applyFilter(root, state) {
    var needle = String(state.query || "").trim().toLowerCase();
    var visible = 0;
    Array.prototype.forEach.call(root.querySelectorAll("[data-sticker-card]"), function (card) {
      var haystack = String(card.getAttribute("data-sticker-search") || "");
      var matched = !needle || haystack.indexOf(needle) !== -1;
      card.style.display = matched ? "" : "none";
      if (matched) {
        visible += 1;
      }
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-sticker-visible]"), function (node) {
      node.textContent = String(visible);
    });
    var noResult = root.querySelector("[data-sticker-noresult]");
    if (noResult) {
      noResult.hidden = visible > 0;
    }
  }

  function submitRename(state, form) {
    var md5 = String(form.getAttribute("data-md5") || "");
    if (!md5) {
      return Promise.resolve();
    }
    var name = String(form.elements.name ? form.elements.name.value : "").trim();
    var tags = form.elements.tags ? parseTags(form.elements.tags.value) : [];
    state.busy = md5;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(RENAME_PATH, {
      method: "POST",
      body: { md5: md5, name: name, tags: tags }
    }).then(function () {
      state.success = "备注已保存。";
      state.editing = "";
      return loadLibrary(state);
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.busy = "";
      Workbench.requestRender();
    });
  }

  function deleteSticker(state, md5) {
    if (!md5) {
      return Promise.resolve();
    }
    if (!window.confirm("删除这张表情？库里的文件会一起删掉。")) {
      return Promise.resolve();
    }
    state.busy = md5;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationAPI(DELETE_PATH, {
      method: "POST",
      body: { md5: md5 }
    }).then(function () {
      state.success = "已删除 1 张表情。";
      if (state.imageURLs[md5]) {
        URL.revokeObjectURL(state.imageURLs[md5]);
      }
      delete state.imageURLs[md5];
      delete state.imageFailed[md5];
      if (state.editing === md5) {
        state.editing = "";
      }
      return loadLibrary(state);
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.busy = "";
      Workbench.requestRender();
    });
  }

  function submitUpload(state, form) {
    var picked = state.uploadFile;
    if (!picked && form.elements.file && form.elements.file.files) {
      picked = form.elements.file.files[0] || null;
    }
    if (!picked) {
      state.error = "请先选择要上传的图片。";
      Workbench.requestRender();
      return Promise.resolve();
    }
    var body = new FormData();
    body.append("file", picked, picked.name || "sticker");
    var name = String(form.elements.name ? form.elements.name.value : "").trim();
    var tags = form.elements.tags ? parseTags(form.elements.tags.value) : [];
    if (name) {
      body.append("name", name);
    }
    if (tags.length) {
      body.append("tags", tags.join(","));
    }
    state.busy = "upload";
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.automationFetch(UPLOAD_PATH, { method: "POST", body: body })
      .then(readJSONResponse).then(function (payload) {
        state.success = payload && payload.existed
          ? "库里已经有这张表情，未重复添加。"
          : "已上传 1 张表情。";
        state.uploadFile = null;
        state.uploadFileName = "";
        return loadLibrary(state);
      }).catch(function (err) {
        state.error = err.message;
      }).finally(function () {
        state.busy = "";
        Workbench.requestRender();
      });
  }

  function bind(root, state) {
    if (!root) {
      return;
    }
    Array.prototype.forEach.call(root.querySelectorAll("[data-sticker-action]"), function (button) {
      button.addEventListener("click", function () {
        var action = button.getAttribute("data-sticker-action");
        var md5 = String(button.getAttribute("data-md5") || "");
        if (action === "refresh") {
          loadLibrary(state);
        } else if (action === "edit") {
          state.editing = md5;
          state.error = "";
          state.success = "";
          Workbench.requestRender();
        } else if (action === "cancel-edit") {
          state.editing = "";
          Workbench.requestRender();
        } else if (action === "delete") {
          deleteSticker(state, md5);
        }
      });
    });

    var uploadForm = root.querySelector("form[data-sticker-form='upload']");
    if (uploadForm) {
      uploadForm.addEventListener("submit", function (event) {
        event.preventDefault();
        submitUpload(state, uploadForm);
      });
    }

    var fileInput = root.querySelector("[data-sticker-file]");
    if (fileInput) {
      fileInput.addEventListener("change", function () {
        var file = fileInput.files && fileInput.files[0] ? fileInput.files[0] : null;
        state.uploadFile = file;
        state.uploadFileName = file ? file.name : "";
        var label = root.querySelector("[data-sticker-upload-name]");
        if (label) {
          label.textContent = state.uploadFileName || "未选择文件";
        }
      });
    }

    var renameForm = root.querySelector("form[data-sticker-form='rename']");
    if (renameForm) {
      renameForm.addEventListener("submit", function (event) {
        event.preventDefault();
        submitRename(state, renameForm);
      });
    }

    var queryInput = root.querySelector("[data-sticker-query]");
    if (queryInput) {
      queryInput.addEventListener("input", function () {
        state.query = queryInput.value;
        applyFilter(root, state);
      });
    }

    applyFilter(root, state);
    queueVisibleImages(root, state);
  }

  function onEnter(state) {
    if (state.loading) {
      return null;
    }
    if (state.items.length === 0) {
      return loadLibrary(state);
    }
    return null;
  }

  Workbench.registerView({
    id: "stickers",
    label: "表情库",
    section: "AI 中心",
    order: 50,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  });
})();
