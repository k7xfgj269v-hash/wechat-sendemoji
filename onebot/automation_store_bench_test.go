package main

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkAutomationStoreUpdateRealState measures a minimal state mutation
// against the real production snapshot (12MB, 7k sessions/turns/runs), which
// is the hot path behind every approval, action and chat event.
func BenchmarkAutomationStoreUpdateRealState(b *testing.B) {
	for _, name := range []string{
		automationRetentionSessionsEnv,
		automationRetentionTurnsEnv,
		automationRetentionRunsEnv,
		automationRetentionIdempotencyEnv,
		automationRetentionPendingAuditEnv,
	} {
		if err := os.Unsetenv(name); err != nil {
			b.Fatal(err)
		}
	}
	root := b.TempDir()
	dataDir := filepath.Join(root, "automation")
	productionState := "<home>/.wechat-chatter/automation/state.json"
	data, err := os.ReadFile(productionState)
	if err != nil {
		b.Skipf("production state unavailable: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), data, 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		b.Fatalf("OpenAutomationStore() error = %v", err)
	}
	b.Cleanup(func() {
		_ = store.Close()
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if err != nil {
			b.Fatalf("Update() error = %v", err)
		}
	}
}
