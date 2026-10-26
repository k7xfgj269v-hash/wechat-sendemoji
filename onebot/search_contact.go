package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "github.com/mutecomm/go-sqlcipher/v4"
)

type SearchContactReq struct {
	Keyword string `json:"keyword"`
}

type ContactResult struct {
	Username  string `json:"username"`
	NickName  string `json:"nick_name"`
	Alias     string `json:"alias"`
	Remark    string `json:"remark"`
	LocalType int    `json:"local_type"`
}

var hexKeyRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

const maxContactSearchResults = 200

type wxRSConfig struct {
	AccountWxID string `json:"account_wxid"`
	DBDir       string `json:"db_dir"`
}

// searchContactHandler 接 POST /search_contact {"keyword":"蒙"} → {"results":[...]}
// contact.db 由 wechat-agent 在进程内读取，hr-workbench 只通过 AgentClient 查询。
func searchContactHandler(w http.ResponseWriter, r *http.Request) {
	searchContactHandlerWithAgent(currentHRWorkbenchAgentClient())(w, r)
}

func searchContactHandlerWithAgent(client AgentClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
			return
		}
		req := new(SearchContactReq)
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, "无效的 JSON", http.StatusBadRequest)
			return
		}
		keyword := strings.TrimSpace(req.Keyword)
		if keyword == "" || len(keyword) > 64 || strings.ContainsAny(keyword, "\n\r;") {
			http.Error(w, "keyword 非法", http.StatusBadRequest)
			return
		}
		if client == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "微信代理不可用")
			return
		}

		result, err := client.SearchContacts(r.Context(), AgentContactSearchRequest{
			Keyword: keyword,
			Limit:   200,
		})
		if err != nil {
			writeAPIError(w, agentHTTPStatus(err), publicAgentError(err, "联系人搜索失败"))
			return
		}
		results := make([]ContactResult, len(result.Contacts))
		for i := range result.Contacts {
			contact := result.Contacts[i]
			results[i] = ContactResult{
				Username:  contact.Username,
				NickName:  contact.NickName,
				Alias:     contact.Alias,
				Remark:    contact.Remark,
				LocalType: contact.LocalType,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}
}

