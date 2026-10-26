package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestForwardRuleStorePersistsDeterministicRulesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	crm, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewForwardRuleStore(crm)

	if store.MatchAndReserve(nil, time.Now()) != true {
		t.Fatal("no-rule compatibility = false")
	}
	first, err := store.Create(" group@chatroom ", []string{" job ", "job", " "}, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "rule_00000001" {
		t.Fatalf("first ID = %q", first.ID)
	}
	if first.GroupID != "group@chatroom" || !reflect.DeepEqual(first.Keywords, []string{"job"}) {
		t.Fatalf("normalized rule = %+v", first)
	}
	second, err := store.Create("", nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "rule_00000002" {
		t.Fatalf("second ID = %q", second.ID)
	}

	record := &ForwardRecord{GroupID: "group@chatroom", Text: "new job available"}
	now := time.Now()
	if !store.MatchAndReserve(record, now) {
		t.Fatal("initial match = false")
	}
	if store.MatchAndReserve(record, now.Add(time.Second)) {
		t.Fatal("cooldown match = true")
	}
	if err := crm.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedCRM, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopenedCRM.Close()
	})
	reopened := NewForwardRuleStore(reopenedCRM)
	if got := reopened.List(); !reflect.DeepEqual(got, []ForwardRule{first, second}) {
		t.Fatalf("reopened rules = %#v", got)
	}
	if !reopened.MatchAndReserve(record, now.Add(time.Second)) {
		t.Fatal("runtime cooldown was persisted")
	}
}

func TestForwardRuleStoreMatchesAndReservesCooldown(t *testing.T) {
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	store := NewForwardRuleStore(crm)
	rule, err := store.Create("group@chatroom", []string{"job", "job", " "}, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.Keywords) != 1 || rule.Keywords[0] != "job" {
		t.Fatalf("keywords = %#v", rule.Keywords)
	}

	record := &ForwardRecord{GroupID: "group@chatroom", Text: "new job available"}
	now := time.Now()
	if !store.MatchAndReserve(record, now) {
		t.Fatal("first match = false")
	}
	if store.MatchAndReserve(record, now.Add(59*time.Second)) {
		t.Fatal("cooldown match = true")
	}
	if !store.MatchAndReserve(record, now.Add(60*time.Second)) {
		t.Fatal("cooldown boundary match = false")
	}
	if store.MatchAndReserve(&ForwardRecord{Text: "new job available"}, now.Add(2*time.Minute)) {
		t.Fatal("private message matched with rules configured")
	}

	disabled := false
	if _, found, err := store.Update(rule.ID, updateForwardRuleRequest{Enabled: &disabled}); err != nil || !found {
		t.Fatalf("Update = found:%v err:%v", found, err)
	}
	if store.MatchAndReserve(record, now.Add(3*time.Minute)) {
		t.Fatal("disabled rule matched")
	}
}

func TestForwardRuleStoreCooldownReservationIsConcurrent(t *testing.T) {
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	store := NewForwardRuleStore(crm)
	if _, err := store.Create("", nil, 30, true); err != nil {
		t.Fatal(err)
	}
	record := &ForwardRecord{GroupID: "group@chatroom", Text: "anything"}
	now := time.Now()
	var matched atomic.Int32
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if store.MatchAndReserve(record, now) {
				matched.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := matched.Load(); got != 1 {
		t.Fatalf("matched = %d, want 1", got)
	}
}

func TestForwardRuleStoreMutationFailuresRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	crm, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	store := NewForwardRuleStore(crm)
	first, err := store.Create("group@chatroom", []string{"job"}, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	beforeDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(blockingFile, "crm.json")
	originalPath := crm.path

	crm.path = badPath
	if _, err := store.Create("", nil, 0, true); !errors.Is(err, ErrCRMPersistence) {
		t.Fatalf("Create failure = %v", err)
	}
	crm.path = originalPath
	if got := crm.Snapshot(); got.Counters.Rule != 1 || len(got.ForwardingRules) != 1 {
		t.Fatalf("state after failed create = %+v", got)
	}
	second, err := store.Create("", nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "rule_00000002" {
		t.Fatalf("ID after failed create = %q", second.ID)
	}

	disabled := false
	crm.path = badPath
	if _, found, err := store.Update(first.ID, updateForwardRuleRequest{Enabled: &disabled}); !found || !errors.Is(err, ErrCRMPersistence) {
		t.Fatalf("Update failure = found:%v err:%v", found, err)
	}
	crm.path = originalPath
	if got := store.List()[0]; !got.Enabled {
		t.Fatalf("rule changed after failed update = %+v", got)
	}

	crm.path = badPath
	if deleted, err := store.forwardRuleDelete(first.ID); deleted || !errors.Is(err, ErrCRMPersistence) {
		t.Fatalf("Delete failure = deleted:%v err:%v", deleted, err)
	}
	crm.path = originalPath
	if got := store.List(); len(got) != 2 || got[0].ID != first.ID {
		t.Fatalf("rules changed after failed delete = %+v", got)
	}

	afterDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(afterDisk, beforeDisk) {
		t.Fatal("successful second create did not update disk")
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Snapshot().ForwardingRules; len(got) != 2 || got[0].Enabled != true {
		t.Fatalf("persisted rules after failures = %+v", got)
	}
}

func TestForwardRulesHandlerCRUDAndPersistenceFailure(t *testing.T) {
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	store := NewForwardRuleStore(crm)
	mux := http.NewServeMux()
	registerForwardRuleRoutes(mux, store)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/forward_rules", bytes.NewBufferString(`{"group_id":"group@chatroom","keywords":["job"],"cooldown_seconds":5}`)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var created ForwardRule
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "rule_00000001" || !created.Enabled {
		t.Fatalf("created rule = %+v", created)
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/forward_rules?id="+created.ID, bytes.NewBufferString(`{"enabled":false,"keywords":[],"cooldown_seconds":0}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/forward_rules", nil))
	if response.Code != http.StatusOK || response.Body.String() != `[{"id":"rule_00000001","group_id":"group@chatroom","keywords":[],"cooldown_seconds":0,"enabled":false}]`+"\n" {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/forward_rules?id="+created.ID, nil))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/forward_rules", bytes.NewBufferString(`null`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("non-object status = %d, body = %s", response.Code, response.Body.String())
	}

	persisted, err := store.Create("group@chatroom", []string{"persisted"}, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalPath := crm.path
	crm.path = filepath.Join(blockingFile, "crm.json")
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/forward_rules", bytes.NewBufferString(`{"group_id":"","keywords":[],"cooldown_seconds":0}`)),
		httptest.NewRequest(http.MethodPut, "/api/forward_rules?id="+persisted.ID, bytes.NewBufferString(`{"enabled":false}`)),
		httptest.NewRequest(http.MethodDelete, "/api/forward_rules?id="+persisted.ID, nil),
	} {
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError || response.Body.String() != `{"error":"保存转发规则失败"}`+"\n" {
			t.Fatalf("%s persistence status = %d, body = %s", request.Method, response.Code, response.Body.String())
		}
	}
	crm.path = originalPath
	if got := store.List(); len(got) != 1 || got[0].ID != persisted.ID || !got[0].Enabled {
		t.Fatalf("rules changed after HTTP persistence failures = %+v", got)
	}
}
