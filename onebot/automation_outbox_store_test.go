package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAutomationOutboxEnsureDuplicateQueriesAndAtomicFailures(t *testing.T) {
	store := automationOutboxTestStore(t)
	ctx := context.Background()
	action := store.Snapshot().Actions[0]

	first, created, err := store.EnsurePending(ctx, action.ID, action.Recipient.PeerID, "hello")
	if err != nil || !created {
		t.Fatalf("first EnsurePending() = %#v, %v, %v", first, created, err)
	}
	second, created, err := store.Ensure(ctx, action, action.Recipient.PeerID, "changed duplicate text")
	if err != nil || created || second.ID != first.ID || second.Text != first.Text {
		t.Fatalf("duplicate Ensure() = %#v, %v, %v", second, created, err)
	}
	byID, err := store.GetOutboxByID(ctx, first.ID)
	if err != nil || byID.ID != first.ID {
		t.Fatalf("GetOutboxByID() = %#v, %v", byID, err)
	}
	byAction, err := store.GetOutboxByActionID(ctx, action.ID)
	if err != nil || byAction.ID != first.ID {
		t.Fatalf("GetOutboxByActionID() = %#v, %v", byAction, err)
	}
	pending, err := store.ListPending(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("ListPending() = %#v, %v", pending, err)
	}

	before := store.Snapshot()
	if _, _, err := store.EnsurePending(ctx, "aiact_99999999", action.Recipient.PeerID, "hello"); !errors.Is(err, ErrAutomationOutboxNotFound) {
		t.Fatalf("missing action error = %v, want not found", err)
	}
	automationOutboxAssertStateEqual(t, store.Snapshot(), before)

	other := automationOutboxTestAddReplacementAction(t, store)
	injected := errors.New("injected persistence failure")
	store.beforePersistenceStep = func(step string) error {
		if step == automationPersistenceStepReplaceSnapshot {
			return injected
		}
		return nil
	}
	before = store.Snapshot()
	if _, _, err := store.EnsurePending(ctx, other.ID, other.Recipient.PeerID, "second"); !errors.Is(err, ErrAutomationPersistence) || !errors.Is(err, injected) {
		t.Fatalf("persistence error = %v", err)
	}
	automationOutboxAssertStateEqual(t, store.Snapshot(), before)
}

