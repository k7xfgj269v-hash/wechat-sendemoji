package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestWorkbenchOperationRegistryBaselineAndDerivations(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	declarations, bindings := registryTestInputs()
	if len(declarations) != 98 {
		t.Fatalf("core declarations = %d, want 98", len(declarations))
	}
	coreStateCounts := map[WorkbenchImplementationState]int{}
	coreForbidden := 0
	for _, declaration := range declarations {
		definition := declaration.definition
		coreStateCounts[definition.State]++
		if definition.Handler != nil {
			t.Fatalf("core declaration unexpectedly carries handler: %s/%s", definition.Tool, definition.Operation)
		}
		if definition.Exposure == WorkbenchExposureForbidden {
			coreForbidden++
		}
	}
	if coreStateCounts[WorkbenchOperationWired] != 23 ||
		coreStateCounts[WorkbenchOperationBackingReady] != 75 ||
		coreStateCounts[WorkbenchOperationNewRequired] != 0 ||
		coreForbidden != 7 {
		t.Fatalf(
			"core state counts = %#v forbidden=%d, want wired=23 backing_ready=75 new_required=0 forbidden=7",
			coreStateCounts,
			coreForbidden,
		)
	}

	definitions := workbenchAllOperationDefinitions()
	if len(definitions) != 98 {
		t.Fatalf("definitions = %d, want 98", len(definitions))
	}

	stateCounts := map[WorkbenchImplementationState]int{}
	finalizedForbidden := 0
	operationSet := make(map[WorkbenchOperation]struct{}, len(definitions))
	identitySet := make(map[string]struct{}, len(definitions))
	permissionView := buildAutomationOperationPermissionsView(7, map[string]struct{}{})
	if permissionView.Version != 7 || len(permissionView.Operations) != 98 {
		t.Fatalf("permission view = version %d operations %d", permissionView.Version, len(permissionView.Operations))
	}
	permissionEntries := make(map[string]AutomationOperationPermissionEntry, len(permissionView.Operations))
	for _, entry := range permissionView.Operations {
		permissionEntries[workbenchOperationIdentity(entry.Tool, entry.Operation)] = entry
	}

	for _, definition := range definitions {
		if _, duplicate := operationSet[definition.Operation]; duplicate {
			t.Fatalf("duplicate operation %q", definition.Operation)
		}
		operationSet[definition.Operation] = struct{}{}
		identity := workbenchOperationIdentity(definition.Tool, definition.Operation)
		if _, duplicate := identitySet[identity]; duplicate {
			t.Fatalf("duplicate identity %q", identity)
		}
		identitySet[identity] = struct{}{}
		stateCounts[definition.State]++
		if definition.Exposure == WorkbenchExposureForbidden {
			finalizedForbidden++
		}

		if strings.TrimSpace(definition.Label) == "" ||
			strings.TrimSpace(definition.Description) == "" ||
			strings.TrimSpace(definition.Category) == "" ||
			definition.NewArguments == nil {
			t.Fatalf("incomplete definition for %s/%s: %+v", definition.Tool, definition.Operation, definition)
		}
		if !isKnownWorkbenchOperation(definition.Operation) {
			t.Fatalf("registry operation is not known to contract validation: %s", definition.Operation)
		}

		catalogEntry, ok := WorkbenchOperationCatalogEntryFor(definition.Tool, definition.Operation)
		if !ok {
			t.Fatalf("catalog missing registry operation %s/%s", definition.Tool, definition.Operation)
		}
		if catalogEntry.Label != definition.Label ||
			catalogEntry.Description != definition.Description ||
			catalogEntry.Category != definition.Category ||
			catalogEntry.ImplementationStatus != definition.State ||
			catalogEntry.Exposure != definition.Exposure {
			t.Fatalf("catalog drift for %s/%s: %+v vs %+v", definition.Tool, definition.Operation, catalogEntry, definition)
		}
		wantAvailability := WorkbenchAvailabilityUnavailable
		if workbenchOperationEffectiveAvailable(definition) {
			wantAvailability = WorkbenchAvailabilityAvailable
		}
		if catalogEntry.Availability != wantAvailability {
			t.Fatalf("%s/%s availability = %q, want %q", definition.Tool, definition.Operation, catalogEntry.Availability, wantAvailability)
		}
		_, _, toolDecisions, _, _, ok := workbenchToolMetadata(definition.Tool)
		if !ok {
			t.Fatalf("tool metadata missing for %q", definition.Tool)
		}
		if wantAvailability == WorkbenchAvailabilityUnavailable {
			toolDecisions = []AutomationPermissionDecision{}
		}
		if !slices.Equal(catalogEntry.ValidDecisions, toolDecisions) ||
			!slices.Equal(WorkbenchValidPermissionDecisions(definition.Tool, definition.Operation), toolDecisions) {
			t.Fatalf("%s/%s decisions = %#v, want %#v", definition.Tool, definition.Operation, catalogEntry.ValidDecisions, toolDecisions)
		}

		permissionEntry, ok := permissionEntries[identity]
		if !ok {
			t.Fatalf("permission view missing %s", identity)
		}
		if permissionEntry.Label != definition.Label ||
			permissionEntry.Description != definition.Description ||
			permissionEntry.ImplementationStatus != definition.State ||
			permissionEntry.Exposure != definition.Exposure ||
			permissionEntry.Implemented != workbenchOperationBaseAvailable(definition) ||
			permissionEntry.Denied {
			t.Fatalf("permission view drift for %s: %+v", identity, permissionEntry)
		}

		byIdentity, ok := WorkbenchOperationDefinitionFor(definition.Tool, definition.Operation)
		if !ok || byIdentity.Operation != definition.Operation || byIdentity.Tool != definition.Tool {
			t.Fatalf("identity lookup failed for %s", identity)
		}
		byOperation, ok := workbenchOperationDefinitionForOperation(definition.Operation)
		if !ok || byOperation.Tool != definition.Tool {
			t.Fatalf("operation lookup failed for %s", definition.Operation)
		}
	}

	if len(operationSet) != 98 || len(identitySet) != 98 {
		t.Fatalf("unique operations = %d identities = %d, want 98 each", len(operationSet), len(identitySet))
	}
	handlerBindings := 0
	for _, queued := range bindings {
		if queued.binding.Handler != nil {
			handlerBindings++
		}
	}
	if stateCounts[WorkbenchOperationWired] != handlerBindings ||
		stateCounts[WorkbenchOperationBackingReady] != 98-handlerBindings ||
		stateCounts[WorkbenchOperationNewRequired] != 0 ||
		finalizedForbidden != 7 {
		t.Fatalf(
			"finalized state counts = %#v handler_bindings=%d forbidden=%d",
			stateCounts,
			handlerBindings,
			finalizedForbidden,
		)
	}
	if handlerBindings < 23 || handlerBindings > 91 {
		t.Fatalf("handler bindings = %d, want incremental range 23..91", handlerBindings)
	}

	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	evaluator := NewServerAutomationRiskEvaluator()
	type policyTest struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
		risk      AutomationRisk
		decision  AutomationPolicyDecision
		reason    string
	}
	policyTests := []policyTest{
		{
			name: "query",
			tool: AutomationToolWorkbenchQuery, operation: WorkbenchOperationCatalogList,
			arguments: WorkbenchEmptyArguments{},
			risk:      AutomationRiskR0, decision: AutomationDecisionAutoApprove,
			reason: automationReasonR0WorkbenchQuery,
		},
		{
			name: "mutation",
			tool: AutomationToolWorkbenchMutate, operation: WorkbenchOperationCandidatesCreate,
			arguments: WorkbenchCandidateCreateArguments{
				Candidate: CandidateDraft{FullName: "Ada", Skills: []string{}},
			},
			risk: AutomationRiskR1, decision: AutomationDecisionAutoApprove,
			reason: automationReasonR1InternalWrite,
		},
		{
			name: "allow_once",
			tool: AutomationToolWorkbenchAllowOnce, operation: WorkbenchOperationLeadsTerminal,
			arguments: WorkbenchLeadTerminalArguments{
				LeadID: "lead_00000001", ExpectedVersion: 1,
				FromStage: LeadStageOffer, ToStage: LeadStageHired,
			},
			risk: AutomationRiskR3, decision: AutomationDecisionReview,
			reason: automationReasonR3WorkbenchAllowOnce,
		},
	}
	for _, definition := range definitions {
		if definition.State != WorkbenchOperationBackingReady ||
			definition.Exposure != WorkbenchExposureAllowed {
			continue
		}
		arguments := definition.NewArguments()
		input := registryWorkbenchPolicyInput(
			t,
			definition.Tool,
			definition.Operation,
			arguments,
		)
		if _, err := DecodeWorkbenchToolRequest(definition.Tool, input.Payload); err != nil {
			continue
		}
		policyTests = append(policyTests, policyTest{
			name:      "backing_ready",
			tool:      definition.Tool,
			operation: definition.Operation,
			arguments: arguments,
			risk:      AutomationRiskR4,
			decision:  AutomationDecisionDeny,
			reason:    automationReasonR4ForbiddenCapability,
		})
		break
	}
	if handlerBindings < 91 && len(policyTests) != 4 {
		t.Fatal("incremental registry has no allowed backing_ready operation")
	}
	for _, test := range policyTests {
		t.Run(test.name, func(t *testing.T) {
			result := evaluator.Evaluate(
				policy,
				registryWorkbenchPolicyInput(t, test.tool, test.operation, test.arguments),
			)
			assertAutomationPolicyResult(t, result, test.risk, test.decision, test.reason)
		})
	}
}

