package main

import (
	"strings"
	"testing"
)

func TestWebRecruitingSourcesRegistrationAPIsAndOptimisticVersioning(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/recruiting_sources.js")
	for _, marker := range []string{
		`id: "recruiting-sources"`,
		`label: "招聘来源组"`,
		"order: 129",
		"Workbench.registerView",
		"Workbench.automationAPI(PROJECT_PATH",
		"Workbench.automationAPI(path",
		"Workbench.api(GROUPS_PATH)",
		`var PROJECT_PATH = "/api/crm/recruiting/project"`,
		`var SOURCES_PATH = "/api/crm/recruiting/source_groups"`,
		`var GROUPS_PATH = "/api/groups"`,
		"Workbench.refreshContactData()",
		"expected_version: state.project.version",
		"expected_project_version: state.project.version",
		`method: editing ? "PATCH" : "POST"`,
		`method: "DELETE"`,
		"enabled: !source.enabled",
		"gate_enabled: state.form.gate_enabled",
		"gate_mode: state.form.gate_mode",
		`disabled(locked || state.contactsLoading)`,
		`disabled(locked) + "></label>"`,
		`endsWith("@chatroom")`,
		"window.confirm(",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("recruiting_sources.js missing %q", marker)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "localStorage", "document.cookie", "Authorization"} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("recruiting_sources.js bypasses workbench API with %q", forbidden)
		}
	}
	for _, lockedWhileEditing := range []string{
		"disabled(locked || editing || state.contactsLoading)",
		"disabled(locked || editing)",
	} {
		if strings.Contains(js, lockedWhileEditing) {
			t.Fatalf("recruiting_sources.js locks editable GroupID control with %q", lockedWhileEditing)
		}
	}

	appJS := readEmbeddedWebAsset(t, "web/app.js")
	if !strings.Contains(appJS, `apiWithTimeout(`) ||
		!strings.Contains(appJS, `"/api/contacts/refresh"`) ||
		!strings.Contains(appJS, `{ method: "POST" }`) {
		t.Fatal("shared contact refresh helper must POST /api/contacts/refresh")
	}
}