func TestAutomationOutboxEnsureAndClaimConcurrency(t *testing.T) {
	store := automationOutboxTestStore(t)
	ctx := context.Background()
	action := store.Snapshot().Actions[0]

	const workers = 24
	var createdCount atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, created, err := store.EnsurePending(ctx, action.ID, action.Recipient.PeerID, "hello")
			if err != nil {
				errs <- err
				return
			}
			if created {
				createdCount.Add(1)
			}
			ids <- entry.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatal(err)
	}
	if createdCount.Load() != 1 {
		t.Fatalf("created count = %d, want 1", createdCount.Load())
	}
	var outboxID string
	for id := range ids {
		if outboxID == "" {
			outboxID = id
		}
		if id != outboxID {
			t.Fatalf("outbox ID = %q, want %q", id, outboxID)
		}
	}

	var claimedCount atomic.Int32
	errs = make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, claimed, err := store.ClaimPending(ctx, outboxID)
			if err != nil {
				errs <- err
				return
			}
			if claimed {
				claimedCount.Add(1)
				if entry.Status != AutomationOutboxDispatching || entry.Attempt != 1 {
					errs <- errors.New("claimed entry has invalid dispatch state")
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if claimedCount.Load() != 1 {
		t.Fatalf("claimed count = %d, want 1", claimedCount.Load())
	}

	state := store.Snapshot()
	if len(state.Outbox) != 1 || len(state.PendingAudit) != 2 {
		t.Fatalf("outbox/audit counts = %d/%d, want 1/2", len(state.Outbox), len(state.PendingAudit))
	}
}

func TestAutomationOutboxRestartRecoveryPreservesTerminalAndPending(t *testing.T) {
	store := automationOutboxTestStore(t)
	ctx := context.Background()
	dispatching := automationOutboxTestEnsureAndClaim(t, store)
	pendingAction := automationOutboxTestAddReplacementAction(t, store)
	pending, created, err := store.EnsurePending(ctx, pendingAction.ID, pendingAction.Recipient.PeerID, "pending")
	if err != nil || !created {
		t.Fatalf("EnsurePending(pending) = %#v, %v, %v", pending, created, err)
	}
	sent := automationOutboxTestCreateTerminal(t, store, AutomationDeliverySent)
	unknown := automationOutboxTestCreateTerminal(t, store, AutomationDeliveryFailed)

	dataDir := filepath.Dir(store.path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	recovered, err := reopened.RecoverDispatchingOutbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].ID != dispatching.ID ||
		recovered[0].Status != AutomationOutboxUnknown ||
		!recovered[0].DispatchAttempted {
		t.Fatalf("recovered = %#v", recovered)
	}
	gotPending, err := reopened.GetOutboxByID(ctx, pending.ID)
	if err != nil || gotPending.Status != AutomationOutboxPending {
		t.Fatalf("pending after recovery = %#v, %v", gotPending, err)
	}
	gotSent, err := reopened.GetOutboxByID(ctx, sent.ID)
	if err != nil || gotSent.Status != AutomationOutboxSent {
		t.Fatalf("sent after recovery = %#v, %v", gotSent, err)
	}
	gotUnknown, err := reopened.GetOutboxByID(ctx, unknown.ID)
	if err != nil || gotUnknown.Status != AutomationOutboxUnknown {
		t.Fatalf("unknown after recovery = %#v, %v", gotUnknown, err)
	}
	again, err := reopened.RecoverDispatchingOutbox(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second recovery = %#v, %v", again, err)
	}
}

func TestAutomationOutboxDispatchStateMachine(t *testing.T) {
	t.Run("pre acceptance failure", func(t *testing.T) {
		store := automationOutboxTestStore(t)
		entry := automationOutboxTestEnsureAndClaim(t, store)
		completed, err := store.CompleteDispatch(context.Background(), entry.ID, AutomationDeliveryResult{
			Status:       AutomationDeliveryFailed,
			ErrorCode:    "canceled",
			ErrorMessage: "context canceled",
		})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != AutomationOutboxFailed || completed.DispatchAttempted {
			t.Fatalf("completed = %#v, want definite failed", completed)
		}
		if _, claimed, err := store.ClaimPending(context.Background(), entry.ID); err != nil || claimed {
			t.Fatalf("terminal ClaimPending() = claimed %v, err %v", claimed, err)
		}
		if _, err := store.MarkDispatchAccepted(context.Background(), entry.ID); !errors.Is(err, ErrAutomationOutboxConflict) {
			t.Fatalf("MarkDispatchAccepted() error = %v, want conflict", err)
		}
	})

	t.Run("accepted fake success", func(t *testing.T) {
		store := automationOutboxTestStore(t)
		entry := automationOutboxTestEnsureAndClaim(t, store)
		accepted, err := store.MarkDispatchAccepted(context.Background(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !accepted.DispatchAttempted || accepted.Status != AutomationOutboxDispatching {
			t.Fatalf("accepted = %#v", accepted)
		}
		completed, err := store.CompleteDispatch(context.Background(), entry.ID, AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != AutomationOutboxSent || !completed.DispatchAttempted {
			t.Fatalf("completed = %#v, want sent", completed)
		}
	})

	t.Run("accepted error becomes unknown", func(t *testing.T) {
		store := automationOutboxTestStore(t)
		entry := automationOutboxTestEnsureAndClaim(t, store)
		completed, err := store.CompleteDispatch(context.Background(), entry.ID, AutomationDeliveryResult{
			Status:            AutomationDeliveryFailed,
			DispatchAttempted: true,
			ErrorCode:         "worker_error",
		})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != AutomationOutboxUnknown || !completed.DispatchAttempted {
			t.Fatalf("completed = %#v, want unknown", completed)
		}
		if _, err := store.CompleteDispatch(context.Background(), entry.ID, AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		}); !errors.Is(err, ErrAutomationOutboxConflict) {
			t.Fatalf("terminal CompleteDispatch() error = %v, want conflict", err)
		}
	})
}

func automationOutboxTestStore(t *testing.T) *AutomationStore {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	state := automationStoreTestStateWithoutOutbox()
	state.Counters.Outbox = 0
	state.Counters.Audit = 0
	state.PendingAudit = []AutomationAuditRecord{}
	if err := store.Update(func(current *AutomationState, allocator *AutomationIDAllocator) error {
		*current = state
		for range state.Counters.Run {
			if _, err := allocator.Next(AutomationIDKindRun); err != nil {
				return err
			}
		}
		for range state.Counters.Action {
			if _, err := allocator.Next(AutomationIDKindAction); err != nil {
				return err
			}
		}
		for range state.Counters.Approval {
			if _, err := allocator.Next(AutomationIDKindApproval); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func automationOutboxTestEnsureAndClaim(t *testing.T, store *AutomationStore) AutomationOutboxEntry {
	t.Helper()
	action := store.Snapshot().Actions[0]
	entry, created, err := store.EnsurePending(
		context.Background(),
		action.ID,
		action.Recipient.PeerID,
		"hello",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("EnsurePending() created = false")
	}
	entry, claimed, err := store.ClaimPending(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("ClaimPending() claimed = false")
	}
	return entry
}

func automationOutboxTestAddReplacementAction(t *testing.T, store *AutomationStore) AutomationAction {
	t.Helper()
	var action AutomationAction
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		action = state.Actions[0]
		action.ID = id
		action.ProviderCallID = "reply-" + id
		action.RetryOfActionID = state.Actions[0].ID
		action.Result = nil
		state.Actions = append(state.Actions, action)
		state.Runs[0].ActionIDs = append(state.Runs[0].ActionIDs, id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func automationOutboxTestCreateTerminal(
	t *testing.T,
	store *AutomationStore,
	deliveryStatus AutomationDeliveryStatus,
) AutomationOutboxEntry {
	t.Helper()
	action := automationOutboxTestAddReplacementAction(t, store)
	entry, created, err := store.EnsurePending(
		context.Background(),
		action.ID,
		action.Recipient.PeerID,
		"terminal",
	)
	if err != nil || !created {
		t.Fatalf("EnsurePending(terminal) = %#v, %v, %v", entry, created, err)
	}
	entry, claimed, err := store.ClaimPending(context.Background(), entry.ID)
	if err != nil || !claimed {
		t.Fatalf("ClaimPending(terminal) = %#v, %v, %v", entry, claimed, err)
	}
	entry, err = store.CompleteDispatch(context.Background(), entry.ID, AutomationDeliveryResult{
		Status:            deliveryStatus,
		DispatchAttempted: true,
		ErrorCode:         "test_delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func automationOutboxAssertStateEqual(t *testing.T, got, want AutomationState) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state changed after failed operation\ngot:  %#v\nwant: %#v", got, want)
	}
}