func TestWorkbenchOperationRegistryRejectsInvalidConstruction(t *testing.T) {
	handler := func(
		context.Context,
		*defaultWorkbenchToolDispatcher,
		WorkbenchExecutionContext,
		any,
	) WorkbenchToolResult {
		return WorkbenchToolResult{}
	}

	t.Run("duplicate declaration", func(t *testing.T) {
		declarations, bindings := registryTestInputs()
		duplicate := registryTestDeclarationIndex(t, declarations, WorkbenchOperationContactsGet)
		declarations[len(declarations)-1] = queuedWorkbenchOperationDefinition{
			module:     "duplicate-module",
			definition: cloneWorkbenchOperationDefinition(declarations[duplicate].definition),
		}
		assertRegistryPanicContains(t, "duplicate workbench operation", func() {
			buildWorkbenchOperationRegistry(declarations, bindings)
		})
	})

	t.Run("tool mismatch", func(t *testing.T) {
		declarations, bindings := registryTestInputs()
		duplicate := registryTestDeclarationIndex(t, declarations, WorkbenchOperationContactsGet)
		mismatched := cloneWorkbenchOperationDefinition(declarations[duplicate].definition)
		mismatched.Tool = AutomationToolWorkbenchMutate
		declarations[len(declarations)-1] = queuedWorkbenchOperationDefinition{
			module:     "mismatched-tool-module",
			definition: mismatched,
		}
		assertRegistryPanicContains(t, "tool mismatch", func() {
			buildWorkbenchOperationRegistry(declarations, bindings)
		})
	})

	t.Run("duplicate binding", func(t *testing.T) {
		declarations, bindings := registryTestInputs()
		bindings = append(bindings, queuedWorkbenchOperationBinding{
			module: "duplicate-binding",
			binding: WorkbenchOperationBinding{
				Operation: bindings[0].binding.Operation,
				Handler:   handler,
			},
		})
		assertRegistryPanicContains(t, "duplicate workbench operation binding", func() {
			buildWorkbenchOperationRegistry(declarations, bindings)
		})
	})

	t.Run("forbidden binding", func(t *testing.T) {
		declarations, bindings := registryTestInputs()
		bindings = append(bindings, queuedWorkbenchOperationBinding{
			module: "forbidden-binding",
			binding: WorkbenchOperationBinding{
				Operation: WorkbenchOperationSystemHealth,
				Handler:   handler,
			},
		})
		assertRegistryPanicContains(t, "cannot bind forbidden operation", func() {
			buildWorkbenchOperationRegistry(declarations, bindings)
		})
	})

	for _, test := range []struct {
		name          string
		operation     WorkbenchOperation
		removeBinding bool
		mutate        func(*WorkbenchOperationDefinition)
		want          string
	}{
		{
			name: "invalid implementation state", operation: WorkbenchOperationContactsGet,
			mutate: func(definition *WorkbenchOperationDefinition) {
				definition.State = WorkbenchImplementationState("invalid")
			},
			want: "invalid implementation state",
		},
		{
			name: "allowed operation marked forbidden", operation: WorkbenchOperationContactsGet,
			mutate: func(definition *WorkbenchOperationDefinition) {
				definition.Exposure = WorkbenchExposureForbidden
			},
			want: "must be allowed",
		},
		{
			name: "forbidden operation state", operation: WorkbenchOperationSystemHealth,
			mutate: func(definition *WorkbenchOperationDefinition) {
				definition.State = WorkbenchOperationWired
			},
			want: "invalid state, exposure, handler, or policy resolver",
		},
		{
			name: "wired operation without handler", operation: WorkbenchOperationContactsSearch,
			removeBinding: true,
			mutate: func(definition *WorkbenchOperationDefinition) {
				definition.State = WorkbenchOperationWired
				definition.Handler = nil
			},
			want: "has no handler",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			declarations, bindings := registryTestInputs()
			index := registryTestDeclarationIndex(t, declarations, test.operation)
			test.mutate(&declarations[index].definition)
			if test.removeBinding {
				bindings = registryTestBindingsWithout(bindings, test.operation)
			}
			assertRegistryPanicContains(t, test.want, func() {
				buildWorkbenchOperationRegistry(declarations, bindings)
			})
		})
	}
}

