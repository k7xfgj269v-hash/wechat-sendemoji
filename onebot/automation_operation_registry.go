package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const workbenchOperationDefinitionCount = 98

type WorkbenchOperationHandler func(
	context.Context,
	*defaultWorkbenchToolDispatcher,
	WorkbenchExecutionContext,
	any,
) WorkbenchToolResult

type WorkbenchPolicyResolver func(
	*ServerAutomationRiskEvaluator,
	AutomationPolicyInput,
	WorkbenchDecodedToolRequest,
) (*AutomationRecipient, error)

type WorkbenchOperationDefinition struct {
	Tool           AutomationToolName
	Operation      WorkbenchOperation
	Label          string
	Description    string
	Category       string
	NewArguments   func() any
	RequiredFields []string
	State          WorkbenchImplementationState
	Exposure       WorkbenchExposure
	Handler        WorkbenchOperationHandler
	PolicyResolver WorkbenchPolicyResolver
}

type WorkbenchOperationBinding struct {
	Operation      WorkbenchOperation
	Handler        WorkbenchOperationHandler
	PolicyResolver WorkbenchPolicyResolver
}

type queuedWorkbenchOperationDefinition struct {
	module     string
	definition WorkbenchOperationDefinition
}

type queuedWorkbenchOperationBinding struct {
	module  string
	binding WorkbenchOperationBinding
}

type workbenchOperationRegistryState struct {
	mu         sync.Mutex
	once       sync.Once
	finalizing bool
	finalized  bool

	declarations []queuedWorkbenchOperationDefinition
	bindings     []queuedWorkbenchOperationBinding

	byIdentity  map[string]WorkbenchOperationDefinition
	byOperation map[WorkbenchOperation]WorkbenchOperationDefinition
	byTool      map[AutomationToolName][]WorkbenchOperationDefinition
}

var workbenchOperationRegistry workbenchOperationRegistryState

var permanentlyForbiddenWorkbenchOperations = map[WorkbenchOperation]AutomationToolName{
	WorkbenchOperationSystemHealth:    AutomationToolWorkbenchQuery,
	WorkbenchOperationAgentHealth:     AutomationToolWorkbenchQuery,
	WorkbenchOperationMetricsGet:      AutomationToolWorkbenchQuery,
	WorkbenchOperationFridaStatus:     AutomationToolWorkbenchQuery,
	WorkbenchOperationFridaInitialize: AutomationToolWorkbenchAllowOnce,
	WorkbenchOperationFridaRescan:     AutomationToolWorkbenchAllowOnce,
	WorkbenchOperationFridaDetach:     AutomationToolWorkbenchAllowOnce,
}

func declareWorkbenchOperations(module string, definitions ...WorkbenchOperationDefinition) {
	module = strings.TrimSpace(module)
	if module == "" {
		panic("workbench operation declaration module is required")
	}
	if len(definitions) == 0 {
		panic(fmt.Sprintf("workbench operation declaration module %q is empty", module))
	}

	workbenchOperationRegistry.mu.Lock()
	defer workbenchOperationRegistry.mu.Unlock()
	if workbenchOperationRegistry.finalizing || workbenchOperationRegistry.finalized {
		panic(fmt.Sprintf("workbench operation declarations are finalized; module %q registered too late", module))
	}
	for _, definition := range definitions {
		workbenchOperationRegistry.declarations = append(
			workbenchOperationRegistry.declarations,
			queuedWorkbenchOperationDefinition{
				module:     module,
				definition: cloneWorkbenchOperationDefinition(definition),
			},
		)
	}
}

func bindWorkbenchOperationHandlers(module string, bindings ...WorkbenchOperationBinding) {
	module = strings.TrimSpace(module)
	if module == "" {
		panic("workbench operation binding module is required")
	}
	if len(bindings) == 0 {
		panic(fmt.Sprintf("workbench operation binding module %q is empty", module))
	}

	workbenchOperationRegistry.mu.Lock()
	defer workbenchOperationRegistry.mu.Unlock()
	if workbenchOperationRegistry.finalizing || workbenchOperationRegistry.finalized {
		panic(fmt.Sprintf("workbench operation bindings are finalized; module %q registered too late", module))
	}
	for _, binding := range bindings {
		workbenchOperationRegistry.bindings = append(
			workbenchOperationRegistry.bindings,
			queuedWorkbenchOperationBinding{module: module, binding: binding},
		)
	}
}

