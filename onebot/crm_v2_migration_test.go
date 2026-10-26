package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCRMStoreMigratesV1AtomicallyAndReopensIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	legacy := crmMigrationTestStateV1()
	legacyBytes, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes = append(legacyBytes, '\n')
	if err := os.WriteFile(path, legacyBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore(v1) error = %v", err)
	}
	state := store.Snapshot()
	if state.SchemaVersion != CRMSchemaVersion {
		t.Fatalf("schema version = %d, want %d", state.SchemaVersion, CRMSchemaVersion)
	}
	if state.Counters.Job != legacy.Counters.Job ||
		state.Counters.StageEvent != 0 ||
		state.Counters.Onboarding != 0 ||
		state.Counters.ServiceRequest != 0 ||
		state.Counters.FundCallback != 0 ||
		state.Counters.Audit != 0 {
		t.Fatalf("migrated counters = %+v", state.Counters)
	}
	if len(state.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(state.Jobs))
	}
	job := state.Jobs[0]
	if job.ID != legacy.Jobs[0].ID ||
		job.Title != legacy.Jobs[0].Title ||
		job.Priority != JobPriorityNormal ||
		job.RequiredDocuments == nil ||
		len(job.RequiredDocuments) != 0 ||
		!job.CreatedAt.Equal(legacy.Jobs[0].CreatedAt) ||
		!job.UpdatedAt.Equal(legacy.Jobs[0].UpdatedAt) {
		t.Fatalf("unexpected migrated job: %#v", job)
	}
	assertCRMStateSlicesNonNil(t, state)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	migratedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(migratedBytes, legacyBytes) {
		t.Fatal("v1 snapshot was not rewritten")
	}
	var persisted map[string]any
	if err := json.Unmarshal(migratedBytes, &persisted); err != nil {
		t.Fatal(err)
	}
	if got := int(persisted["schema_version"].(float64)); got != CRMSchemaVersion {
		t.Fatalf("persisted schema version = %d, want %d", got, CRMSchemaVersion)
	}
	jobs := persisted["jobs"].([]any)
	persistedJob := jobs[0].(map[string]any)
	if persistedJob["priority"] != string(JobPriorityNormal) {
		t.Fatalf("persisted priority = %#v", persistedJob["priority"])
	}
	if documents, ok := persistedJob["required_documents"].([]any); !ok || len(documents) != 0 {
		t.Fatalf("persisted required_documents = %#v, want []", persistedJob["required_documents"])
	}
	assertCRMMode(t, path, 0o600)

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore(v2) error = %v", err)
	}
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, state) {
		t.Fatalf("idempotent reopen state = %#v, want %#v", got, state)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reopenedBytes, migratedBytes) {
		t.Fatal("opening an already migrated v2 snapshot rewrote its bytes")
	}
}

func TestDecodeCRMStateWithMigrationRejectsInvalidSchemaShapes(t *testing.T) {
	legacy := crmMigrationTestStateV1()
	validV1, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	future := newCRMState()
	future.SchemaVersion = CRMSchemaVersion + 1
	futureBytes, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}

	var v1Object map[string]any
	if err := json.Unmarshal(validV1, &v1Object); err != nil {
		t.Fatal(err)
	}
	v1Object["audit_events"] = []any{}
	partialRoot, err := json.Marshal(v1Object)
	if err != nil {
		t.Fatal(err)
	}
	delete(v1Object, "audit_events")
	v1Object["jobs"].([]any)[0].(map[string]any)["required_documents"] = []any{}
	partialJob, err := json.Marshal(v1Object)
	if err != nil {
		t.Fatal(err)
	}

	v2 := newCRMState()
	v2.Counters.Job = 1
	v2.Jobs = append(v2.Jobs, crmStoreTestJob("job_00000001"))
	v2Bytes, err := json.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	var v2Object map[string]any
	if err := json.Unmarshal(v2Bytes, &v2Object); err != nil {
		t.Fatal(err)
	}
	v2Object["jobs"].([]any)[0].(map[string]any)["required_documents"] = nil
	nullDocuments, err := json.Marshal(v2Object)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "schema zero", data: []byte(`{"schema_version":0}`)},
		{name: "future schema", data: futureBytes},
		{name: "v1 with v2 root field", data: partialRoot},
		{name: "v1 with v2 job field", data: partialJob},
		{name: "v2 null required documents", data: nullDocuments},
		{name: "missing schema", data: []byte(`{}`)},
		{name: "non integer schema", data: []byte(`{"schema_version":"1"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := decodeCRMStateWithMigration(test.data); !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("decodeCRMStateWithMigration() error = %v, want validation error", err)
			}
		})
	}
}

func TestMigrateCRMStateV1IsIdempotentAndRejectsPartialState(t *testing.T) {
	state := crmStateFromV1(crmMigrationTestStateV1())
	if err := migrateCRMStateV1(&state); err != nil {
		t.Fatalf("first migration error = %v", err)
	}
	first := cloneCRMState(state)
	if err := migrateCRMStateV1(&state); err != nil {
		t.Fatalf("second migration error = %v", err)
	}
	if !reflect.DeepEqual(state, first) {
		t.Fatalf("idempotent migration changed state: %#v != %#v", state, first)
	}

	partial := crmStateFromV1(crmMigrationTestStateV1())
	partial.Counters.Audit = 1
	if err := migrateCRMStateV1(&partial); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("partial migration error = %v, want validation error", err)
	}
}

func crmMigrationTestStateV1() crmStateV1 {
	now := time.Date(2026, time.July, 21, 10, 30, 0, 0, time.UTC)
	return crmStateV1{
		SchemaVersion: 1,
		Counters: crmIDCountersV1{
			Job: 7,
		},
		Candidates: []Candidate{},
		Jobs: []crmJobV1{
			{
				ID:             "job_00000007",
				Title:          "Legacy Backend Engineer",
				Company:        "Example",
				Location:       "Berlin",
				EmploymentType: "contract",
				Description:    "legacy description",
				Requirements:   []string{"Go"},
				Owner:          "operator",
				Status:         JobStatusOpen,
				Version:        3,
				CreatedAt:      now,
				UpdatedAt:      now.Add(time.Hour),
			},
		},
		Leads:           []Lead{},
		Tasks:           []FollowUpTask{},
		Notes:           []Note{},
		MessageEvents:   []MessageEvent{},
		ForwardingRules: []ForwardRule{},
	}
}