func TestWorkbenchOperationRegistryFinalStageContract(t *testing.T) {
	finalBindingSources := []string{
		"automation_workbench_registry_contacts.go",
		"automation_workbench_registry_pipeline.go",
		"automation_workbench_registry_activities.go",
		"automation_workbench_registry_service_requests.go",
		"automation_workbench_registry_workforce.go",
		"automation_workbench_registry_governance.go",
		"automation_workbench_registry_recruiting.go",
		"automation_workbench_registry_introspection.go",
	}
	for _, path := range finalBindingSources {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Skipf("incremental registry stage: %s is not integrated", path)
		} else if err != nil {
			t.Fatalf("inspect final binding source %s: %v", path, err)
		}
	}

	stateCounts := map[WorkbenchImplementationState]int{}
	forbidden := 0
	for _, definition := range workbenchAllOperationDefinitions() {
		stateCounts[definition.State]++
		if definition.Exposure == WorkbenchExposureForbidden {
			forbidden++
		}
	}
	if stateCounts[WorkbenchOperationWired] != 91 ||
		stateCounts[WorkbenchOperationBackingReady] != 7 ||
		stateCounts[WorkbenchOperationNewRequired] != 0 ||
		forbidden != 7 {
		t.Fatalf(
			"final registry state = %#v forbidden=%d, want wired=91 backing_ready=7 new_required=0 forbidden=7",
			stateCounts,
			forbidden,
		)
	}
}

