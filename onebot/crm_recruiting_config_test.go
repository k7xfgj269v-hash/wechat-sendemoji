package main

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecruitingProjectAndSourceGroupsPersistWithAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}

	project := store.RecruitingProject()
	if project.OrganizationName != CRMDefaultOrganizationName ||
		project.Version != 1 ||
		!project.RequireAuthoritativeJob ||
		len(project.SourceGroups) != 0 {
		t.Fatalf("default project = %#v", project)
	}

	organizationName := "国爱招聘中心"
	project, err = store.UpdateRecruitingProject(project.Version, RecruitingProjectUpdate{
		OrganizationName: &organizationName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.OrganizationName != organizationName || project.Version != 2 {
		t.Fatalf("updated project = %#v", project)
	}

	project, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		GroupID:     "synthetic_jobs@chatroom",
		DisplayName: "合成职位来源",
		Enabled:     true,
		GateEnabled: true,
		GateMode:    RecruitingSourceGateModeJobRelevance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.Version != 3 || len(project.SourceGroups) != 1 {
		t.Fatalf("project after create = %#v", project)
	}
	source := project.SourceGroups[0]
	if source.ID != "source_00000001" ||
		source.GroupID != "synthetic_jobs@chatroom" ||
		source.DisplayName != "合成职位来源" ||
		!source.Enabled ||
		!source.GateEnabled ||
		source.GateMode != RecruitingSourceGateModeJobRelevance ||
		source.Version != 1 ||
		source.CreatedAt.IsZero() ||
		!source.UpdatedAt.Equal(source.CreatedAt) {
		t.Fatalf("created source = %#v", source)
	}

	project, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:          source.ID,
		GroupID:     source.GroupID,
		DisplayName: "合成职位订单",
		Enabled:     false,
		GateEnabled: false,
		GateMode:    RecruitingSourceGateModeJobRelevance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.Version != 4 ||
		project.SourceGroups[0].Version != 2 ||
		project.SourceGroups[0].DisplayName != "合成职位订单" ||
		project.SourceGroups[0].Enabled ||
		project.SourceGroups[0].GateEnabled {
		t.Fatalf("project after update = %#v", project)
	}

	unchanged, err := store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:          project.SourceGroups[0].ID,
		GroupID:     project.SourceGroups[0].GroupID,
		DisplayName: project.SourceGroups[0].DisplayName,
		Enabled:     project.SourceGroups[0].Enabled,
		GateEnabled: project.SourceGroups[0].GateEnabled,
		GateMode:    project.SourceGroups[0].GateMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != project.Version {
		t.Fatalf("no-op project version = %d, want %d", unchanged.Version, project.Version)
	}

	project, err = store.RemoveRecruitingSourceGroup(
		"synthetic_jobs@chatroom",
		project.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if project.Version != 5 || len(project.SourceGroups) != 0 {
		t.Fatalf("project after remove = %#v", project)
	}

	state := store.Snapshot()
	if len(state.AuditEvents) != 4 {
		t.Fatalf("audit events = %d, want 4", len(state.AuditEvents))
	}
	wantOperations := []string{
		"recruiting.project.update",
		"recruiting.source.create",
		"recruiting.source.update",
		"recruiting.source.remove",
	}
	for index, operation := range wantOperations {
		event := state.AuditEvents[index]
		if event.Actor != "operator" || event.Operation != operation {
			t.Fatalf("audit[%d] = %#v, want operation %q", index, event, operation)
		}
	}
	if state.AuditEvents[2].EntityType != "recruiting_source_group" ||
		state.AuditEvents[2].EntityID != "" {
		t.Fatalf("source update audit = %#v", state.AuditEvents[2])
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	persisted := reopened.RecruitingProject()
	if persisted.OrganizationName != organizationName ||
		persisted.Version != project.Version ||
		len(persisted.SourceGroups) != 0 ||
		len(reopened.Snapshot().AuditEvents) != len(state.AuditEvents) {
		t.Fatalf("persisted project/state = %#v / %#v", persisted, reopened.Snapshot())
	}
}

func TestRecruitingSourceGroupValidationConflictsAndDuplicates(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, groupID := range []string{
		"",
		"team",
		"@chatroom",
		"team name@chatroom",
		"team/@chatroom",
		"群聊@chatroom",
	} {
		t.Run(groupID, func(t *testing.T) {
			_, upsertErr := store.UpsertRecruitingSourceGroup(
				store.RecruitingProject().Version,
				RecruitingSourceGroupUpsert{GroupID: groupID, Enabled: true},
			)
			if !errors.Is(upsertErr, ErrCRMValidation) {
				t.Fatalf("UpsertRecruitingSourceGroup(%q) error = %v, want validation", groupID, upsertErr)
			}
		})
	}

	project, err := store.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
		GroupID: "jobs_a@chatroom",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertRecruitingSourceGroup(1, RecruitingSourceGroupUpsert{
		GroupID: "jobs_b@chatroom",
		Enabled: true,
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("stale create error = %v, want conflict", err)
	}
	_, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		GroupID: "jobs_a@chatroom",
		Enabled: true,
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("duplicate create error = %v, want conflict", err)
	}

	project, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		GroupID: "jobs_b@chatroom",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:      project.SourceGroups[0].ID,
		GroupID: "jobs_b@chatroom",
		Enabled: true,
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("duplicate update error = %v, want conflict", err)
	}
	_, err = store.RemoveRecruitingSourceGroup("missing@chatroom", project.Version)
	var notFound *recruitingSourceGroupNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("missing remove error = %v, want not found", err)
	}
}

func TestRecruitingProjectUpdateRejectsStaleAndEmptyUpdates(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, err = store.UpdateRecruitingProject(1, RecruitingProjectUpdate{})
	if !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("empty update error = %v, want validation", err)
	}
	name := "合成组织"
	project, err := store.UpdateRecruitingProject(1, RecruitingProjectUpdate{
		OrganizationName: &name,
	})
	if err != nil {
		t.Fatal(err)
	}
	other := "过期草稿"
	_, err = store.UpdateRecruitingProject(1, RecruitingProjectUpdate{
		OrganizationName: &other,
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("stale update error = %v, want conflict", err)
	}
	if got := store.RecruitingProject(); got.Version != project.Version || got.OrganizationName != name {
		t.Fatalf("project changed after stale update: %#v", got)
	}
}

func TestRecruitingProjectConcurrentUpdatesUseOneOptimisticVersion(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, value := range []string{"并发组织甲", "并发组织乙"} {
		name := value
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, updateErr := store.UpdateRecruitingProject(1, RecruitingProjectUpdate{
				OrganizationName: &name,
			})
			results <- updateErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for updateErr := range results {
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, ErrCRMConflict):
			conflicts++
		default:
			t.Fatalf("concurrent update error = %v", updateErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = success %d conflict %d", successes, conflicts)
	}
	project := store.RecruitingProject()
	if project.Version != 2 || len(store.Snapshot().AuditEvents) != 1 {
		t.Fatalf("project/audit after concurrent updates = %#v / %#v", project, store.Snapshot().AuditEvents)
	}
}