func WorkbenchOperationDefinitionFor(
	tool AutomationToolName,
	operation WorkbenchOperation,
) (WorkbenchOperationDefinition, bool) {
	finalizeWorkbenchOperationRegistry()
	definition, ok := workbenchOperationRegistry.byIdentity[workbenchOperationIdentity(tool, operation)]
	if !ok {
		return WorkbenchOperationDefinition{}, false
	}
	return cloneWorkbenchOperationDefinition(definition), true
}

func workbenchOperationDefinitionForOperation(
	operation WorkbenchOperation,
) (WorkbenchOperationDefinition, bool) {
	finalizeWorkbenchOperationRegistry()
	definition, ok := workbenchOperationRegistry.byOperation[operation]
	if !ok {
		return WorkbenchOperationDefinition{}, false
	}
	return cloneWorkbenchOperationDefinition(definition), true
}

func workbenchOperationDefinitionsForTool(
	tool AutomationToolName,
) []WorkbenchOperationDefinition {
	finalizeWorkbenchOperationRegistry()
	definitions := workbenchOperationRegistry.byTool[tool]
	cloned := make([]WorkbenchOperationDefinition, len(definitions))
	for i := range definitions {
		cloned[i] = cloneWorkbenchOperationDefinition(definitions[i])
	}
	return cloned
}

func workbenchAllOperationDefinitions() []WorkbenchOperationDefinition {
	finalizeWorkbenchOperationRegistry()
	definitions := make([]WorkbenchOperationDefinition, 0, workbenchOperationDefinitionCount)
	for _, tool := range WorkbenchProviderTools() {
		definitions = append(definitions, workbenchOperationDefinitionsForTool(tool)...)
	}
	return definitions
}

func workbenchOperationBaseAvailable(definition WorkbenchOperationDefinition) bool {
	return definition.Exposure == WorkbenchExposureAllowed &&
		definition.State == WorkbenchOperationWired &&
		definition.Handler != nil
}

func workbenchOperationEffectiveAvailable(definition WorkbenchOperationDefinition) bool {
	return workbenchOperationBaseAvailable(definition) &&
		!workbenchOperationOverrideDenied(definition.Tool, definition.Operation)
}

func isPermanentlyForbiddenWorkbenchOperation(operation WorkbenchOperation) bool {
	_, forbidden := permanentlyForbiddenWorkbenchOperations[operation]
	return forbidden
}

func finalizeWorkbenchOperationRegistry() {
	workbenchOperationRegistry.once.Do(func() {
		workbenchOperationRegistry.mu.Lock()
		workbenchOperationRegistry.finalizing = true
		declarations := append(
			[]queuedWorkbenchOperationDefinition(nil),
			workbenchOperationRegistry.declarations...,
		)
		bindings := append(
			[]queuedWorkbenchOperationBinding(nil),
			workbenchOperationRegistry.bindings...,
		)
		workbenchOperationRegistry.mu.Unlock()

		byIdentity, byOperation, byTool := buildWorkbenchOperationRegistry(declarations, bindings)

		workbenchOperationRegistry.mu.Lock()
		workbenchOperationRegistry.byIdentity = byIdentity
		workbenchOperationRegistry.byOperation = byOperation
		workbenchOperationRegistry.byTool = byTool
		workbenchOperationRegistry.finalized = true
		workbenchOperationRegistry.finalizing = false
		workbenchOperationRegistry.mu.Unlock()
	})
	if !workbenchOperationRegistry.finalized {
		panic("workbench operation registry finalization failed")
	}
}