func TestWorkbenchOperationRegistryConcurrentReadsAreImmutable(t *testing.T) {
	const (
		readers    = 16
		iterations = 20
	)

	baseline, ok := WorkbenchOperationDefinitionFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCandidatesList,
	)
	if !ok || len(baseline.RequiredFields) == 0 {
		t.Fatalf("baseline definition = %+v, found=%t", baseline, ok)
	}

	errs := make(chan error, readers)
	var wait sync.WaitGroup
	for reader := 0; reader < readers; reader++ {
		wait.Add(1)
		go func(reader int) {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				definition, ok := WorkbenchOperationDefinitionFor(
					AutomationToolWorkbenchQuery,
					WorkbenchOperationCandidatesList,
				)
				if !ok || definition.Label != baseline.Label ||
					!reflect.DeepEqual(definition.RequiredFields, baseline.RequiredFields) {
					errs <- fmt.Errorf("reader %d definition drift", reader)
					return
				}
				definition.Label = "mutated"
				definition.RequiredFields[0] = "mutated"

				definitions := workbenchAllOperationDefinitions()
				if len(definitions) != 98 {
					errs <- fmt.Errorf("reader %d definitions = %d", reader, len(definitions))
					return
				}
				definitions[0].Label = "mutated"

				catalog := WorkbenchToolCatalog()
				if len(catalog.Tools) != 3 {
					errs <- fmt.Errorf("reader %d catalog tools = %d", reader, len(catalog.Tools))
					return
				}
				view := buildAutomationOperationPermissionsView(0, map[string]struct{}{})
				if len(view.Operations) != 98 {
					errs <- fmt.Errorf("reader %d permission operations = %d", reader, len(view.Operations))
					return
				}
			}
		}(reader)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	after, ok := WorkbenchOperationDefinitionFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCandidatesList,
	)
	if !ok || after.Label != baseline.Label ||
		!reflect.DeepEqual(after.RequiredFields, baseline.RequiredFields) {
		t.Fatalf("registry mutation escaped defensive copies: before=%+v after=%+v", baseline, after)
	}
}

