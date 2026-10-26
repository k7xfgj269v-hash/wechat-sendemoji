package main

// 表情库 HTTP 路由：面板与外部工具共用。
//
// 挂载风格与 registerCRMBaseHTTPRoutes 一致：由 httpRouteRegistrars 里的
// 一项调用 registerStickerHTTPRoutes(context)；鉴权沿用同族的
// automationOperatorAuth（仅 loopback + Authorization: Bearer <operatorToken>）。
//
//	GET  /api/stickers         表情列表 + 当前开关
//	GET  /api/stickers/image?md5=  单张图片字节
//	POST /api/stickers/rename  {"md5","name","tags"}
//	POST /api/stickers/delete  {"md5"}
//	POST /api/stickers/upload  multipart(file,name,tags) 或 JSON{"data","name","tags"}

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	stickerCollectionPath = "/api/stickers"
	stickerImagePath      = "/api/stickers/image"
	stickerRenamePath     = "/api/stickers/rename"
	stickerDeletePath     = "/api/stickers/delete"
	stickerUploadPath     = "/api/stickers/upload"

	stickerJSONBodyMaxBytes = 64 << 10
	stickerUploadMaxBytes   = 4 << 20
)

// stickerStoreProvider 提供表情库句柄（允许为 nil，表示库不可用）。
type stickerStoreProvider func() *StickerStore

type stickerRenameRequest struct {
	MD5  string   `json:"md5"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type stickerDeleteRequest struct {
	MD5 string `json:"md5"`
}

type stickerUploadRequest struct {
	Data string   `json:"data"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// registerStickerHTTPRoutes 供 httpRouteRegistrars 使用。
func registerStickerHTTPRoutes(context httpRouteRegistrationContext) {
	registerStickerRoutes(context.mux, context.operatorToken())
}

// registerStickerRoutes 挂载表情库路由（默认表情库为 $HOME/.wechat-chatter/stickers）。
func registerStickerRoutes(mux *http.ServeMux, operatorToken string) {
	registerStickerRoutesWithStore(mux, operatorToken, defaultStickerStore)
}

// registerStickerRoutesWithStore 允许注入表情库（测试 / 嵌入式场景）。
func registerStickerRoutesWithStore(
	mux *http.ServeMux,
	operatorToken string,
	store stickerStoreProvider,
) {
	if mux == nil {
		return
	}
	if store == nil {
		store = defaultStickerStore
	}
	mux.HandleFunc(
		stickerCollectionPath,
		automationOperatorAuth(operatorToken, stickerListHandler(store)),
	)
	mux.HandleFunc(
		stickerImagePath,
		automationOperatorAuth(operatorToken, stickerImageHandler(store)),
	)
	mux.HandleFunc(
		stickerRenamePath,
		automationOperatorAuth(operatorToken, stickerRenameHandler(store)),
	)
	mux.HandleFunc(
		stickerDeletePath,
		automationOperatorAuth(operatorToken, stickerDeleteHandler(store)),
	)
	mux.HandleFunc(
		stickerUploadPath,
		automationOperatorAuth(operatorToken, stickerUploadHandler(store)),
	)
}

func stickerListHandler(store stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		library := store()
		if library == nil {
			stickerWriteUnavailable(w)
			return
		}
		items := library.Items()
		if items == nil {
			items = []StickerItem{}
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"items":    items,
			"total":    len(items),
			"settings": library.Settings(),
		})
	}
}

func stickerImageHandler(store stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodHead)
			return
		}
		digest := normalizeStickerMD5(r.URL.Query().Get("md5"))
		if digest == "" {
			automationWriteError(w, http.StatusBadRequest, "invalid_md5", "md5 must be 32 hexadecimal characters")
			return
		}
		library := store()
		if library == nil {
			stickerWriteUnavailable(w)
			return
		}
		path, err := library.ImagePath(digest)
		if err != nil {
			automationWriteError(w, http.StatusNotFound, "sticker_not_found", "sticker not found")
			return
		}
		data, err := readStickerFileBytes(path)
		if err != nil {
			automationWriteError(w, http.StatusNotFound, "sticker_missing", "sticker file is unavailable")
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}
}

func stickerRenameHandler(store stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request stickerRenameRequest
		if !stickerDecodeJSONBody(w, r, &request) {
			return
		}
		digest := normalizeStickerMD5(request.MD5)
		if digest == "" {
			automationWriteError(w, http.StatusBadRequest, "invalid_md5", "md5 must be 32 hexadecimal characters")
			return
		}
		library := store()
		if library == nil {
			stickerWriteUnavailable(w)
			return
		}
		if err := library.Rename(digest, request.Name, request.Tags); err != nil {
			stickerWriteStoreError(w, err)
			return
		}
		item, ok := library.Get(digest)
		if !ok {
			stickerWriteStoreError(w, errStickerNotFound)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "item": item})
	}
}

