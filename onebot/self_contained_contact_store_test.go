package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wxproto "github.com/k7xfgj269v-hash/wechat-chatter-workbench/onebot/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const syntheticContactKey = "1111111111111111111111111111111111111111111111111111111111111111"

func TestSQLCipherContactStoreIsEncryptedReadOnlyAndBounded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "contact.db")
	createSyntheticEncryptedContactDB(t, dbPath, syntheticContactKey)

	header, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(header) < 16 {
		t.Fatalf("encrypted database header length = %d", len(header))
	}
	if string(header[:16]) == "SQLite format 3\x00" {
		t.Fatal("database header is plaintext SQLite")
	}

	snapshot, err := loadContactSnapshotFrom(dbPath, syntheticContactKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Contacts) != 207 {
		t.Fatalf("contacts = %d, want 207", len(snapshot.Contacts))
	}
	if len(snapshot.Groups) != 1 || snapshot.Groups[0].Username != "synthetic-team@chatroom" {
		t.Fatalf("groups = %#v", snapshot.Groups)
	}
	if len(snapshot.Members) != 2 ||
		snapshot.Members[0].GroupID != "synthetic-team@chatroom" {
		t.Fatalf("members = %#v", snapshot.Members)
	}

	results, err := querySqlcipher(dbPath, syntheticContactKey, "Synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != maxContactSearchResults {
		t.Fatalf("bounded results = %d, want %d", len(results), maxContactSearchResults)
	}

	results, err = querySqlcipher(dbPath, syntheticContactKey, "missing' OR 1=1 --")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("injection-shaped keyword returned %d contacts", len(results))
	}

	results, err = querySqlcipher(dbPath, syntheticContactKey, "%")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Username != "wxid_literal_percent" {
		t.Fatalf("literal wildcard results = %#v", results)
	}

	db, err := openSQLCipherReadOnly(dbPath, syntheticContactKey)
	if err != nil {
		t.Fatal(err)
	}
	var cipherVersion string
	if err := db.QueryRow("PRAGMA cipher_version").Scan(&cipherVersion); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if !strings.HasPrefix(cipherVersion, "4.") {
		db.Close()
		t.Fatalf("cipher version = %q", cipherVersion)
	}
	if _, err := db.Exec("INSERT INTO contact (id, username) VALUES (?, ?)", 9999, "wxid_write"); err == nil {
		db.Close()
		t.Fatal("read-only contact database accepted an insert")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	wrongKey := strings.Repeat("2", 64)
	if _, err := openSQLCipherReadOnly(dbPath, wrongKey); err == nil {
		t.Fatal("opening encrypted database with wrong key succeeded")
	}
	if _, err := openSQLCipherReadOnly(dbPath, "not-a-key"); err == nil {
		t.Fatal("invalid key format succeeded")
	}
}

