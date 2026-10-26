package main

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCRMStoreUpdateRealState measures a minimal CRM mutation against
// the real production snapshot (8.6MB), mirroring the automation benchmark.
func BenchmarkCRMStoreUpdateRealState(b *testing.B) {
	for _, name := range []string{} {
		_ = os.Unsetenv(name)
	}
	root := b.TempDir()
	path := filepath.Join(root, "workbench-crm.json")
	productionState := "<home>/.wechat-chatter/workbench-crm.json"
	data, err := os.ReadFile(productionState)
	if err != nil {
		b.Skipf("production crm state unavailable: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := OpenCRMStore(path)
	if err != nil {
		b.Fatalf("OpenCRMStore() error = %v", err)
	}
	b.Cleanup(func() {
		_ = store.Close()
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.Update(func(state *CRMState, _ *IDAllocator) error {
			// UpdateWithMeta always appends a CRM audit event; keep the
			// mutation minimal (an empty callback is the smallest change).
			return nil
		})
		if err != nil {
			b.Fatalf("Update() error = %v", err)
		}
	}
}

// BenchmarkCRMStoreSnapshotRealState measures the full-clone read path used
// by the 26 non-test Snapshot() call sites.
func BenchmarkCRMStoreSnapshotRealState(b *testing.B) {
	root := b.TempDir()
	path := filepath.Join(root, "workbench-crm.json")
	productionState := "<home>/.wechat-chatter/workbench-crm.json"
	data, err := os.ReadFile(productionState)
	if err != nil {
		b.Skipf("production crm state unavailable: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := OpenCRMStore(path)
	if err != nil {
		b.Fatalf("OpenCRMStore() error = %v", err)
	}
	b.Cleanup(func() {
		_ = store.Close()
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.Snapshot()
	}
}
