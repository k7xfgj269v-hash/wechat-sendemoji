package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"testing"
)

func newWebTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerWebRoutes(mux)
	return mux
}

func TestWebEmbeddedCoreRoutes(t *testing.T) {
	mux := newWebTestMux()

	for _, tc := range []struct {
		path        string
		contentType string
		contains    []string
	}{
		{
			path:        "/",
			contentType: "text/html",
			contains: []string{
				`<link rel="stylesheet" href="/app.css">`,
				`<script src="/app.js" defer></script>`,
				`data-workbench-shell`,
				"工作台",
			},
		},
		{
			path:        "/app.css",
			contentType: "text/css",
			contains:    []string{":root", ".app-shell", ".mobile-tabs"},
		},
		{
			path:        "/app.js",
			contentType: "text/javascript",
			contains: []string{
				`"use strict"`,
				"window.OneBotWorkbench",
				"直接联系人",
				"群聊联系人",
				"转发规则",
			},
		},
	} {
		t.Run("GET "+tc.path, func(t *testing.T) {
			response := requestWebRoute(t, mux, http.MethodGet, tc.path, tc.contentType)
			body := response.Body.String()
			for _, want := range tc.contains {
				if !strings.Contains(body, want) {
					t.Fatalf("body does not contain %q", want)
				}
			}
		})

		t.Run("HEAD "+tc.path, func(t *testing.T) {
			requestWebRoute(t, mux, http.MethodHead, tc.path, tc.contentType)
		})
	}
}