func TestLoadContactSnapshotSelectsDatabaseMatchingCurrentKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := filepath.Join(
		home,
		"Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files",
	)
	wrongPath := filepath.Join(base, "wxid_first_0001", "db_storage/contact/contact.db")
	rightPath := filepath.Join(base, "wxid_second_0002", "db_storage/contact/contact.db")
	if err := os.MkdirAll(filepath.Dir(wrongPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(rightPath), 0o700); err != nil {
		t.Fatal(err)
	}
	createSyntheticEncryptedContactDB(t, wrongPath, strings.Repeat("2", 64))
	createSyntheticEncryptedContactDB(t, rightPath, syntheticContactKey)

	keysPath := filepath.Join(home, ".wx-rs", "keys.json")
	if err := os.MkdirAll(filepath.Dir(keysPath), 0o700); err != nil {
		t.Fatal(err)
	}
	keys, err := json.Marshal(map[string]any{
		"entries": map[string]any{
			"contact/contact.db": map[string]string{
				"key_hex": syntheticContactKey,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keysPath, keys, 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := loadContactSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Contacts) != 207 || snapshot.Contacts[0].Username == "" {
		t.Fatalf("loaded contact snapshot = %+v", snapshot)
	}
}

func TestLoadContactSnapshotUsesConfiguredAccountKeyOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := filepath.Join(
		home,
		"Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files",
	)
	oldPath := filepath.Join(base, "wxid_old_0001", "db_storage/contact/contact.db")
	currentAccount := "wxid_current_0002"
	currentPath := filepath.Join(base, currentAccount, "db_storage/contact/contact.db")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	currentKey := strings.Repeat("3", 64)
	createSyntheticEncryptedContactDB(t, oldPath, syntheticContactKey)
	createSyntheticEncryptedContactDB(t, currentPath, currentKey)

	keysPath := filepath.Join(home, ".wx-rs", "keys.json")
	if err := os.MkdirAll(filepath.Dir(keysPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticContactKey(t, keysPath, syntheticContactKey)
	config, err := json.Marshal(map[string]string{
		"account_wxid": currentAccount,
		"db_dir":       filepath.Dir(filepath.Dir(currentPath)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".wx-rs", "config.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadContactSnapshot(); err == nil {
		t.Fatal("configured account accepted another account's contact key")
	} else if !strings.Contains(err.Error(), currentAccount) {
		t.Fatalf("configured account error = %q", err)
	}

	writeSyntheticContactKey(t, keysPath, currentKey)
	snapshot, err := loadContactSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Contacts) != 207 {
		t.Fatalf("configured account contacts = %d, want 207", len(snapshot.Contacts))
	}
}

func TestSelfContainedModuleAndProtoIdentity(t *testing.T) {
	const (
		newModule = "github.com/k7xfgj269v-hash/wechat-chatter-workbench/onebot"
	)
	oldModule := strings.Join([]string{
		"github.com",
		"yincongcyincong",
		"weixin-macos",
		"onebot",
	}, "/")

	moduleFile, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(moduleFile), "module "+newModule+"\n") {
		t.Fatalf("go.mod module path is not %s", newModule)
	}

	var sourceFiles []string
	rootEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range rootEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			sourceFiles = append(sourceFiles, entry.Name())
		}
	}
	protoEntries, err := os.ReadDir("proto")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range protoEntries {
		if !entry.IsDir() &&
			(strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), ".proto")) {
			sourceFiles = append(sourceFiles, filepath.Join("proto", entry.Name()))
		}
	}
	for _, path := range sourceFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), oldModule) {
			t.Errorf("%s still references old module path", path)
		}
	}

	searchSource, err := os.ReadFile("search_contact.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"os/exec"`, "exec.LookPath", "exec.Command"} {
		if strings.Contains(string(searchSource), forbidden) {
			t.Errorf("search_contact.go still contains %q", forbidden)
		}
	}

	vendorModules, err := os.ReadFile("vendor/modules.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(vendorModules), "github.com/mutecomm/go-sqlcipher/v4 v4.4.2") {
		t.Fatal("vendored module metadata does not include go-sqlcipher/v4")
	}

	descriptors := []protoreflect.FileDescriptor{
		wxproto.File_app_msg_proto,
		wxproto.File_check_md5_proto,
		wxproto.File_common_resp_proto,
		wxproto.File_file_msg_proto,
		wxproto.File_img_msg_proto,
		wxproto.File_send_app_msg_proto,
		wxproto.File_text_msg_proto,
		wxproto.File_upload_app_attach_proto,
		wxproto.File_video_msg_proto,
		wxproto.File_voice_msg_proto,
		wxproto.File_wx_msg_proto,
	}
	for _, descriptor := range descriptors {
		options, ok := descriptor.Options().(*descriptorpb.FileOptions)
		if !ok {
			t.Fatalf("%s options type = %T", descriptor.Path(), descriptor.Options())
		}
		if options.GetGoPackage() != newModule+"/proto;wxproto" {
			t.Errorf("%s go_package = %q", descriptor.Path(), options.GetGoPackage())
		}
	}
}

func writeSyntheticContactKey(t *testing.T, path, keyHex string) {
	t.Helper()
	keys, err := json.Marshal(map[string]any{
		"entries": map[string]any{
			"contact/contact.db": map[string]string{
				"key_hex": keyHex,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, keys, 0o600); err != nil {
		t.Fatal(err)
	}
}

func createSyntheticEncryptedContactDB(t *testing.T, dbPath, keyHex string) {
	t.Helper()
	if !hexKeyRe.MatchString(keyHex) {
		t.Fatalf("synthetic key is not 64 hex characters")
	}
	fileURL := &url.URL{Scheme: "file", Path: dbPath}
	params := fileURL.Query()
	params.Set("mode", "rwc")
	params.Set("_pragma_key", "x'"+keyHex+"'")
	params.Set("_pragma_cipher_page_size", "4096")
	fileURL.RawQuery = params.Encode()

	db, err := sql.Open("sqlite3", fileURL.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.Exec(`
CREATE TABLE contact (
	id INTEGER PRIMARY KEY,
	username TEXT,
	nick_name TEXT,
	alias TEXT,
	remark TEXT,
	local_type INTEGER,
	big_head_url TEXT,
	small_head_url TEXT,
	is_in_chat_room INTEGER,
	description TEXT
);
CREATE TABLE chat_room (
	id INTEGER PRIMARY KEY,
	username TEXT,
	owner TEXT
);
CREATE TABLE chatroom_member (
	room_id INTEGER,
	member_id INTEGER
);`); err != nil {
		t.Fatal(err)
	}

	insertContact, err := db.Prepare(`
INSERT INTO contact (
	id, username, nick_name, alias, remark, local_type,
	big_head_url, small_head_url, is_in_chat_room, description
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer insertContact.Close()
	for i := 1; i <= 205; i++ {
		if _, err := insertContact.Exec(
			i,
			fmt.Sprintf("wxid_synthetic_%03d", i),
			fmt.Sprintf("Synthetic Contact %03d", i),
			fmt.Sprintf("alias_%03d", i),
			"",
			3,
			"",
			"",
			0,
			"",
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := insertContact.Exec(
		1000,
		"synthetic-team@chatroom",
		"Synthetic Team",
		"",
		"",
		2,
		"",
		"",
		1,
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := insertContact.Exec(
		1001,
		"wxid_literal_percent",
		"100% match",
		"",
		"",
		3,
		"",
		"",
		0,
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO chat_room (id, username, owner) VALUES (?, ?, ?)",
		10,
		"synthetic-team@chatroom",
		"wxid_synthetic_001",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO chatroom_member (room_id, member_id) VALUES (?, ?), (?, ?)",
		10,
		1,
		10,
		2,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