func buildWorkbenchOperationRegistry(
	declarations []queuedWorkbenchOperationDefinition,
	bindings []queuedWorkbenchOperationBinding,
) (
	map[string]WorkbenchOperationDefinition,
	map[WorkbenchOperation]WorkbenchOperationDefinition,
	map[AutomationToolName][]WorkbenchOperationDefinition,
) {
	if len(declarations) != workbenchOperationDefinitionCount {
		panic(fmt.Sprintf(
			"workbench operation registry contains %d definitions, want %d",
			len(declarations),
			workbenchOperationDefinitionCount,
		))
	}

	definitions := make(map[WorkbenchOperation]WorkbenchOperationDefinition, len(declarations))
	definitionModules := make(map[WorkbenchOperation]string, len(declarations))
	for _, declaration := range declarations {
		definition := cloneWorkbenchOperationDefinition(declaration.definition)
		validateWorkbenchOperationDefinition(declaration.module, definition)
		if previous, duplicate := definitions[definition.Operation]; duplicate {
			previousModule := definitionModules[definition.Operation]
			if previous.Tool != definition.Tool {
				panic(fmt.Sprintf(
					"workbench operation %q tool mismatch between modules %q and %q",
					definition.Operation,
					previousModule,
					declaration.module,
				))
			}
			panic(fmt.Sprintf(
				"duplicate workbench operation %q in modules %q and %q",
				definition.Operation,
				previousModule,
				declaration.module,
			))
		}
		definitions[definition.Operation] = definition
		definitionModules[definition.Operation] = declaration.module
	}

	seenBindings := make(map[WorkbenchOperation]string, len(bindings))
	for _, queued := range bindings {
		binding := queued.binding
		if binding.Operation == "" {
			panic(fmt.Sprintf("workbench operation binding module %q has an empty operation", queued.module))
		}
		if binding.Handler == nil && binding.PolicyResolver == nil {
			panic(fmt.Sprintf(
				"workbench operation binding module %q has no handler or policy resolver for %q",
				queued.module,
				binding.Operation,
			))
		}
		if previousModule, duplicate := seenBindings[binding.Operation]; duplicate {
			panic(fmt.Sprintf(
				"duplicate workbench operation binding %q in modules %q and %q",
				binding.Operation,
				previousModule,
				queued.module,
			))
		}
		seenBindings[binding.Operation] = queued.module

		definition, ok := definitions[binding.Operation]
		if !ok {
			panic(fmt.Sprintf(
				"workbench operation binding module %q references unknown operation %q",
				queued.module,
				binding.Operation,
			))
		}
		if definition.Exposure == WorkbenchExposureForbidden {
			panic(fmt.Sprintf(
				"workbench operation binding module %q cannot bind forbidden operation %q",
				queued.module,
				binding.Operation,
			))
		}
		if binding.Handler != nil {
			if definition.Handler != nil {
				panic(fmt.Sprintf(
					"workbench operation %q has both a declared and bound handler",
					binding.Operation,
				))
			}
			definition.Handler = binding.Handler
			definition.State = WorkbenchOperationWired
		}
		if binding.PolicyResolver != nil {
			definition.PolicyResolver = binding.PolicyResolver
		}
		definitions[binding.Operation] = definition
	}

	byIdentity := make(map[string]WorkbenchOperationDefinition, len(definitions))
	byOperation := make(map[WorkbenchOperation]WorkbenchOperationDefinition, len(definitions))
	byTool := make(map[AutomationToolName][]WorkbenchOperationDefinition, len(WorkbenchProviderTools()))
	for operation, definition := range definitions {
		validateFinalWorkbenchOperationDefinition(definitionModules[operation], definition)
		cloned := cloneWorkbenchOperationDefinition(definition)
		byIdentity[workbenchOperationIdentity(definition.Tool, operation)] = cloned
		byOperation[operation] = cloned
		byTool[definition.Tool] = append(byTool[definition.Tool], cloned)
	}
	for _, tool := range WorkbenchProviderTools() {
		sort.Slice(byTool[tool], func(i, j int) bool {
			return byTool[tool][i].Operation < byTool[tool][j].Operation
		})
	}
	return byIdentity, byOperation, byTool
}