func TestWorkbenchOperationEffectiveAvailabilityOverride(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	tool := AutomationToolWorkbenchQuery
	operation := WorkbenchOperationCatalogList
	definition, ok := WorkbenchOperationDefinitionFor(tool, operation)
	if !ok || !workbenchOperationBaseAvailable(definition) ||
		!workbenchOperationEffectiveAvailable(definition) {
		t.Fatalf("baseline definition = %+v, found=%t", definition, ok)
	}
	decoded, err := DecodeWorkbenchToolRequest(
		tool,
		workbenchTestWrapper(t, operation, `{}`),
	)
	if err != nil {
		t.Fatal(err)
	}

	denied := map[string]struct{}{workbenchOperationDenyKey(tool, operation): {}}
	applyWorkbenchOperationDenySet(denied)
	if !workbenchOperationBaseAvailable(definition) ||
		workbenchOperationEffectiveAvailable(definition) {
		t.Fatalf("override changed base availability or failed to tighten effective availability")
	}
	entry, _ := WorkbenchOperationCatalogEntryFor(tool, operation)
	if entry.Availability != WorkbenchAvailabilityUnavailable || len(entry.ValidDecisions) != 0 {
		t.Fatalf("denied catalog entry = %+v", entry)
	}
	if result := NewDefaultWorkbenchToolDispatcher(nil).Dispatch(
		context.Background(),
		WorkbenchExecutionContext{},
		decoded,
	); result.OK || result.Error == nil || result.Error.Code != WorkbenchErrorUnavailable {
		t.Fatalf("denied dispatch result = %+v", result)
	}
	policyResult := NewServerAutomationRiskEvaluator().Evaluate(
		DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic),
		registryWorkbenchPolicyInput(t, tool, operation, WorkbenchEmptyArguments{}),
	)
	assertAutomationPolicyResult(
		t,
		policyResult,
		AutomationRiskR4,
		AutomationDecisionDeny,
		automationReasonR4ForbiddenCapability,
	)
	view := buildAutomationOperationPermissionsView(1, denied)
	permission := registryPermissionEntry(t, view, tool, operation)
	if !permission.Implemented || !permission.Denied {
		t.Fatalf("denied permission entry = %+v", permission)
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{})
	if !workbenchOperationEffectiveAvailable(definition) {
		t.Fatal("clearing override did not restore effective availability")
	}
	if result := NewDefaultWorkbenchToolDispatcher(nil).Dispatch(
		context.Background(),
		WorkbenchExecutionContext{},
		decoded,
	); !result.OK {
		t.Fatalf("restored dispatch result = %+v", result)
	}
}

