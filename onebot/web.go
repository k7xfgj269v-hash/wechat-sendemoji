package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
)

//go:embed web/*.html web/*.css web/*.js
var webAssets embed.FS

type webModuleManifest struct {
	Styles  []string `json:"styles"`
	Scripts []string `json:"scripts"`
}

func registerWebRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", webAssetHandler("/", "web/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("/app.css", webAssetHandler("/app.css", "web/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/app.js", webAssetHandler("/app.js", "web/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("/workbench/modules.json", webModuleManifestHandler())
	mux.HandleFunc("/assets", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/assets/", webModuleAssetHandler())
}

func webAssetHandler(route, assetPath, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != route {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", strings.Join([]string{http.MethodGet, http.MethodHead}, ", "))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data, err := webAssets.ReadFile(assetPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}
}

func webModuleManifestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workbench/modules.json" {
			http.NotFound(w, r)
			return
		}
		if !webMethodAllowed(w, r) {
			return
		}

		manifest, err := embeddedWebModuleManifest()
		if err != nil {
			http.Error(w, "module manifest unavailable", http.StatusInternalServerError)
			return
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			http.Error(w, "module manifest unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}
}

func webModuleAssetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !webMethodAllowed(w, r) {
			return
		}

		const prefix = "/assets/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if name == "" || name != path.Base(name) || strings.Contains(name, `\`) {
			http.NotFound(w, r)
			return
		}
		contentType, ok := webModuleContentType(name)
		if !ok {
			http.NotFound(w, r)
			return
		}

		data, err := webAssets.ReadFile("web/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}
}

func embeddedWebModuleManifest() (webModuleManifest, error) {
	manifest := webModuleManifest{
		Styles:  make([]string, 0),
		Scripts: make([]string, 0),
	}
	entries, err := fs.ReadDir(webAssets, "web")
	if err != nil {
		return manifest, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch path.Ext(name) {
		case ".css":
			if name != "app.css" {
				manifest.Styles = append(manifest.Styles, "/assets/"+name)
			}
		case ".js":
			if name != "app.js" {
				manifest.Scripts = append(manifest.Scripts, "/assets/"+name)
			}
		}
	}
	sort.Strings(manifest.Styles)
	sort.Strings(manifest.Scripts)
	return manifest, nil
}

func webModuleContentType(name string) (string, bool) {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8", true
	case ".js":
		return "text/javascript; charset=utf-8", true
	default:
		return "", false
	}
}

func webMethodAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", strings.Join([]string{http.MethodGet, http.MethodHead}, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}
