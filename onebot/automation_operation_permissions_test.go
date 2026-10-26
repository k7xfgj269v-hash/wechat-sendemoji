package main

import "testing"

func TestWorkbenchOperationOverrideTightensImplementedOnly(t *testing.T) {
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	if entry, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery, WorkbenchOperationContactsSearch,
	); !ok || entry.Availability != WorkbenchAvailabilityAvailable {
		t.Fatalf("baseline availability = %+v ok=%v", entry, ok)
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(AutomationToolWorkbenchQuery, WorkbenchOperationContactsSearch): {},
		workbenchOperationDenyKey(AutomationToolWorkbenchQuery, WorkbenchOperationSystemHealth):   {},
	})

	if entry, _ := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery, WorkbenchOperationContactsSearch,
	); entry.Availability != WorkbenchAvailabilityUnavailable {
		t.Fatalf("denied implemented op availability = %q", entry.Availability)
	}
	// Overriding an unimplemented op must not make it available.
	if entry, _ := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery, WorkbenchOperationSystemHealth,
	); entry.Availability != WorkbenchAvailabilityUnavailable {
		t.Fatalf("unimplemented op availability = %q", entry.Availability)
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{})
	if entry, _ := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery, WorkbenchOperationContactsSearch,
	); entry.Availability != WorkbenchAvailabilityAvailable {
		t.Fatalf("restored availability = %q", entry.Availability)
	}
}

func TestAutomationOperationPermissionStorePersistsAndValidates(t *testing.T) {
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	dir := t.TempDir()
	store, err := NewAutomationOperationPermissionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.View().Version != 0 {
		t.Fatalf("initial version = %d", store.View().Version)
	}

	updated, err := store.Update(AutomationOperationPermissionsUpdate{
		ExpectedVersion: 0,
		Denied: []AutomationOperationPermissionRef{
			{Tool: AutomationToolWorkbenchQuery, Operation: WorkbenchOperationContactsSearch},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 1 {
		t.Fatalf("updated version = %d", updated.Version)
	}
	if !workbenchOperationOverrideDenied(AutomationToolWorkbenchQuery, WorkbenchOperationContactsSearch) {
		t.Fatal("runtime override was not applied")
	}

	// Unimplemented operations cannot be toggled.
	if _, err := store.Update(AutomationOperationPermissionsUpdate{
		ExpectedVersion: 1,
		Denied: []AutomationOperationPermissionRef{
			{Tool: AutomationToolWorkbenchQuery, Operation: WorkbenchOperationSystemHealth},
		},
	}); err == nil {
		t.Fatal("expected rejection for unimplemented operation")
	}

	// Stale version is rejected.
	if _, err := store.Update(AutomationOperationPermissionsUpdate{ExpectedVersion: 99}); err == nil {
		t.Fatal("expected version conflict")
	}

	reloaded, err := NewAutomationOperationPermissionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	view := reloaded.View()
	if view.Version != 1 {
		t.Fatalf("reloaded version = %d", view.Version)
	}
	denied := false
	for _, op := range view.Operations {
		if op.Tool == AutomationToolWorkbenchQuery &&
			op.Operation == WorkbenchOperationContactsSearch {
			denied = op.Denied
		}
	}
	if !denied {
		t.Fatal("reloaded deny state was not persisted")
	}
}