func TestWebModuleManifestAndAssetRoutes(t *testing.T) {
	mux := newWebTestMux()
	response := requestWebRoute(t, mux, http.MethodGet, "/workbench/modules.json", "application/json")

	var manifest webModuleManifest
	if err := json.Unmarshal(response.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Styles == nil || manifest.Scripts == nil {
		t.Fatalf("manifest arrays must be non-nil: %#v", manifest)
	}
	if !sort.StringsAreSorted(manifest.Styles) || !sort.StringsAreSorted(manifest.Scripts) {
		t.Fatalf("manifest assets are not sorted: %#v", manifest)
	}

	expectedStyles, expectedScripts := expectedEmbeddedModules(t)
	if strings.Join(manifest.Styles, "\n") != strings.Join(expectedStyles, "\n") {
		t.Fatalf("styles = %#v, want %#v", manifest.Styles, expectedStyles)
	}
	if strings.Join(manifest.Scripts, "\n") != strings.Join(expectedScripts, "\n") {
		t.Fatalf("scripts = %#v, want %#v", manifest.Scripts, expectedScripts)
	}

	requestWebRoute(t, mux, http.MethodHead, "/workbench/modules.json", "application/json")
	for _, assetPath := range append(append([]string{}, manifest.Styles...), manifest.Scripts...) {
		contentType := "text/javascript"
		if strings.HasSuffix(assetPath, ".css") {
			contentType = "text/css"
		}
		requestWebRoute(t, mux, http.MethodGet, assetPath, contentType)
		requestWebRoute(t, mux, http.MethodHead, assetPath, contentType)
	}

	requestWebRoute(t, mux, http.MethodGet, "/assets/app.css", "text/css")
	requestWebRoute(t, mux, http.MethodHead, "/assets/app.css", "text/css")
	requestWebRoute(t, mux, http.MethodGet, "/assets/app.js", "text/javascript")
	requestWebRoute(t, mux, http.MethodHead, "/assets/app.js", "text/javascript")
}

func TestWebAssetRouteBoundaries(t *testing.T) {
	mux := newWebTestMux()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/assets"},
		{method: http.MethodGet, path: "/assets/"},
		{method: http.MethodGet, path: "/assets/index.html"},
		{method: http.MethodGet, path: "/assets/missing.js"},
		{method: http.MethodGet, path: "/assets/nested/app.js"},
		{method: http.MethodGet, path: "/missing"},
		{method: http.MethodGet, path: "/app.js/extra"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	for _, assetPath := range []string{"/assets/../app.js", `/assets/..\app.js`} {
		t.Run("traversal "+assetPath, func(t *testing.T) {
			response := httptest.NewRecorder()
			webModuleAssetHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, assetPath, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestWebEmbeddedAssetMethodHandling(t *testing.T) {
	mux := newWebTestMux()

	for _, assetPath := range []string{
		"/",
		"/app.css",
		"/app.js",
		"/workbench/modules.json",
		"/assets/app.css",
		"/assets/app.js",
	} {
		t.Run("POST "+assetPath, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, assetPath, nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Fatalf("Allow = %q, want %q", allow, "GET, HEAD")
			}
		})
	}
}

func TestWebRoutesDoNotShadowExistingAPIRoutes(t *testing.T) {
	mux := newWebTestMux()
	for _, apiPath := range []string{"/api/contacts", "/api/crm/candidates"} {
		path := apiPath
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"source":"api"}`))
		})
	}

	for _, apiPath := range []string{"/api/contacts", "/api/crm/candidates"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiPath, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", apiPath, response.Code, response.Body.String())
		}
		if body := response.Body.String(); body != `{"source":"api"}` {
			t.Fatalf("%s body = %q, want API probe response", apiPath, body)
		}
	}
}

func TestWebWorkbenchModuleContract(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	for _, want := range []string{
		"window.OneBotWorkbench = workbenchAPI",
		"registerView: registerView",
		"api: api",
		"init.signal = options.signal",
		"requestError.payload = data",
		"CONTACT_REFRESH_TIMEOUT_MS",
		"apiWithTimeout",
		"contactDataEpoch",
		"epoch !== contactDataEpoch",
		"escapeHTML: escapeHTML",
		"encodeQuery: encodeQuery",
		"renderStatus: renderStatus",
		"requestRender: requestRender",
		"navigate: navigate",
		"refreshContactData: refreshContactData",
		"on: on",
		"definition.createState",
		"definition.render",
		"definition.onEnter",
		"definition.bind",
		"view already registered",
		`/^[a-z][a-z0-9_-]*$/`,
		`id: "contacts"`,
		`id: "groups"`,
		`id: "rules"`,
		`"/workbench/modules.json"`,
		`data-workbench-module`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app.js does not contain module contract marker %q", want)
		}
	}

	styleLoad := strings.Index(body, "Promise.all(styles.map(loadStyleAsset))")
	scriptLoad := strings.Index(body, "scripts.reduce")
	if styleLoad < 0 || scriptLoad < 0 || styleLoad > scriptLoad {
		t.Fatalf("module styles must load before sorted scripts: style=%d script=%d", styleLoad, scriptLoad)
	}
}

func TestWebPreservesCoreContactLabelsAndLegacyAPIs(t *testing.T) {
	body := readEmbeddedWebAsset(t, "web/app.js")
	for _, want := range []string{
		"工作台",
		"招聘与微信运营",
		"直接联系人",
		"群聊联系人",
		"转发规则",
		`endsWith("@chatroom")`,
		`"/api/contacts"`,
		`"/api/contacts/refresh"`,
		`"/api/groups"`,
		"clearContactSelections()",
		"loadContacts(true)",
		"loadGroups(true)",
		"刷新联系人缓存超时",
		"切换微信后刷新",
		`"/api/group_members"`,
		`"/api/contact_detail"`,
		`"/api/forward_rules"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app.js does not preserve %q", want)
		}
	}
}

func TestWebContactRefreshIgnoresPreviousAccountResponses(t *testing.T) {
	runWebNodeBehavior(
		t,
		"web/app.js",
		`
const requests = [];
const app = {
  innerHTML: "",
  querySelector() { return null; },
  addEventListener() {}
};
global.document = {
  getElementById() { return app; },
  querySelectorAll() { return []; },
  addEventListener() {},
  createElement() { return {}; },
  head: { appendChild() {} }
};
global.window = {
  location: { href: "http://127.0.0.1:58080/", origin: "http://127.0.0.1:58080" },
  sessionStorage: { getItem() { return null; }, setItem() {}, removeItem() {} },
  prompt() { return null; },
  confirm() { return true; },
  setTimeout,
  clearTimeout
};
global.fetch = function (path, options) {
  return new Promise((resolve) => {
    requests.push({ path: String(path), options: options || {}, resolve, resolved: false });
  });
};
function respond(request, body, status = 200) {
  request.resolved = true;
  request.resolve({
    status,
    ok: status >= 200 && status < 300,
    statusText: status === 200 ? "OK" : "Error",
    text() { return Promise.resolve(JSON.stringify(body)); }
  });
}
function pending(path, method) {
  return requests.filter((request) =>
    !request.resolved &&
    request.path.startsWith(path) &&
    String(request.options.method || "GET") === String(method || "GET")
  );
}
`,
		`
(async () => {
  await new Promise((resolve) => setImmediate(resolve));
  const manifest = pending("/workbench/modules.json")[0];
  const oldGroups = pending("/api/groups")[0];
  if (!manifest || !oldGroups) {
    throw new Error("initial workbench requests were not started");
  }
  respond(manifest, { styles: [], scripts: [] });

  window.OneBotWorkbench.navigate("contacts");
  await new Promise((resolve) => setImmediate(resolve));
  const oldContacts = pending("/api/contacts")[0];
  if (!oldContacts) {
    throw new Error("contact request was not started after opening the address book");
  }

  const refresh = window.OneBotWorkbench.refreshContactData();
  const refreshRequest = pending("/api/contacts/refresh", "POST")[0];
  if (!refreshRequest || !refreshRequest.options.signal) {
    throw new Error("contact refresh did not use a cancellable request");
  }
  respond(refreshRequest, { contacts: 1, groups: 1, members: 1 });
  await new Promise((resolve) => setImmediate(resolve));

  const newGroups = pending("/api/groups").find((request) => request !== oldGroups);
  const newContacts = pending("/api/contacts").find((request) => request !== oldContacts);
  if (!newGroups || !newContacts) {
    throw new Error("new account data was not reloaded after refresh");
  }
  respond(newGroups, {
    groups: [{ username: "new@chatroom", nick_name: "新账号职位群", member_count: 1 }]
  });
  respond(newContacts, {
    total: 1,
    contacts: [{ username: "wxid_new", nick_name: "新账号联系人", local_type: 3 }]
  });
  await refresh;

  respond(oldGroups, {
    groups: [{ username: "old@chatroom", nick_name: "旧账号职位群", member_count: 1 }]
  });
  respond(oldContacts, {
    total: 1,
    contacts: [{ username: "wxid_old", nick_name: "旧账号联系人", local_type: 3 }]
  });
  await new Promise((resolve) => setImmediate(resolve));

  window.OneBotWorkbench.navigate("groups");
  if (!app.innerHTML.includes("新账号职位群") || app.innerHTML.includes("旧账号职位群")) {
    throw new Error("old group response overwrote the refreshed account");
  }
  window.OneBotWorkbench.navigate("contacts");
  if (!app.innerHTML.includes("新账号联系人") || app.innerHTML.includes("旧账号联系人")) {
    throw new Error("old contact response overwrote the refreshed account");
  }
})().catch((error) => {
  console.error(error.stack || error);
  process.exit(1);
});
`,
	)
}

func TestWebEmbeddedAssetsAvoidExternalImportsAndGradients(t *testing.T) {
	entries, err := fs.ReadDir(webAssets, "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := path.Ext(entry.Name())
		if extension != ".html" && extension != ".css" && extension != ".js" {
			continue
		}
		assetPath := "web/" + entry.Name()
		t.Run(assetPath, func(t *testing.T) {
			body := strings.ToLower(readEmbeddedWebAsset(t, assetPath))
			for _, forbidden := range []string{
				"http://",
				"https://",
				"//cdn",
				"cdn.",
				"@import",
				" import ",
				"linear-gradient",
				"radial-gradient",
				"conic-gradient",
				"gradient(",
			} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s contains forbidden reference %q", assetPath, forbidden)
				}
			}
		})
	}
}

