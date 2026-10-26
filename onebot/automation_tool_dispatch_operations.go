package main

import (
	"encoding/json"
	"sort"
	"sync"
)

// workbenchOperationPlanCache memoises the expensive, static half of the
// per-tool operation registry.
//
// Deriving an operation's JSON schema reflects over its argument struct and
// re-encodes the query pagination wrapper; that work is pure and only depends
// on the tool name. Action/tool validation runs on every store update and on
// every provider turn, so rebuilding the registry each time dominated both.
//
// The *dynamic* half (availability/exposure decisions derived from runtime
// permission overrides) is intentionally not cached and is re-applied on every
// workbenchOperationSpecs call. Cached plans are immutable.
var workbenchOperationPlanCache sync.Map // AutomationToolName -> []workbenchOperationPlan

type workbenchOperationPlan struct {
	definition     WorkbenchOperationDefinition
	schema         json.RawMessage
	requiredFields []string
	catalog        WorkbenchOperationCatalogEntry
}

func workbenchOperationSpecs(tool AutomationToolName) []workbenchOperationSpec {
	plans := workbenchOperationPlans(tool)
	specs := make([]workbenchOperationSpec, len(plans))
	for i := range plans {
		catalog := plans[i].catalog
		if !workbenchOperationEffectiveAvailable(plans[i].definition) {
			catalog.Availability = WorkbenchAvailabilityUnavailable
			catalog.ValidDecisions = []AutomationPermissionDecision{}
		}
		specs[i] = workbenchOperationSpec{
			catalog:        catalog,
			requiredFields: plans[i].requiredFields,
			newArguments:   plans[i].definition.NewArguments,
		}
	}
	return specs
}

func workbenchOperationPlans(tool AutomationToolName) []workbenchOperationPlan {
	if cached, ok := workbenchOperationPlanCache.Load(tool); ok {
		return cached.([]workbenchOperationPlan)
	}
	plans := buildWorkbenchOperationPlans(tool)
	actual, _ := workbenchOperationPlanCache.LoadOrStore(tool, plans)
	return actual.([]workbenchOperationPlan)
}

func buildWorkbenchOperationPlans(tool AutomationToolName) []workbenchOperationPlan {
	definitions := workbenchOperationDefinitionsForTool(tool)
	plans := make([]workbenchOperationPlan, 0, len(definitions))
	for _, definition := range definitions {
		schema, err := workbenchArgumentsSchema(definition.NewArguments())
		if err != nil {
			panic(err)
		}
		requiredFields := append([]string(nil), definition.RequiredFields...)
		if tool == AutomationToolWorkbenchQuery {
			schema, requiredFields = workbenchOptionalQueryPagination(schema, requiredFields)
		}
		catalog, err := workbenchOperationCatalogEntry(definition)
		if err != nil {
			panic(err)
		}
		catalog.ArgumentsSchema = schema
		plans = append(plans, workbenchOperationPlan{
			definition:     definition,
			schema:         schema,
			requiredFields: requiredFields,
			catalog:        catalog,
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].catalog.Operation < plans[j].catalog.Operation
	})
	return plans
}

func lookupWorkbenchOperationSpec(
	tool AutomationToolName,
	operation WorkbenchOperation,
) (workbenchOperationSpec, bool) {
	specs := workbenchOperationSpecs(tool)
	index := sort.Search(len(specs), func(i int) bool {
		return specs[i].catalog.Operation >= operation
	})
	if index >= len(specs) || specs[index].catalog.Operation != operation {
		return workbenchOperationSpec{}, false
	}
	return specs[index], true
}

func workbenchNewArguments[T any]() any {
	return new(T)
}

func workbenchOperationCatalogEntry(
	definition WorkbenchOperationDefinition,
) (WorkbenchOperationCatalogEntry, error) {
	risk, sideEffect, decisions, _, _, ok := workbenchToolMetadata(definition.Tool)
	if !ok {
		return WorkbenchOperationCatalogEntry{}, NewAutomationValidationError(
			"tool",
			"unsupported workbench tool",
		)
	}
	schema, err := workbenchArgumentsSchema(definition.NewArguments())
	if err != nil {
		return WorkbenchOperationCatalogEntry{}, err
	}
	available := workbenchOperationEffectiveAvailable(definition)
	availability := WorkbenchAvailabilityUnavailable
	if available {
		availability = WorkbenchAvailabilityAvailable
	} else {
		decisions = []AutomationPermissionDecision{}
	}
	return WorkbenchOperationCatalogEntry{
		Operation:            definition.Operation,
		Label:                definition.Label,
		Description:          definition.Description,
		Category:             definition.Category,
		Risk:                 risk,
		SideEffect:           sideEffect,
		ImplementationStatus: definition.State,
		Exposure:             definition.Exposure,
		Availability:         availability,
		ValidDecisions:       append([]AutomationPermissionDecision(nil), decisions...),
		ArgumentsSchema:      schema,
		MaxResultBytes:       WorkbenchMaxResultJSONBytes,
	}, nil
}

func workbenchOptionalQueryPagination(
	schema json.RawMessage,
	requiredFields []string,
) (json.RawMessage, []string) {
	filteredFields := make([]string, 0, len(requiredFields))
	for _, field := range requiredFields {
		if field != "limit" && field != "offset" {
			filteredFields = append(filteredFields, field)
		}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(schema, &object); err != nil {
		panic(err)
	}
	var required []string
	if err := json.Unmarshal(object["required"], &required); err != nil {
		panic(err)
	}
	filteredRequired := make([]string, 0, len(required))
	for _, field := range required {
		if field != "limit" && field != "offset" {
			filteredRequired = append(filteredRequired, field)
		}
	}
	encodedRequired, err := json.Marshal(filteredRequired)
	if err != nil {
		panic(err)
	}
	object["required"] = encodedRequired
	updatedSchema, err := json.Marshal(object)
	if err != nil {
		panic(err)
	}
	return updatedSchema, filteredFields
}