func writeSearchError(w http.ResponseWriter, err error) {
	Error("search_contact 失败", "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

// findContactDB 动态发现与当前 key 匹配的 db_storage/contact/contact.db。
func findContactDB(keyHex string) (string, error) {
	currentDB, account, configured, err := configuredContactDB()
	if err != nil {
		return "", err
	}
	if configured {
		db, err := openSQLCipherReadOnly(currentDB, keyHex)
		if err != nil {
			return "", fmt.Errorf(
				"当前账号 %s 的 contact.db 无法使用 keys.json 密钥解密: %w",
				account,
				err,
			)
		}
		if err := db.Close(); err != nil {
			return "", fmt.Errorf("关闭当前账号 contact.db 失败: %w", err)
		}
		return currentDB, nil
	}

	base := filepath.Join(os.Getenv("HOME"),
		"Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", fmt.Errorf("读 xwechat_files 失败: %w", err)
	}
	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || name == "all_users" || name == "Backup" {
			continue
		}
		p := filepath.Join(base, name, "db_storage", "contact", "contact.db")
		if _, err := os.Stat(p); err == nil {
			candidates = append(candidates, p)
		}
	}
	for _, candidate := range candidates {
		db, err := openSQLCipherReadOnly(candidate, keyHex)
		if err != nil {
			continue
		}
		if err := db.Close(); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到与当前密钥匹配的 contact.db")
}

func configuredContactDB() (string, string, bool, error) {
	configPath := filepath.Join(os.Getenv("HOME"), ".wx-rs", "config.json")
	raw, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", true, fmt.Errorf("读 config.json 失败: %w", err)
	}

	var cfg wxRSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", "", true, fmt.Errorf("解析 config.json 失败: %w", err)
	}
	cfg.AccountWxID = strings.TrimSpace(cfg.AccountWxID)
	cfg.DBDir = strings.TrimSpace(cfg.DBDir)
	if cfg.AccountWxID == "" && cfg.DBDir == "" {
		return "", "", true, fmt.Errorf("config.json 缺少 account_wxid 和 db_dir")
	}

	base := filepath.Join(os.Getenv("HOME"),
		"Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files")
	if cfg.DBDir == "" {
		cfg.DBDir = filepath.Join(base, cfg.AccountWxID, "db_storage")
	}
	dbDir, err := filepath.Abs(cfg.DBDir)
	if err != nil {
		return "", "", true, fmt.Errorf("解析当前账号 db_dir 失败: %w", err)
	}
	relative, err := filepath.Rel(base, dbDir)
	if err != nil {
		return "", "", true, fmt.Errorf("校验当前账号 db_dir 失败: %w", err)
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	if len(parts) != 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." ||
		parts[1] != "db_storage" {
		return "", "", true, fmt.Errorf("config.json 的 db_dir 不属于微信账号目录")
	}
	account := parts[0]
	if cfg.AccountWxID != "" && cfg.AccountWxID != account {
		return "", "", true, fmt.Errorf(
			"config.json 账号不一致: account_wxid=%s, db_dir=%s",
			cfg.AccountWxID,
			account,
		)
	}

	dbPath := filepath.Join(dbDir, "contact", "contact.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		return "", "", true, fmt.Errorf("当前账号 %s 的 contact.db 不可用: %w", account, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", true, fmt.Errorf("当前账号 %s 的 contact.db 不是普通文件", account)
	}
	return dbPath, account, true, nil
}

func loadContactKey() (string, error) {
	p := filepath.Join(os.Getenv("HOME"), ".wx-rs", "keys.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读 keys.json 失败: %w", err)
	}
	var kf struct {
		Entries map[string]struct {
			KeyHex string `json:"key_hex"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &kf); err != nil {
		return "", fmt.Errorf("解析 keys.json 失败: %w", err)
	}
	e, ok := kf.Entries["contact/contact.db"]
	if !ok || e.KeyHex == "" {
		return "", fmt.Errorf("keys.json 无 contact/contact.db key")
	}
	return e.KeyHex, nil
}

func querySqlcipher(dbPath, keyHex, keyword string) ([]ContactResult, error) {
	db, err := openSQLCipherReadOnly(dbPath, keyHex)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	pattern := "%" + escapeLikePattern(keyword) + "%"
	rows, err := db.Query(`
SELECT COALESCE(username, ''),
       COALESCE(nick_name, ''),
       COALESCE(alias, ''),
       COALESCE(remark, ''),
       COALESCE(local_type, 0)
FROM contact
WHERE nick_name LIKE ? ESCAPE '\'
   OR remark LIKE ? ESCAPE '\'
   OR alias LIKE ? ESCAPE '\'
ORDER BY username
LIMIT ?`, pattern, pattern, pattern, maxContactSearchResults)
	if err != nil {
		return nil, fmt.Errorf("查询联系人失败: %w", err)
	}
	defer rows.Close()

	results := make([]ContactResult, 0)
	for rows.Next() {
		var result ContactResult
		if err := rows.Scan(
			&result.Username,
			&result.NickName,
			&result.Alias,
			&result.Remark,
			&result.LocalType,
		); err != nil {
			return nil, fmt.Errorf("读取联系人失败: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历联系人失败: %w", err)
	}
	return results, nil
}

func openSQLCipherReadOnly(dbPath, keyHex string) (*sql.DB, error) {
	if !hexKeyRe.MatchString(keyHex) {
		return nil, fmt.Errorf("contact.db key 非 32 字节 hex")
	}
	absolutePath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("解析 contact.db 路径失败: %w", err)
	}
	fileURL := &url.URL{Scheme: "file", Path: absolutePath}
	params := fileURL.Query()
	params.Set("mode", "ro")
	params.Set("_query_only", "1")
	params.Set("_pragma_key", "x'"+keyHex+"'")
	params.Set("_pragma_cipher_page_size", "4096")
	fileURL.RawQuery = params.Encode()

	db, err := sql.Open("sqlite3", fileURL.String())
	if err != nil {
		return nil, fmt.Errorf("打开 contact.db 失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 contact.db 失败: %w", err)
	}
	var cipherVersion string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA cipher_version").Scan(&cipherVersion); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("读取 SQLCipher 版本失败: %w", err)
	}
	if !strings.HasPrefix(cipherVersion, "4.") {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("不支持的 SQLCipher 版本: %s", cipherVersion)
	}
	var schemaEntries int
	if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sqlite_master").Scan(&schemaEntries); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("解密 contact.db 失败: %w", err)
	}
	if err := conn.Close(); err != nil {
		db.Close()
		return nil, fmt.Errorf("释放 contact.db 连接失败: %w", err)
	}
	return db, nil
}

func escapeLikePattern(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(value)
}
