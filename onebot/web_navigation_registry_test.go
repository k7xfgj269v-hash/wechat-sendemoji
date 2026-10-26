package main

import (
	"strings"
	"testing"
)

func TestWebNavigationRegistryCoversAllViewsAndRequiredGroups(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/app.js",
		`
const nullPrototypeObjects = [];
const objectCreate = Object.create;
Object.create = function (prototype, properties) {
  const value = objectCreate(prototype, properties);
  if (prototype === null) {
    nullPrototypeObjects.push(value);
  }
  return value;
};

const viewRoot = {
  querySelector() { return null; },
  querySelectorAll() { return []; }
};
const appRoot = {
  innerHTML: "",
  addEventListener() {},
  removeEventListener() {},
  contains() { return true; },
  querySelector(selector) {
    return selector === ".view-root" ? viewRoot : null;
  }
};
global.document = {
  getElementById() { return appRoot; },
  querySelectorAll() { return []; },
  createElement() {
    return {
      addEventListener() {},
      remove() {},
      setAttribute() {},
      click() {}
    };
  },
  head: { appendChild() {} },
  body: { appendChild() {} }
};
global.window = {
  location: {
    href: "http://127.0.0.1:58080/",
    origin: "http://127.0.0.1:58080"
  },
  sessionStorage: {
    getItem() { return "test-operator-token"; },
    setItem() {},
    removeItem() {}
  },
  prompt() { return null; },
  confirm() { return true; },
  setTimeout,
  clearTimeout
};
global.fetch = function (path) {
  const requestPath = String(path);
  let body = {};
  if (requestPath.startsWith("/workbench/modules.json")) {
    body = { styles: [], scripts: [] };
  } else if (requestPath.startsWith("/api/groups")) {
    body = { groups: [] };
  } else if (requestPath.startsWith("/api/crm/message_events")) {
    body = { total: 0, events: [], ingestion_totals: {}, recent_decisions: [] };
  }
  return Promise.resolve({
    status: 200,
    ok: true,
    statusText: "OK",
    text() { return Promise.resolve(JSON.stringify(body)); }
  });
};
`,
		webNavigationModuleScripts(t)+`
(async () => {
  await new Promise((resolve) => setImmediate(resolve));

  const views = nullPrototypeObjects[0];
  const expected = {
    "inbox": ["招聘交付", 10],
    "candidates": ["招聘交付", 20],
    "jobs": ["招聘交付", 30],
    "pipeline": ["招聘交付", 40],
    "tasks": ["招聘交付", 50],
    "onboarding-fleet": ["驻场履约", 45],
    "funds-governance": ["驻场履约", 56],
    "service-requests": ["驻场履约", 70],
    "ai-chat": ["AI 中心", 60, true],
    "ai-actions": ["AI 中心", 65, false],
    "ai-supervision": ["AI 中心", 70, true],
    "automation-rules": ["AI 中心", 80, true],
    "automation-runs": ["AI 中心", 90, true],
    "automation-config": ["AI 中心", 100, true],
    "automation-operation-permissions": ["AI 中心", 110, true],
    "contacts": ["联系人", 110],
    "groups": ["联系人", 120],
    "recruiting-sources": ["联系人", 129],
    "rules": ["联系人", 130],
    "frida-control": ["系统", 140],
    "system-metrics": ["系统", 165],
    "system-data-management": ["系统", 170]
  };
  const expectedFields = [
    "bind", "createState", "id", "label", "navigation", "onEnter", "order", "render", "section"
  ];
  const actualIDs = Object.keys(views).sort();
  const expectedIDs = Object.keys(expected).sort();
  if (JSON.stringify(actualIDs) !== JSON.stringify(expectedIDs)) {
    throw new Error("registered views differ: " + JSON.stringify(actualIDs));
  }

  const legalSections = ["招聘交付", "驻场履约", "AI 中心", "联系人", "系统"];
  const actualSections = [];
  const sectionOrders = Object.create(null);
  actualIDs.forEach((id) => {
    const view = views[id];
    const fields = Object.keys(view).sort();
    if (JSON.stringify(fields) !== JSON.stringify(expectedFields)) {
      throw new Error(id + " has fields " + JSON.stringify(fields));
    }
    if (view.section !== expected[id][0] || view.order !== expected[id][1] ||
        view.navigation !== (expected[id][2] !== false)) {
      throw new Error(id + " registered as " + view.section + "/" + view.order +
        "/navigation=" + view.navigation);
    }
    if (!actualSections.includes(view.section)) {
      actualSections.push(view.section);
    }
    sectionOrders[view.section] = sectionOrders[view.section] || [];
    if (sectionOrders[view.section].includes(view.order)) {
      throw new Error("duplicate runtime order in " + view.section + ": " + view.order);
    }
    sectionOrders[view.section].push(view.order);
  });
  actualSections.sort((left, right) =>
    legalSections.indexOf(left) - legalSections.indexOf(right));
  if (JSON.stringify(actualSections) !== JSON.stringify(legalSections)) {
    throw new Error("legal sections differ: " + JSON.stringify(actualSections));
  }

  const navIDs = Array.from(appRoot.innerHTML.matchAll(/data-view="([^"]+)"/g),
    (match) => match[1]);
  actualIDs.forEach((id) => {
    const count = navIDs.filter((navID) => navID === id).length;
    const expectedCount = expected[id][2] === false ? 0 : 2;
    if (count !== expectedCount) {
      throw new Error(id + " rendered " + count + " navigation buttons");
    }
  });
  const renderedIDs = Array.from(new Set(navIDs)).sort();
  const expectedRenderedIDs = expectedIDs.filter((id) => expected[id][2] !== false);
  if (JSON.stringify(renderedIDs) !== JSON.stringify(expectedRenderedIDs)) {
    throw new Error("rendered navigation differs: " + JSON.stringify(renderedIDs));
  }
  const renderedSections = Array.from(
    appRoot.innerHTML.matchAll(/data-nav-section="([^"]+)"/g),
    (match) => match[1]
  );
  if (JSON.stringify(renderedSections) !== JSON.stringify(legalSections)) {
    throw new Error("rendered sections differ: " + JSON.stringify(renderedSections));
  }
  if (!window.OneBotWorkbench.navigate("ai-actions") ||
      !appRoot.innerHTML.includes("AI 工作台")) {
    throw new Error("hidden AI actions view is not reachable through the registry");
  }
  if (appRoot.innerHTML.includes('data-view="ai-actions"')) {
    throw new Error("hidden AI actions view appeared in navigation after direct access");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebNavigationRegistryRejectsInvalidDefinitions(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/app.js",
		webNavigationNodeEnvironment(),
		`
function definition(id, section, order) {
  return {
    id,
    label: id,
    section,
    order,
    createState() { return {}; },
    render() { return ""; },
    onEnter: null,
    bind: null
  };
}
function expectFailure(label, callback, message) {
  try {
    callback();
  } catch (error) {
    if (!String(error.message).includes(message)) {
      throw new Error(label + " returned unexpected error: " + error.message);
    }
    return;
  }
  throw new Error(label + " was accepted");
}

expectFailure(
  "duplicate id",
  () => window.OneBotWorkbench.registerView(definition("contacts", "系统", 910)),
  "view already registered"
);
expectFailure(
  "duplicate section order",
  () => window.OneBotWorkbench.registerView(definition("contact-copy", "联系人", 110)),
  "view order already registered in section"
);
expectFailure(
  "unknown section",
  () => window.OneBotWorkbench.registerView(definition("unknown-section", "其他", 911)),
  "unknown view section"
);
expectFailure(
  "infinite order",
  () => window.OneBotWorkbench.registerView(definition("infinite-order", "系统", Infinity)),
  "view order must be finite"
);
expectFailure(
  "NaN order",
  () => window.OneBotWorkbench.registerView(definition("nan-order", "系统", NaN)),
  "view order must be finite"
);

window.OneBotWorkbench.registerView(
  definition("cross-section-order", "系统", 110)
);
if (!window.OneBotWorkbench.navigate("cross-section-order")) {
  throw new Error("the same order in a different section was rejected");
}
`,
	)
}

func TestWebNavigationRegistryRejectsReentrantDuplicateReservations(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/app.js",
		webNavigationNodeEnvironment(),
		`
function definition(id, section, order, createState) {
  return {
    id,
    label: id,
    section,
    order,
    createState: createState || function () { return {}; },
    render() { return ""; },
    onEnter: null,
    bind: null
  };
}
function expectFailure(label, callback, message) {
  try {
    callback();
  } catch (error) {
    if (!String(error.message).includes(message)) {
      throw new Error(label + " returned unexpected error: " + error.message);
    }
    return;
  }
  throw new Error(label + " was accepted");
}

window.OneBotWorkbench.registerView(definition(
  "reentrant-id",
  "系统",
  930,
  function () {
    expectFailure(
      "reentrant duplicate id",
      () => window.OneBotWorkbench.registerView(
        definition("reentrant-id", "系统", 931)
      ),
      "view already registered"
    );
    return {};
  }
));
if (!window.OneBotWorkbench.navigate("reentrant-id")) {
  throw new Error("outer duplicate-id view was not registered");
}

window.OneBotWorkbench.registerView(definition(
  "reentrant-order",
  "系统",
  940,
  function () {
    expectFailure(
      "reentrant duplicate section order",
      () => window.OneBotWorkbench.registerView(
        definition("reentrant-order-copy", "系统", 940)
      ),
      "view order already registered in section"
    );
    return {};
  }
));
if (!window.OneBotWorkbench.navigate("reentrant-order") ||
    window.OneBotWorkbench.navigate("reentrant-order-copy")) {
  throw new Error("reentrant duplicate-order registration changed the registry");
}

expectFailure(
  "createState failure",
  () => window.OneBotWorkbench.registerView(definition(
    "released-reservation",
    "系统",
    950,
    function () { throw new Error("state initialization failed"); }
  )),
  "state initialization failed"
);
window.OneBotWorkbench.registerView(
  definition("released-reservation", "系统", 950)
);
if (!window.OneBotWorkbench.navigate("released-reservation")) {
  throw new Error("failed createState left its registration reserved");
}
`,
	)
}

func TestWebNavigationRegistryPreservesViewState(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/app.js",
		webNavigationNodeEnvironment(),
		`
let firstCreateCount = 0;
let secondCreateCount = 0;
let firstState;
const firstRenderStates = [];

window.OneBotWorkbench.registerView({
  id: "state-first",
  label: "State First",
  section: "招聘交付",
  order: 910,
  createState() {
    firstCreateCount += 1;
    firstState = { value: 0 };
    return firstState;
  },
  render(state) {
    firstRenderStates.push(state);
    return String(state.value);
  },
  onEnter: null,
  bind: null
});
window.OneBotWorkbench.registerView({
  id: "state-second",
  label: "State Second",
  section: "系统",
  order: 920,
  createState() {
    secondCreateCount += 1;
    return { value: 0 };
  },
  render(state) {
    return String(state.value);
  },
  onEnter: null,
  bind: null
});

window.OneBotWorkbench.navigate("state-first");
firstState.value = 37;
window.OneBotWorkbench.navigate("state-second");
window.OneBotWorkbench.navigate("state-first");

if (firstCreateCount !== 1 || secondCreateCount !== 1) {
  throw new Error("view state was recreated during navigation");
}
if (firstRenderStates.length < 2 ||
    firstRenderStates.some((state) => state !== firstState) ||
    firstRenderStates[firstRenderStates.length - 1].value !== 37) {
  throw new Error("registered view state identity or contents were not preserved");
}
`,
	)
}

func TestWebNavigationHasNoStandaloneViewTruth(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	for _, marker := range []string{
		"function navigationItems()",
		"return Object.keys(views).map(function (id)",
		"navigationItems().map(navButton)",
		"navigationItems().forEach(function (item)",
		`var navigationSections = ["招聘交付", "驻场履约", "AI 中心", "联系人", "系统"]`,
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("app.js missing generated navigation marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"function navigationViewDefinitions(",
		"var navigation = [",
		"var navigationItems = [",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("app.js contains standalone navigation truth %q", forbidden)
		}
	}
}

func webNavigationModuleScripts(t *testing.T) string {
	t.Helper()
	paths := []string{
		"web/automation_actions.js",
		"web/automation_config.js",
		"web/automation_metrics.js",
		"web/automation_operation_permissions.js",
		"web/automation_rules.js",
		"web/automation_runs.js",
		"web/automation_supervision.js",
		"web/crm_candidates.js",
		"web/crm_inbox.js",
		"web/crm_jobs.js",
		"web/crm_pipeline.js",
		"web/crm_tasks.js",
		"web/frida_control.js",
		"web/ops_funds_governance.js",
		"web/ops_onboarding_fleet.js",
		"web/ops_service_requests.js",
		"web/recruiting_sources.js",
		"web/system_data_management.js",
	}
	var scripts strings.Builder
	for _, path := range paths {
		scripts.WriteString("\n")
		scripts.WriteString(readEmbeddedWebAsset(t, path))
		scripts.WriteString("\n")
	}
	return scripts.String()
}

func webNavigationNodeEnvironment() string {
	return `
const viewRoot = {
  querySelector() { return null; },
  querySelectorAll() { return []; }
};
const appRoot = {
  innerHTML: "",
  addEventListener() {},
  removeEventListener() {},
  contains() { return true; },
  querySelector(selector) {
    return selector === ".view-root" ? viewRoot : null;
  }
};
global.document = {
  getElementById() { return appRoot; },
  querySelectorAll() { return []; },
  createElement() {
    return {
      addEventListener() {},
      remove() {},
      setAttribute() {},
      click() {}
    };
  },
  head: { appendChild() {} },
  body: { appendChild() {} }
};
global.window = {
  location: {
    href: "http://127.0.0.1:58080/",
    origin: "http://127.0.0.1:58080"
  },
  sessionStorage: {
    getItem() { return "test-operator-token"; },
    setItem() {},
    removeItem() {}
  },
  prompt() { return null; },
  confirm() { return true; },
  setTimeout,
  clearTimeout
};
global.fetch = function (path) {
  const requestPath = String(path);
  let body = {};
  if (requestPath.startsWith("/workbench/modules.json")) {
    body = { styles: [], scripts: [] };
  } else if (requestPath.startsWith("/api/groups")) {
    body = { groups: [] };
  }
  return Promise.resolve({
    status: 200,
    ok: true,
    statusText: "OK",
    text() { return Promise.resolve(JSON.stringify(body)); }
  });
};
`
}