func TestWebRecruitingSourcesRefreshesAccountDataWithoutOverlap(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/recruiting_sources.js",
		`
const views = {};
const calls = [];
let resolveRefresh;
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    refreshContactData() {
      calls.push({ kind: "refresh" });
      return new Promise((resolve) => { resolveRefresh = resolve; });
    },
    api(path, options) {
      calls.push({ kind: "api", path, options: options || {} });
      if (path === "/api/groups") {
        return Promise.resolve({
          groups: [{ username: "new_jobs@chatroom", nick_name: "新账号职位群" }]
        });
      }
      throw new Error("unexpected api path " + path);
    },
    automationAPI(path, options) {
      calls.push({ kind: "automation", path, options: options || {} });
      if (path !== "/api/crm/recruiting/project") {
        throw new Error("unexpected automation path " + path);
      }
      return Promise.resolve({
        organization_name: "国爱人力",
        version: 4,
        require_authoritative_job: true,
        source_groups: []
      });
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
  const view = views["recruiting-sources"];
  const state = view.createState();
  state.loading = false;
  const button = {
    getAttribute(name) {
      return name === "data-source-action" ? "refresh-contacts" : "";
    },
    addEventListener(name, handler) {
      if (name === "click") this.click = handler;
    }
  };
  const root = {
    querySelector() { return null; },
    querySelectorAll() { return [button]; }
  };
  view.bind(root, state);

  button.click();
  button.click();
  if (calls.length !== 1 ||
      calls[0].kind !== "refresh") {
    throw new Error("refresh requests overlapped or used the wrong contract");
  }
  if (!state.refreshingContacts) {
    throw new Error("refresh state was not locked");
  }
  if (calls.some((call) => call.kind === "api" || call.kind === "automation")) {
    throw new Error("project or group GET started before contact refresh completed");
  }

  resolveRefresh({ contacts: 17, groups: 5 });
  await new Promise((resolve) => setTimeout(resolve, 0));

  const sequence = calls.map((call) =>
    call.kind + (call.path ? ":" + call.path : ""));
  const expectedSequence = [
    "refresh",
    "automation:/api/crm/recruiting/project",
    "api:/api/groups"
  ];
  if (JSON.stringify(sequence) !== JSON.stringify(expectedSequence)) {
    throw new Error("unexpected refresh sequence " + JSON.stringify(sequence));
  }
  if (calls[1].options.method || calls[2].options.method) {
    throw new Error("project and group reloads must use GET");
  }
  if (state.contacts.length !== 1 ||
      state.contacts[0].username !== "new_jobs@chatroom" ||
      state.project.version !== 4) {
    throw new Error("reloaded data was not applied");
  }
  if (state.success !== "通讯录已刷新：17 个联系人，5 个群聊。") {
    throw new Error("refresh counts were not shown");
  }
  if (state.refreshingContacts) {
    throw new Error("refresh state remained locked");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebRecruitingSourcesReportsContactRefreshFailure(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/recruiting_sources.js",
		`
const views = {};
const calls = [];
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    refreshContactData() {
      calls.push({ kind: "refresh" });
      return Promise.reject(new Error("微信通讯录刷新失败"));
    },
    api(path) {
      calls.push({ kind: "api", path });
      return Promise.resolve({ groups: [] });
    },
    automationAPI(path) {
      calls.push({ kind: "automation", path });
      return Promise.resolve({
        organization_name: "国爱人力",
        version: 4,
        require_authoritative_job: true,
        source_groups: []
      });
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
  const view = views["recruiting-sources"];
  const state = view.createState();
  state.loading = false;
  const button = {
    getAttribute(name) {
      return name === "data-source-action" ? "refresh-contacts" : "";
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  if (calls.length !== 1 || calls[0].kind !== "refresh") {
    throw new Error("failed refresh must not start project or group GETs");
  }
  if (state.success !== "" ||
      state.error !== "微信通讯录刷新失败" ||
      state.refreshingContacts) {
    throw new Error("refresh failure was not exposed or state remained locked");
  }
  if (!view.render(state).includes("微信通讯录刷新失败")) {
    throw new Error("refresh failure is not visible in the rendered status");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebRecruitingSourcesDoesNotReportSuccessWhenReloadFails(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/recruiting_sources.js",
		`
const views = {};
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    refreshContactData() {
      return Promise.resolve({ contacts: 17, groups: 5 });
    },
    api(path) {
      if (path === "/api/groups") {
        return Promise.reject(new Error("群聊列表重新加载失败"));
      }
      throw new Error("unexpected api path " + path);
    },
    automationAPI() {
      return Promise.resolve({
        organization_name: "国爱人力",
        version: 4,
        require_authoritative_job: true,
        source_groups: []
      });
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  }
};
`,
		`
(async () => {
  const view = views["recruiting-sources"];
  const state = view.createState();
  state.loading = false;
  state.contacts = [{ username: "old_jobs@chatroom" }];
  const button = {
    getAttribute(name) {
      return name === "data-source-action" ? "refresh-contacts" : "";
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  if (state.success !== "" ||
      state.error !== "群聊列表重新加载失败" ||
      state.contacts.length !== 0 ||
      state.refreshingContacts) {
    throw new Error("partial refresh was reported as success");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebRecruitingSourcesRendersAndSubmitsGateFields(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/recruiting_sources.js",
		`
const views = {};
const calls = [];
global.window = {
  OneBotWorkbench: {
    registerView(view) { views[view.id] = view; },
    api() { return Promise.resolve({ groups: [] }); },
    automationAPI(path, options) {
      calls.push({ path, options: options || {} });
      return Promise.resolve({
        organization_name: "国爱人力",
        version: 8,
        require_authoritative_job: true,
        source_groups: []
      });
    },
    escapeHTML(value) { return String(value == null ? "" : value); },
    renderStatus() { return ""; },
    requestRender() {}
  },
  confirm() { return true; }
};
`,
		`
(async () => {
  const view = views["recruiting-sources"];
  const state = view.createState();
  state.loading = false;
  state.project = {
    organization_name: "国爱人力",
    version: 7,
    require_authoritative_job: true,
    source_groups: [{
      id: "source_1",
      group_id: "jobs@chatroom",
      display_name: "职位群",
      enabled: true,
      gate_enabled: true,
      gate_mode: "job_relevance",
      version: 2
    }]
  };
  const html = view.render(state);
  for (const expected of [
    'name="gate_enabled"',
    'name="gate_mode"',
    'value="job_relevance"',
    "职位相关性门控已启用",
    "切换微信账号后刷新通讯录"
  ]) {
    if (!html.includes(expected)) {
      throw new Error("missing gate or refresh UI " + expected);
    }
  }

  state.form = {
    id: "source_1",
    group_id: "jobs@chatroom",
    display_name: "职位群",
    enabled: true,
    gate_enabled: true,
    gate_mode: "job_relevance"
  };
  const element = (value, checked) => ({
    value: value || "",
    checked: !!checked,
    addEventListener() {}
  });
  const form = {
    elements: {
      group_id: element("jobs@chatroom"),
      display_name: element("职位群"),
      enabled: element("", true),
      gate_enabled: element("", true),
      gate_mode: element("job_relevance"),
      contact_group_id: element("")
    },
    addEventListener(name, handler) {
      if (name === "submit") this.submit = handler;
    }
  };
  const root = {
    querySelector(selector) {
      return selector === '[data-source-form="source"]' ? form : null;
    },
    querySelectorAll() { return []; }
  };
  view.bind(root, state);
  form.submit({ preventDefault() {} });
  await new Promise((resolve) => setTimeout(resolve, 0));

  const save = calls.find((call) =>
    call.path === "/api/crm/recruiting/source_groups/source_1");
  if (!save ||
      save.options.method !== "PATCH" ||
      save.options.body.gate_enabled !== true ||
      save.options.body.gate_mode !== "job_relevance") {
    throw new Error("gate fields were not submitted");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebRecruitingSourcesEscapesValuesAndPreservesStaleDrafts(t *testing.T) {
	js := readEmbeddedWebAsset(t, "web/recruiting_sources.js")
	for _, marker := range []string{
		"function applyProject(state, project, preserveOrganization, preserveSource)",
		"var organizationDraft = state.organizationDraft",
		"var sourceDraft = state.form",
		"state.organizationDraft = preserveOrganization",
		"state.form = sourceDraft",
		"return recoverStale(state, true, true)",
		"当前草稿已保留，已加载服务端最新项目版本",
		"409 版本冲突",
		"escapeHTML(source.display_name",
		"escapeHTML(source.group_id)",
		"escapeHTML(group.username",
		"escapeHTML(state.organizationDraft)",
	} {
		if !strings.Contains(js, marker) {
			t.Fatalf("recruiting_sources.js missing stale/escape marker %q", marker)
		}
	}
	if strings.Count(js, "escapeHTML(") < 16 {
		t.Fatal("server-rendered recruiting source values are not consistently escaped")
	}
}

func TestWebRecruitingSourcesResponsiveScopedStyles(t *testing.T) {
	css := readEmbeddedWebAsset(t, "web/recruiting_sources.css")
	for _, marker := range []string{
		".recruitingSources {",
		".recruitingSources__workspace",
		".recruitingSources__topbarActions",
		"grid-template-columns: minmax(360px, 1.05fr) minmax(330px, 0.95fr)",
		".recruitingSources__list",
		"overflow-y: auto",
		"overflow-wrap: anywhere",
		"@media (max-width: 430px)",
		"flex-direction: column",
		"grid-template-columns: repeat(3, minmax(0, 1fr))",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("recruiting_sources.css missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"@import", "url(", "http://", "https://",
		"linear-gradient", "radial-gradient", "conic-gradient",
		".recruitingSources__panel .recruitingSources__panel",
	} {
		if strings.Contains(strings.ToLower(css), strings.ToLower(forbidden)) {
			t.Fatalf("recruiting_sources.css contains forbidden marker %q", forbidden)
		}
	}
}

func TestWebRecruitingSourcesRegisteredNavigationEntry(t *testing.T) {
	appJS := readEmbeddedWebAsset(t, "web/app.js")
	sourceJS := readEmbeddedWebAsset(t, "web/recruiting_sources.js")
	for _, marker := range []string{
		`id: "recruiting-sources"`,
		`label: "招聘来源组"`,
		`section: "联系人"`,
		"order: 129",
		"Workbench.registerView({",
	} {
		if !strings.Contains(sourceJS, marker) {
			t.Fatalf("recruiting_sources.js missing navigation registration marker %q", marker)
		}
	}
	for _, marker := range []string{
		"function navigationItems()",
		"return Object.keys(views).map(function (id)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("app.js missing generated navigation marker %q", marker)
		}
	}
	if strings.Contains(appJS, `{ id: "recruiting-sources"`) {
		t.Fatal("app.js must not duplicate the recruiting source view registration")
	}
}
