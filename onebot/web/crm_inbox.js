(function () {
  "use strict";

  var Workbench = window.OneBotWorkbench;
  var MESSAGE_EVENTS_PATH = "/api/crm/message_events";
  var MESSAGE_EVENT_ITEM_PREFIX = "/api/crm/message_events/";
  var CANDIDATES_PATH = "/api/crm/candidates";
  var MESSAGE_LINKS_PATH = "/api/crm/message_candidate_links";
  var PAGE_SIZE = 50;
  var CANDIDATE_PAGE_SIZE = 25;

  function createCandidateForm() {
    return {
      full_name: "",
      wechat_id: "",
      phone: "",
      email: "",
      location: "",
      source: "",
      skills: "",
      owner: "",
      status: "active",
      summary: ""
    };
  }

  function createState() {
    return {
      filters: {
        review_status: "",
        chat_type: "",
        candidate_id: "",
        linked: "",
        q: ""
      },
      limit: PAGE_SIZE,
      offset: 0,
      total: 0,
      events: [],
      ingestionTotals: {},
      recentDecisions: [],
      selectedId: "",
      detail: null,
      loading: false,
      detailLoading: false,
      savingReview: false,
      linking: false,
      error: "",
      success: "",
      candidateQuery: "",
      candidateTotal: 0,
      candidates: [],
      candidateLoading: false,
      selectedCandidateId: "",
      linkMode: "existing",
      candidateForm: createCandidateForm()
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

  function selectedEvent(state) {
    if (state.detail && state.detail.id === state.selectedId) {
      return state.detail;
    }
    return state.events.find(function (event) {
      return event.id === state.selectedId;
    }) || null;
  }

  function updateEvent(state, updated) {
    if (!updated || !updated.id) {
      return;
    }
    state.events = state.events.map(function (event) {
      return event.id === updated.id ? updated : event;
    });
    if (state.selectedId === updated.id) {
      state.detail = updated;
    }
  }

  function normalizeCandidate(item) {
    return item && item.candidate ? item.candidate : item;
  }

  function formatTime(value) {
    var numeric = Number(value);
    if (!Number.isFinite(numeric)) {
      return String(value == null ? "" : value);
    }
    if (Math.abs(numeric) < 1000000000000) {
      numeric *= 1000;
    }
    var date = new Date(numeric);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function eventSummary(event) {
    var text = String(event && event.text || "").replace(/\s+/g, " ").trim();
    return text || "无文本内容";
  }

  function ingestionRecord(event) {
    var ingestion = event && event.job_ingestion;
    return ingestion && typeof ingestion === "object" ? ingestion : {};
  }

  function eventProvenance(event) {
    var ingestion = ingestionRecord(event);
    var provenance = ingestion.provenance;
    return provenance && typeof provenance === "object" ? provenance : null;
  }

  function eventIngestionDecision(event) {
    var ingestion = ingestionRecord(event);
    return String(ingestion.decision || "");
  }

  function ingestionDecisionLabel(decision) {
    var labels = {
      create: "创建职位",
      update: "更新职位",
      duplicate: "重复需求",
      noop: "内容无变化",
      noise: "群聊噪声",
      failure: "摄取失败"
    };
    return labels[decision] || decision || "未决策";
  }

  function dispositionLabel(disposition) {
    var labels = {
      private_candidate: "私聊候选人",
      source_group_job: "权威职位来源",
      group_noise: "非职位群消息"
    };
    return labels[disposition] || disposition || "未分类";
  }

  function renderFilters(state) {
    var filters = state.filters;
    return '<form class="crmInboxFilters" data-crm-inbox-form="filters">' +
      '<label class="crmInboxSearchField">搜索消息<input name="q" value="' + escapeHTML(filters.q) + '" autocomplete="off" placeholder="消息正文"></label>' +
      '<label>审核状态<select name="review_status">' +
      '<option value="">全部</option>' +
      '<option value="unreviewed"' + selected("unreviewed", filters.review_status) + '>未审核</option>' +
      '<option value="reviewed"' + selected("reviewed", filters.review_status) + '>已审核</option>' +
      "</select></label>" +
      '<label>聊天类型<select name="chat_type">' +
      '<option value="">全部</option>' +
      '<option value="private"' + selected("private", filters.chat_type) + '>私聊</option>' +
      '<option value="group"' + selected("group", filters.chat_type) + '>群聊</option>' +
      "</select></label>" +
      '<label>关联状态<select name="linked">' +
      '<option value="">全部</option>' +
      '<option value="true"' + selected("true", filters.linked) + '>已关联</option>' +
      '<option value="false"' + selected("false", filters.linked) + '>未关联</option>' +
      "</select></label>" +
      '<label>候选人 ID<input name="candidate_id" value="' + escapeHTML(filters.candidate_id) + '" autocomplete="off" placeholder="candidate-..."></label>' +
      '<button class="crmInboxButtonPrimary" type="submit"' + disabled(state.loading) + '>筛选</button>' +
      "</form>";
  }

  function renderEventList(state) {
    if (!state.events.length) {
      return '<div class="crmInboxEmpty" role="status">当前筛选条件下没有消息事件。</div>';
    }
    return '<div class="crmInboxEventList" role="list">' + state.events.map(function (event) {
      var reviewLabel = event.review_status === "reviewed" ? "已审核" : "未审核";
      var linkLabel = event.candidate_id ? "已关联" : "未关联";
      var decision = eventIngestionDecision(event);
      return '<button class="crmInboxEventRow" type="button" role="listitem" data-crm-inbox-event-id="' + escapeHTML(event.id) + '" aria-current="' + (state.selectedId === event.id ? "true" : "false") + '">' +
        '<span class="crmInboxEventTop"><strong>' + escapeHTML(eventSummary(event)) + '</strong><time>' + escapeHTML(formatTime(event.timestamp)) + "</time></span>" +
        '<span class="crmInboxEventMeta">' +
        '<span>' + escapeHTML(event.chat_type || "未知类型") + "</span>" +
        '<span>' + escapeHTML(reviewLabel) + "</span>" +
        '<span>' + escapeHTML(linkLabel) + "</span>" +
        '<span>' + escapeHTML(dispositionLabel(event.disposition)) + "</span>" +
        (decision ? '<span class="crmInboxDecision crmInboxDecision-' + escapeHTML(decision) + '">' +
          escapeHTML(ingestionDecisionLabel(decision)) + "</span>" : "") +
        (event.suspicious ? '<span class="crmInboxFlag">可疑</span>' : "") +
        "</span></button>";
    }).join("") + "</div>";
  }

  function renderPager(state) {
    var start = state.total ? state.offset + 1 : 0;
    var end = Math.min(state.total, state.offset + state.events.length);
    return '<div class="crmInboxPager">' +
      '<span>' + escapeHTML("显示 " + start + "-" + end + " / 共 " + state.total + " 条") + "</span>" +
      '<div class="crmInboxPagerActions">' +
      '<button type="button" data-crm-inbox-action="previous"' + disabled(state.loading || state.offset === 0) + '>上一页</button>' +
      '<button type="button" data-crm-inbox-action="next"' + disabled(state.loading || state.offset + state.limit >= state.total) + '>下一页</button>' +
      "</div></div>";
  }

  function renderKinds(kinds) {
    if (!Array.isArray(kinds) || !kinds.length) {
      return '<span class="crmInboxMuted">无</span>';
    }
    return '<span class="crmInboxChips">' + kinds.map(function (kind) {
      return '<span class="crmInboxChip">' + escapeHTML(kind) + "</span>";
    }).join("") + "</span>";
  }

  function renderDetailRow(label, value) {
    var shown = value === 0 || value ? value : "无";
    return "<dt>" + escapeHTML(label) + "</dt><dd>" + escapeHTML(shown) + "</dd>";
  }

  function renderReviewActions(state, event) {
    var linked = !!event.candidate_id;
    return '<div class="crmInboxActionRow" aria-label="消息审核操作">' +
      '<button type="button" data-crm-inbox-review="reviewed"' + disabled(state.savingReview || event.review_status === "reviewed") + '>标记已审核</button>' +
      '<button type="button" data-crm-inbox-review="unreviewed"' + disabled(state.savingReview || event.review_status === "unreviewed" || linked) + '>标记未审核</button>' +
      "</div>";
  }

  function renderCandidateResults(state) {
    if (state.candidateLoading && !state.candidates.length) {
      return '<div class="crmInboxEmpty">正在搜索候选人...</div>';
    }
    if (!state.candidates.length) {
      return '<div class="crmInboxEmpty">没有已加载的候选人。</div>';
    }
    return '<div class="crmInboxCandidateList" role="list">' + state.candidates.map(function (item) {
      var candidate = normalizeCandidate(item) || {};
      var meta = [candidate.wechat_id, candidate.phone, candidate.email].filter(Boolean).join(" · ");
      return '<button class="crmInboxCandidateRow" type="button" role="listitem" data-crm-inbox-candidate-id="' + escapeHTML(candidate.id) + '" aria-current="' + (state.selectedCandidateId === candidate.id ? "true" : "false") + '">' +
        '<strong>' + escapeHTML(candidate.full_name || "未命名候选人") + "</strong>" +
        '<span>' + escapeHTML(meta || candidate.id || "无标识") + "</span>" +
        "</button>";
    }).join("") + "</div>";
  }

  function renderExistingCandidateLink(state, event) {
    return '<div class="crmInboxLinkPane">' +
      '<form class="crmInboxCandidateSearch" data-crm-inbox-form="candidate-search">' +
      '<label>候选人搜索<input name="candidate_query" value="' + escapeHTML(state.candidateQuery) + '" autocomplete="off" placeholder="姓名、微信、电话、邮箱"></label>' +
      '<button type="submit"' + disabled(state.candidateLoading || state.linking) + '>搜索</button>' +
      "</form>" +
      '<div class="crmInboxResultCount">' + escapeHTML("匹配 " + state.candidateTotal + " 位候选人") + "</div>" +
      renderCandidateResults(state) +
      '<button class="crmInboxButtonPrimary crmInboxFullButton" type="button" data-crm-inbox-action="link-existing"' +
      disabled(state.linking || !state.selectedCandidateId || !!event.candidate_id) + '>关联所选候选人</button>' +
      "</div>";
  }

  function renderCreateCandidateForm(state, event) {
    var form = state.candidateForm;
    return '<form class="crmInboxCandidateForm" data-crm-inbox-form="candidate-create">' +
      '<label>姓名<input name="full_name" required value="' + escapeHTML(form.full_name) + '" autocomplete="off"></label>' +
      '<label>微信号<input name="wechat_id" value="' + escapeHTML(form.wechat_id) + '" autocomplete="off"></label>' +
      '<label>电话<input name="phone" value="' + escapeHTML(form.phone) + '" autocomplete="off"></label>' +
      '<label>邮箱<input name="email" type="email" value="' + escapeHTML(form.email) + '" autocomplete="off"></label>' +
      '<label>所在地<input name="location" value="' + escapeHTML(form.location) + '" autocomplete="off"></label>' +
      '<label>来源<input name="source" value="' + escapeHTML(form.source) + '" autocomplete="off"></label>' +
      '<label class="crmInboxWideField">技能<textarea name="skills" placeholder="每行一个，或用逗号分隔">' + escapeHTML(form.skills) + "</textarea></label>" +
      '<label>负责人<input name="owner" value="' + escapeHTML(form.owner) + '" autocomplete="off"></label>' +
      '<label>状态<select name="status">' +
      '<option value="active"' + selected("active", form.status) + '>活跃</option>' +
      '<option value="paused"' + selected("paused", form.status) + '>暂停</option>' +
      '<option value="placed"' + selected("placed", form.status) + '>已入职</option>' +
      '<option value="archived"' + selected("archived", form.status) + '>已归档</option>' +
      "</select></label>" +
      '<label class="crmInboxWideField">摘要<textarea name="summary">' + escapeHTML(form.summary) + "</textarea></label>" +
      '<button class="crmInboxButtonPrimary crmInboxWideField" type="submit"' + disabled(state.linking || !!event.candidate_id) + '>创建并关联</button>' +
      "</form>";
  }

  function renderLinkSection(state, event) {
    if (event.candidate_id) {
      return '<section class="crmInboxLinkSection" aria-label="候选人关联">' +
        '<div class="crmInboxSectionHeading"><h4>候选人关联</h4><span class="crmInboxLinkedState">已关联</span></div>' +
        '<p class="crmInboxLinkedCandidate">' + escapeHTML(event.candidate_id) + "</p></section>";
    }
    return '<section class="crmInboxLinkSection" aria-label="候选人关联">' +
      '<div class="crmInboxSectionHeading"><h4>候选人关联</h4><span>未关联</span></div>' +
      '<div class="crmInboxSegmented" role="group" aria-label="候选人关联方式">' +
      '<button type="button" data-crm-inbox-link-mode="existing" aria-pressed="' + (state.linkMode === "existing") + '">已有候选人</button>' +
      '<button type="button" data-crm-inbox-link-mode="create" aria-pressed="' + (state.linkMode === "create") + '">手工新建</button>' +
      "</div>" +
      (state.linkMode === "create" ? renderCreateCandidateForm(state, event) : renderExistingCandidateLink(state, event)) +
      "</section>";
  }

  function renderDetail(state) {
    var event = selectedEvent(state);
    if (!event) {
      return '<div class="crmInboxEmpty crmInboxDetailEmpty">选择一条消息后查看详情并处理。</div>';
    }
    return '<div class="crmInboxDetail">' +
      '<div class="crmInboxDetailHeader"><div><h3>消息详情</h3><span>' + escapeHTML(event.id) + '</span></div><span class="crmInboxReviewState">' + escapeHTML(event.review_status === "reviewed" ? "已审核" : "未审核") + "</span></div>" +
      '<div class="crmInboxMessageText">' + escapeHTML(event.text || "无文本内容") + "</div>" +
      '<dl class="crmInboxDetails">' +
      renderDetailRow("来源", event.source) +
      renderDetailRow("聊天类型", event.chat_type) +
      renderDetailRow("会话对象", event.peer_id) +
      renderDetailRow("作者", event.author_id) +
      renderDetailRow("群聊", event.group_id) +
      renderDetailRow("时间", formatTime(event.timestamp)) +
      "<dt>消息类型</dt><dd>" + renderKinds(event.kinds) + "</dd>" +
      renderDetailRow("可疑内容", event.suspicious ? "是" : "否") +
      renderDetailRow("审核状态", event.review_status) +
      renderDetailRow("关联状态", event.candidate_id ? "已关联" : "未关联") +
      "</dl>" +
      renderIngestionDetail(event) +
      renderReviewActions(state, event) +
      renderLinkSection(state, event) +
      "</div>";
  }

  function renderIngestionDetail(event) {
    var provenance = eventProvenance(event);
    var decision = eventIngestionDecision(event);
    var authority = event.disposition === "source_group_job" ? "已确认权威来源" :
      event.disposition === "group_noise" ? "已判定为噪声" : "非来源组职位";
    if (!provenance && !decision && !event.disposition) {
      return '<section class="crmInboxIngestion"><div class="crmInboxSectionHeading"><h4>职位摄取</h4>' +
        '<span>暂无决策</span></div></section>';
    }
    return '<section class="crmInboxIngestion" aria-label="职位摄取与不可变来源记录">' +
      '<div class="crmInboxSectionHeading"><h4>职位摄取</h4><span class="crmInboxDecision crmInboxDecision-' +
      escapeHTML(decision || "pending") + '">' + escapeHTML(ingestionDecisionLabel(decision)) + "</span></div>" +
      '<dl class="crmInboxDetails">' +
      renderDetailRow("来源权威性", authority) +
      renderDetailRow("消息处置", dispositionLabel(event.disposition)) +
      renderDetailRow("Provenance ID", provenance && provenance.id) +
      renderDetailRow("职位 ID", provenance && provenance.job_id) +
      renderDetailRow("职位修订", provenance && provenance.job_revision) +
      renderDetailRow("需求编号", provenance && provenance.demand_code) +
      renderDetailRow("来源群", provenance && provenance.group_id || event.group_id) +
      renderDetailRow("来源作者", provenance && provenance.author_id || event.author_id) +
      renderDetailRow("来源消息键", provenance && provenance.message_key || event.message_key) +
      renderDetailRow("来源时间", provenance && provenance.source_timestamp) +
      renderDetailRow("内容 SHA-256", provenance && provenance.content_sha256) +
      renderDetailRow("规范指纹", provenance && provenance.canonical_fingerprint) +
      renderDetailRow("记录时间", provenance && provenance.created_at) +
      "</dl></section>";
  }

  function renderIngestionSummary(state) {
    var totals = state.ingestionTotals || {};
    var keys = ["create", "update", "duplicate", "noop", "noise", "failure"];
    var totalHTML = '<div class="crmInboxIngestionTotals">' + keys.map(function (decision) {
      return '<span><strong>' + escapeHTML(Number(totals[decision] || 0)) + '</strong>' +
        escapeHTML(ingestionDecisionLabel(decision)) + "</span>";
    }).join("") + "</div>";
    var recentHTML = state.recentDecisions.length ? '<ol class="crmInboxRecentDecisions">' +
      state.recentDecisions.map(function (item) {
        var provenance = item || {};
        return '<li><span class="crmInboxDecision crmInboxDecision-' +
          escapeHTML(provenance.decision || "pending") + '">' +
          escapeHTML(ingestionDecisionLabel(provenance.decision)) + '</span><strong>' +
          escapeHTML(provenance.demand_code || provenance.job_id || "无职位") +
          '</strong><span>' + escapeHTML(provenance.group_id || "无来源群") + "</span></li>";
      }).join("") + "</ol>" : '<div class="crmInboxEmpty">暂无最近摄取决策。</div>';
    return '<section class="crmInboxIngestionSummary" aria-label="职位摄取摘要"><div class="crmInboxSectionHeading">' +
      '<h3>职位摄取</h3><span>append-only provenance</span></div>' + totalHTML + recentHTML + "</section>";
  }

  function render(state) {
    var busyText = "";
    if (state.loading) {
      busyText = "正在加载消息事件...";
    } else if (state.detailLoading) {
      busyText = "正在刷新消息详情...";
    } else if (state.savingReview) {
      busyText = "正在更新审核状态...";
    } else if (state.linking) {
      busyText = "正在关联候选人...";
    }
    return '<section class="crmInboxTopbar">' +
      '<div><h2>消息收件箱</h2><p>集中审核微信消息，并按人工判断关联候选人。</p></div>' +
      '<button type="button" data-crm-inbox-action="reload"' + disabled(state.loading) + '>刷新</button>' +
      "</section>" +
      '<div class="crmInboxStatus">' + Workbench.renderStatus(state.error, state.success, busyText) + "</div>" +
      renderIngestionSummary(state) +
      '<section class="crmInboxLayout" aria-label="消息收件箱工作区">' +
      '<div class="crmInboxPanel crmInboxListPanel">' +
      '<div class="crmInboxPanelHeader"><h3>消息事件</h3><span>' + escapeHTML(state.total + " 条") + "</span></div>" +
      '<div class="crmInboxPanelBody">' + renderFilters(state) + renderEventList(state) + renderPager(state) + "</div></div>" +
      '<div class="crmInboxPanel crmInboxDetailPanel">' +
      '<div class="crmInboxPanelBody">' + renderDetail(state) + "</div></div>" +
      "</section>";
  }

  function loadEvents(state) {
    state.loading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api(MESSAGE_EVENTS_PATH + Workbench.encodeQuery({
      review_status: state.filters.review_status,
      chat_type: state.filters.chat_type,
      candidate_id: state.filters.candidate_id,
      linked: state.filters.linked,
      q: state.filters.q,
      limit: state.limit,
      offset: state.offset
    })).then(function (data) {
      state.total = Number(data && data.total || 0);
      state.events = data && Array.isArray(data.events) ? data.events : [];
      state.ingestionTotals = data && data.ingestion_totals || {};
      state.recentDecisions = data && Array.isArray(data.recent_decisions) ? data.recent_decisions : [];
      if (!state.selectedId && state.events.length) {
        state.selectedId = state.events[0].id;
        state.detail = state.events[0];
      } else if (state.selectedId && (!state.detail || state.detail.id !== state.selectedId)) {
        var current = state.events.find(function (event) {
          return event.id === state.selectedId;
        });
        if (current) {
          state.detail = current;
        }
      }
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.loading = false;
      Workbench.requestRender();
    });
  }

  function loadEventDetail(state, id) {
    state.detailLoading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api(MESSAGE_EVENT_ITEM_PREFIX + encodeURIComponent(id)).then(function (event) {
      if (state.selectedId === id) {
        updateEvent(state, event);
      }
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.detailLoading = false;
      Workbench.requestRender();
    });
  }

  function selectEvent(state, id) {
    var event = state.events.find(function (item) {
      return item.id === id;
    });
    state.selectedId = id;
    if (event) {
      state.detail = event;
    }
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return loadEventDetail(state, id);
  }

  function updateReview(state, reviewStatus) {
    var event = selectedEvent(state);
    if (!event) {
      return null;
    }
    state.savingReview = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.api(MESSAGE_EVENT_ITEM_PREFIX + encodeURIComponent(event.id), {
      method: "PATCH",
      body: {
        expected_version: event.version,
        review_status: reviewStatus
      }
    }).then(function (updated) {
      updateEvent(state, updated);
      state.success = reviewStatus === "reviewed" ? "消息已标记为已审核。" : "消息已标记为未审核。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.savingReview = false;
      Workbench.requestRender();
    });
  }

  function loadCandidates(state) {
    state.candidateLoading = true;
    state.error = "";
    Workbench.requestRender();
    return Workbench.api(CANDIDATES_PATH + Workbench.encodeQuery({
      q: state.candidateQuery,
      limit: CANDIDATE_PAGE_SIZE,
      offset: 0
    })).then(function (data) {
      state.candidateTotal = Number(data && data.total || 0);
      state.candidates = data && Array.isArray(data.candidates) ? data.candidates : [];
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.candidateLoading = false;
      Workbench.requestRender();
    });
  }

  function linkCandidate(state, body) {
    state.linking = true;
    state.error = "";
    state.success = "";
    Workbench.requestRender();
    return Workbench.api(MESSAGE_LINKS_PATH, {
      method: "POST",
      body: body
    }).then(function (data) {
      if (data && data.event) {
        updateEvent(state, data.event);
      }
      if (data && data.candidate) {
        state.selectedCandidateId = data.candidate.id || state.selectedCandidateId;
      }
      state.success = "消息已关联候选人。";
    }).catch(function (err) {
      state.error = err.message;
    }).finally(function () {
      state.linking = false;
      Workbench.requestRender();
    });
  }

  function linkExistingCandidate(state) {
    var event = selectedEvent(state);
    if (!event || !state.selectedCandidateId) {
      return null;
    }
    return linkCandidate(state, {
      message_event_id: event.id,
      expected_event_version: event.version,
      candidate_id: state.selectedCandidateId
    });
  }

  function parseSkills(value) {
    return String(value || "").split(/[\n,]/).map(function (skill) {
      return skill.trim();
    }).filter(Boolean);
  }

  function createAndLinkCandidate(state) {
    var event = selectedEvent(state);
    if (!event) {
      return null;
    }
    var form = state.candidateForm;
    return linkCandidate(state, {
      message_event_id: event.id,
      expected_event_version: event.version,
      candidate: {
        full_name: form.full_name.trim(),
        wechat_id: form.wechat_id.trim(),
        phone: form.phone.trim(),
        email: form.email.trim(),
        location: form.location.trim(),
        source: form.source.trim(),
        skills: parseSkills(form.skills),
        owner: form.owner.trim(),
        status: form.status,
        summary: form.summary.trim()
      }
    });
  }

  function syncFormValue(state, target) {
    var form = target.form;
    if (!form || !target.name) {
      return;
    }
    var formKind = form.getAttribute("data-crm-inbox-form");
    if (formKind === "filters" && Object.prototype.hasOwnProperty.call(state.filters, target.name)) {
      state.filters[target.name] = target.value;
    } else if (formKind === "candidate-search" && target.name === "candidate_query") {
      state.candidateQuery = target.value;
    } else if (formKind === "candidate-create" && Object.prototype.hasOwnProperty.call(state.candidateForm, target.name)) {
      state.candidateForm[target.name] = target.value;
    }
  }

  function bind(root, state) {
    root.addEventListener("input", function (event) {
      syncFormValue(state, event.target);
    });
    root.addEventListener("change", function (event) {
      syncFormValue(state, event.target);
    });
    root.addEventListener("submit", function (event) {
      var form = event.target.closest("form[data-crm-inbox-form]");
      if (!form) {
        return;
      }
      event.preventDefault();
      var kind = form.getAttribute("data-crm-inbox-form");
      Array.prototype.forEach.call(form.elements, function (element) {
        syncFormValue(state, element);
      });
      if (kind === "filters") {
        state.offset = 0;
        state.success = "";
        loadEvents(state);
      } else if (kind === "candidate-search") {
        loadCandidates(state);
      } else if (kind === "candidate-create") {
        createAndLinkCandidate(state);
      }
    });
    root.addEventListener("click", function (event) {
      var target = event.target.closest("button");
      if (!target || !root.contains(target)) {
        return;
      }
      var eventID = target.getAttribute("data-crm-inbox-event-id");
      var candidateID = target.getAttribute("data-crm-inbox-candidate-id");
      var reviewStatus = target.getAttribute("data-crm-inbox-review");
      var linkMode = target.getAttribute("data-crm-inbox-link-mode");
      var action = target.getAttribute("data-crm-inbox-action");
      if (eventID) {
        selectEvent(state, eventID);
      } else if (candidateID) {
        state.selectedCandidateId = candidateID;
        state.error = "";
        Workbench.requestRender();
      } else if (reviewStatus) {
        updateReview(state, reviewStatus);
      } else if (linkMode) {
        state.linkMode = linkMode;
        state.error = "";
        Workbench.requestRender();
      } else if (action === "reload") {
        loadEvents(state);
      } else if (action === "previous" && state.offset > 0) {
        state.offset = Math.max(0, state.offset - state.limit);
        loadEvents(state);
      } else if (action === "next" && state.offset + state.limit < state.total) {
        state.offset += state.limit;
        loadEvents(state);
      } else if (action === "link-existing") {
        linkExistingCandidate(state);
      }
    });
  }

  function onEnter(state) {
    if (!state.loading && !state.events.length && state.total === 0) {
      return loadEvents(state);
    }
    return null;
  }

  var view = {
    id: "inbox",
    label: "消息收件箱",
    section: "招聘交付",
    order: 10,
    createState: createState,
    render: render,
    onEnter: onEnter,
    bind: bind
  };

  window.OneBotWorkbench.registerView(view);
})();