func TestWorkbenchOperationRegistryPermanentlyForbidsSevenOperations(t *testing.T) {
	forbidden := map[WorkbenchOperation]AutomationToolName{
		WorkbenchOperationSystemHealth:    AutomationToolWorkbenchQuery,
		WorkbenchOperationAgentHealth:     AutomationToolWorkbenchQuery,
		WorkbenchOperationMetricsGet:      AutomationToolWorkbenchQuery,
		WorkbenchOperationFridaStatus:     AutomationToolWorkbenchQuery,
		WorkbenchOperationFridaInitialize: AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFridaRescan:     AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFridaDetach:     AutomationToolWorkbenchAllowOnce,
	}
	if !reflect.DeepEqual(permanentlyForbiddenWorkbenchOperations, forbidden) {
		t.Fatalf("forbidden operations = %#v, want %#v", permanentlyForbiddenWorkbenchOperations, forbidden)
	}

	denied := make(map[string]struct{}, len(forbidden))
	for operation, tool := range forbidden {
		denied[workbenchOperationDenyKey(tool, operation)] = struct{}{}
	}
	applyWorkbenchOperationDenySet(denied)
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})
	view := buildAutomationOperationPermissionsView(1, denied)
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil)
	policy := DefaultAutomationPolicy(automationPolicyTestTime, AutomationModeAutomatic)
	evaluator := NewServerAutomationRiskEvaluator()

	for operation, tool := range forbidden {
		definition, ok := WorkbenchOperationDefinitionFor(tool, operation)
		if !ok ||
			definition.State != WorkbenchOperationBackingReady ||
			definition.Exposure != WorkbenchExposureForbidden ||
			definition.Handler != nil ||
			definition.PolicyResolver != nil ||
			workbenchOperationBaseAvailable(definition) ||
			workbenchOperationEffectiveAvailable(definition) {
			t.Fatalf("forbidden definition %s/%s = %+v, found=%t", tool, operation, definition, ok)
		}
		entry, ok := WorkbenchOperationCatalogEntryFor(tool, operation)
		if !ok ||
			entry.ImplementationStatus != WorkbenchOperationBackingReady ||
			entry.Exposure != WorkbenchExposureForbidden ||
			entry.Availability != WorkbenchAvailabilityUnavailable ||
			len(entry.ValidDecisions) != 0 {
			t.Fatalf("forbidden catalog entry %s/%s = %+v, found=%t", tool, operation, entry, ok)
		}
		permission := registryPermissionEntry(t, view, tool, operation)
		if permission.Implemented || permission.Denied {
			t.Fatalf("forbidden permission entry %s/%s = %+v", tool, operation, permission)
		}
		decoded, err := DecodeWorkbenchToolRequest(
			tool,
			workbenchTestWrapper(t, operation, `{}`),
		)
		if err != nil {
			t.Fatalf("decode forbidden %s/%s: %v", tool, operation, err)
		}
		result := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
		if result.OK || result.Error == nil || result.Error.Code != WorkbenchErrorUnavailable {
			t.Fatalf("forbidden dispatch %s/%s = %+v", tool, operation, result)
		}
		policyResult := evaluator.Evaluate(
			policy,
			registryWorkbenchPolicyInput(t, tool, operation, WorkbenchEmptyArguments{}),
		)
		assertAutomationPolicyResult(
			t,
			policyResult,
			AutomationRiskR4,
			AutomationDecisionDeny,
			automationReasonR4ForbiddenCapability,
		)
	}
}