func TestWebResponsiveShellMarkers(t *testing.T) {
	html := readEmbeddedWebAsset(t, "web/index.html")
	if !strings.Contains(html, `name="viewport" content="width=device-width, initial-scale=1"`) {
		t.Fatal("index.html is missing the responsive viewport")
	}

	css := readEmbeddedWebAsset(t, "web/app.css")
	for _, marker := range []string{
		"grid-template-columns: 216px minmax(0, 1fr)",
		"@media (max-width: 760px)",
		"@media (max-width: 430px)",
		"overflow-x: auto",
		"min-width: max-content",
		"grid-template-columns: 38px minmax(0, 1fr)",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("app.css does not contain responsive marker %q", marker)
		}
	}
}

func requestWebRoute(t *testing.T, mux *http.ServeMux, method, assetPath, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(method, assetPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, body = %s", method, assetPath, response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, contentType) {
		t.Fatalf("%s %s Content-Type = %q, want prefix %q", method, assetPath, got, contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("%s %s Cache-Control = %q, want no-store", method, assetPath, cacheControl)
	}
	if method == http.MethodHead {
		if response.Body.Len() != 0 {
			t.Fatalf("%s %s body length = %d, want 0", method, assetPath, response.Body.Len())
		}
	} else if response.Body.Len() == 0 {
		t.Fatalf("%s %s body is empty", method, assetPath)
	}
	return response
}

func expectedEmbeddedModules(t *testing.T) ([]string, []string) {
	t.Helper()
	styles := make([]string, 0)
	scripts := make([]string, 0)
	entries, err := fs.ReadDir(webAssets, "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch path.Ext(entry.Name()) {
		case ".css":
			if entry.Name() != "app.css" {
				styles = append(styles, "/assets/"+entry.Name())
			}
		case ".js":
			if entry.Name() != "app.js" {
				scripts = append(scripts, "/assets/"+entry.Name())
			}
		}
	}
	sort.Strings(styles)
	sort.Strings(scripts)
	return styles, scripts
}

func readEmbeddedWebAsset(t *testing.T, assetPath string) string {
	t.Helper()
	data, err := webAssets.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