func validateWorkbenchOperationDefinition(
	module string,
	definition WorkbenchOperationDefinition,
) {
	if !IsWorkbenchToolName(definition.Tool) {
		panic(fmt.Sprintf("workbench operation module %q has invalid tool %q", module, definition.Tool))
	}
	if definition.Operation == "" {
		panic(fmt.Sprintf("workbench operation module %q has an empty operation", module))
	}
	if strings.TrimSpace(definition.Label) == "" ||
		strings.TrimSpace(definition.Description) == "" ||
		strings.TrimSpace(definition.Category) == "" {
		panic(fmt.Sprintf("workbench operation %q has incomplete catalog metadata", definition.Operation))
	}
	if definition.NewArguments == nil {
		panic(fmt.Sprintf("workbench operation %q has no argument constructor", definition.Operation))
	}
	if !isValidWorkbenchImplementationState(definition.State) {
		panic(fmt.Sprintf(
			"workbench operation %q has invalid implementation state %q",
			definition.Operation,
			definition.State,
		))
	}
	if !isValidWorkbenchExposure(definition.Exposure) {
		panic(fmt.Sprintf(
			"workbench operation %q has invalid exposure %q",
			definition.Operation,
			definition.Exposure,
		))
	}
	seenRequired := make(map[string]struct{}, len(definition.RequiredFields))
	for _, field := range definition.RequiredFields {
		field = strings.TrimSpace(field)
		if field == "" {
			panic(fmt.Sprintf("workbench operation %q has an empty required field", definition.Operation))
		}
		if _, duplicate := seenRequired[field]; duplicate {
			panic(fmt.Sprintf(
				"workbench operation %q has duplicate required field %q",
				definition.Operation,
				field,
			))
		}
		seenRequired[field] = struct{}{}
	}

	expectedTool, permanentlyForbidden := permanentlyForbiddenWorkbenchOperations[definition.Operation]
	if permanentlyForbidden {
		if definition.Tool != expectedTool {
			panic(fmt.Sprintf(
				"forbidden workbench operation %q has tool %q, want %q",
				definition.Operation,
				definition.Tool,
				expectedTool,
			))
		}
		if definition.Exposure != WorkbenchExposureForbidden ||
			definition.State != WorkbenchOperationBackingReady ||
			definition.Handler != nil ||
			definition.PolicyResolver != nil {
			panic(fmt.Sprintf(
				"forbidden workbench operation %q has an invalid state, exposure, handler, or policy resolver",
				definition.Operation,
			))
		}
		return
	}
	if definition.Exposure != WorkbenchExposureAllowed {
		panic(fmt.Sprintf(
			"workbench operation %q is not permanently forbidden and must be allowed",
			definition.Operation,
		))
	}
}

func validateFinalWorkbenchOperationDefinition(
	module string,
	definition WorkbenchOperationDefinition,
) {
	if definition.Exposure == WorkbenchExposureForbidden {
		if definition.Handler != nil || definition.PolicyResolver != nil {
			panic(fmt.Sprintf("forbidden workbench operation %q became bindable", definition.Operation))
		}
		return
	}
	if definition.State == WorkbenchOperationWired && definition.Handler == nil {
		panic(fmt.Sprintf(
			"wired workbench operation %q from module %q has no handler",
			definition.Operation,
			module,
		))
	}
	if definition.State == WorkbenchOperationWired && definition.PolicyResolver == nil {
		panic(fmt.Sprintf(
			"wired workbench operation %q from module %q has no policy resolver",
			definition.Operation,
			module,
		))
	}
	if definition.State != WorkbenchOperationWired && definition.Handler != nil {
		panic(fmt.Sprintf(
			"non-wired workbench operation %q from module %q has a handler",
			definition.Operation,
			module,
		))
	}
}

func workbenchOperationIdentity(tool AutomationToolName, operation WorkbenchOperation) string {
	return string(tool) + "|" + string(operation)
}

func cloneWorkbenchOperationDefinition(
	definition WorkbenchOperationDefinition,
) WorkbenchOperationDefinition {
	definition.RequiredFields = append([]string(nil), definition.RequiredFields...)
	return definition
}

func isValidWorkbenchImplementationState(state WorkbenchImplementationState) bool {
	switch state {
	case WorkbenchOperationWired,
		WorkbenchOperationBackingReady,
		WorkbenchOperationNewRequired:
		return true
	default:
		return false
	}
}

func isValidWorkbenchExposure(exposure WorkbenchExposure) bool {
	return exposure == WorkbenchExposureAllowed ||
		exposure == WorkbenchExposureForbidden
}