func TestDefaultWorkbenchDispatcherConstructorsRemainCompatible(t *testing.T) {
	crm := &CRMStore{}
	agent := &task003AgentClient{}

	legacy, ok := NewDefaultWorkbenchToolDispatcher(crm).(*defaultWorkbenchToolDispatcher)
	if !ok || legacy.crm != crm {
		t.Fatalf("legacy constructor = %#v, concrete=%t", legacy, ok)
	}
	legacyWithAgent, ok := NewDefaultWorkbenchToolDispatcher(crm, agent).(*defaultWorkbenchToolDispatcher)
	if !ok || legacyWithAgent.crm != crm || legacyWithAgent.agent != agent {
		t.Fatalf("legacy variadic constructor = %#v, concrete=%t", legacyWithAgent, ok)
	}

	automation := &AutomationStore{}
	audit := &AutomationAuditLedger{}
	audiences := &syntheticAutomationAudienceResolver{}
	sender := &agentAutomationTextSender{}
	forwardRules := NewForwardRuleStore(crm)
	injected, ok := NewDefaultWorkbenchToolDispatcherWithDependencies(
		WorkbenchToolDependencies{
			CRM:          crm,
			Agent:        agent,
			Automation:   automation,
			Audit:        audit,
			Audiences:    audiences,
			Sender:       sender,
			ForwardRules: forwardRules,
		},
	).(*defaultWorkbenchToolDispatcher)
	if !ok {
		t.Fatalf("dependency constructor returned %T", injected)
	}
	if injected.crm != crm ||
		injected.agent != agent ||
		injected.automation != automation ||
		injected.audit != audit ||
		injected.audiences != audiences ||
		injected.sender != sender ||
		injected.forwardRules != forwardRules {
		t.Fatalf("dependency injection drift: %+v", injected)
	}
}

func registryTestInputs() (
	[]queuedWorkbenchOperationDefinition,
	[]queuedWorkbenchOperationBinding,
) {
	workbenchOperationRegistry.mu.Lock()
	defer workbenchOperationRegistry.mu.Unlock()

	declarations := make(
		[]queuedWorkbenchOperationDefinition,
		len(workbenchOperationRegistry.declarations),
	)
	for i, declaration := range workbenchOperationRegistry.declarations {
		declarations[i] = queuedWorkbenchOperationDefinition{
			module:     declaration.module,
			definition: cloneWorkbenchOperationDefinition(declaration.definition),
		}
	}
	bindings := append(
		[]queuedWorkbenchOperationBinding(nil),
		workbenchOperationRegistry.bindings...,
	)
	return declarations, bindings
}

func registryTestBindingsWithout(
	bindings []queuedWorkbenchOperationBinding,
	operation WorkbenchOperation,
) []queuedWorkbenchOperationBinding {
	filtered := make([]queuedWorkbenchOperationBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.binding.Operation != operation {
			filtered = append(filtered, binding)
		}
	}
	return filtered
}

func registryTestDeclarationIndex(
	t *testing.T,
	declarations []queuedWorkbenchOperationDefinition,
	operation WorkbenchOperation,
) int {
	t.Helper()
	for i := range declarations {
		if declarations[i].definition.Operation == operation {
			return i
		}
	}
	t.Fatalf("operation %q missing from test declarations", operation)
	return -1
}

func assertRegistryPanicContains(t *testing.T, want string, call func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		if message := fmt.Sprint(recovered); !strings.Contains(message, want) {
			t.Fatalf("panic = %q, want substring %q", message, want)
		}
	}()
	call()
}

func registryWorkbenchPolicyInput(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) AutomationPolicyInput {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	return AutomationPolicyInput{
		Tool:           tool,
		Confidence:     1,
		Payload:        payload,
		SourceMessage:  privateAutomationSource(false),
		RunActionCount: 1,
	}
}

func registryPermissionEntry(
	t *testing.T,
	view AutomationOperationPermissionsView,
	tool AutomationToolName,
	operation WorkbenchOperation,
) AutomationOperationPermissionEntry {
	t.Helper()
	for _, entry := range view.Operations {
		if entry.Tool == tool && entry.Operation == operation {
			return entry
		}
	}
	t.Fatalf("permission entry missing for %s/%s", tool, operation)
	return AutomationOperationPermissionEntry{}
}