func stickerDeleteHandler(store stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request stickerDeleteRequest
		if !stickerDecodeJSONBody(w, r, &request) {
			return
		}
		digest := normalizeStickerMD5(request.MD5)
		if digest == "" {
			automationWriteError(w, http.StatusBadRequest, "invalid_md5", "md5 must be 32 hexadecimal characters")
			return
		}
		library := store()
		if library == nil {
			stickerWriteUnavailable(w)
			return
		}
		if err := library.Remove(digest); err != nil {
			stickerWriteStoreError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "removed": digest})
	}
}

func stickerUploadHandler(store stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		library := store()
		if library == nil {
			stickerWriteUnavailable(w)
			return
		}
		buf, name, tags, ok := stickerReadUpload(w, r)
		if !ok {
			return
		}
		if len(buf) == 0 {
			automationWriteError(w, http.StatusBadRequest, "empty_upload", "sticker content is empty")
			return
		}
		if !stickerImageKnown(buf) {
			automationWriteError(w, http.StatusBadRequest, "unsupported_image", "sticker must be png, gif, jpeg or webp")
			return
		}
		item, existed, err := library.Add(buf, StickerMeta{
			From: stickerFromManual,
			Name: name,
			Tags: tags,
		})
		if err != nil {
			automationWriteError(w, http.StatusInternalServerError, "sticker_write_failed", "sticker could not be stored")
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"item":    item,
			"existed": existed,
		})
	}
}

// stickerReadUpload 兼容 multipart（面板）与 JSON base64（脚本）两种上传方式。
func stickerReadUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, []string, bool) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, stickerUploadMaxBytes)
		if err := r.ParseMultipartForm(stickerUploadMaxBytes); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_request", "multipart body is invalid")
			return nil, "", nil, false
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		file, _, err := firstStickerUploadFile(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "missing_file", "upload requires a file field")
			return nil, "", nil, false
		}
		defer file.Close()
		buf, err := io.ReadAll(file)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_file", "uploaded file is unreadable")
			return nil, "", nil, false
		}
		return buf, r.FormValue("name"), stickerTagsFromValues(r.MultipartForm.Value["tags"]), true
	}

	var request stickerUploadRequest
	if !stickerDecodeJSONBodyLimited(w, r, &request, stickerUploadMaxBytes) {
		return nil, "", nil, false
	}
	encoded := strings.TrimSpace(request.Data)
	if index := strings.Index(encoded, "base64,"); index >= 0 {
		encoded = encoded[index+len("base64,"):]
	}
	buf, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		automationWriteError(w, http.StatusBadRequest, "invalid_data", "data must be base64 encoded")
		return nil, "", nil, false
	}
	return buf, request.Name, request.Tags, true
}

func firstStickerUploadFile(r *http.Request) (multipart.File, string, error) {
	for _, field := range []string{"file", "image", "sticker"} {
		if file, header, err := r.FormFile(field); err == nil {
			name := field
			if header != nil {
				name = header.Filename
			}
			return file, name, nil
		}
	}
	return nil, "", errors.New("sticker upload file is missing")
}

// stickerTagsFromValues 接受重复字段或 "a,b,c" / "a b c" 形式。
func stickerTagsFromValues(values []string) []string {
	tags := make([]string, 0, len(values))
	for _, value := range values {
		tags = append(tags, strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == '、' || r == ' ' || r == '\t' || r == '\n'
		})...)
	}
	return tags
}

func stickerDecodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	return stickerDecodeJSONBodyLimited(w, r, destination, stickerJSONBodyMaxBytes)
}

func stickerDecodeJSONBodyLimited(
	w http.ResponseWriter,
	r *http.Request,
	destination any,
	limit int64,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := decoder.Decode(destination); err != nil {
		automationWriteError(w, http.StatusBadRequest, "invalid_request", "request body is invalid JSON")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		automationWriteError(w, http.StatusBadRequest, "invalid_request", "request body must contain a single JSON object")
		return false
	}
	return true
}

func stickerWriteStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, errStickerNotFound) {
		automationWriteError(w, http.StatusNotFound, "sticker_not_found", "sticker not found")
		return
	}
	automationWriteError(w, http.StatusInternalServerError, "sticker_write_failed", "sticker library write failed")
}

func stickerWriteUnavailable(w http.ResponseWriter) {
	automationWriteError(w, http.StatusServiceUnavailable, "stickers_unavailable", "sticker library is unavailable")
}

func readStickerFileBytes(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > stickerCollectMaxRead {
		return nil, errors.New("sticker file is not readable")
	}
	return os.ReadFile(path)
}
