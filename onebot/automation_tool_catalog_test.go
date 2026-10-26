package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestWorkbenchToolCatalogIsDeterministicAndClosed(t *testing.T) {
	first := WorkbenchToolCatalog()
	second := WorkbenchToolCatalog()
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first catalog: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second catalog: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("catalog is not deterministic")
	}
	if first.Version != WorkbenchToolCatalogVersion {
		t.Fatalf("catalog version = %q", first.Version)
	}
	wantTools := []AutomationToolName{
		AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
		AutomationToolWorkbenchAllowOnce,
	}
	if len(first.Tools) != len(wantTools) {
		t.Fatalf("catalog tools = %d, want %d", len(first.Tools), len(wantTools))
	}
	for i, want := range wantTools {
		tool := first.Tools[i]
		if tool.Name != want {
			t.Fatalf("tool[%d] = %q, want %q", i, tool.Name, want)
		}
		if tool.Description == "" ||
			!IsValidAutomationRisk(tool.Risk) ||
			!IsValidWorkbenchSideEffect(tool.SideEffect) ||
			tool.Availability != WorkbenchAvailabilityAvailable ||
			tool.MaxArgumentBytes != WorkbenchMaxArgumentsJSONBytes ||
			tool.MaxResultBytes != WorkbenchMaxResultJSONBytes ||
			len(tool.Operations) == 0 {
			t.Fatalf("incomplete tool metadata: %+v", tool)
		}
		previous := WorkbenchOperation("")
		for _, operation := range tool.Operations {
			if operation.Operation <= previous {
				t.Fatalf("%s operations are not strictly sorted: %q after %q", tool.Name, operation.Operation, previous)
			}
			previous = operation.Operation
			if operation.Risk != tool.Risk ||
				operation.SideEffect != tool.SideEffect ||
				(operation.Availability != WorkbenchAvailabilityAvailable &&
					operation.Availability != WorkbenchAvailabilityUnavailable) ||
				operation.MaxResultBytes != WorkbenchMaxResultJSONBytes ||
				len(operation.ArgumentsSchema) == 0 {
				t.Fatalf("operation metadata drift for %s/%s: %+v", tool.Name, operation.Operation, operation)
			}
			if operation.Availability == WorkbenchAvailabilityUnavailable &&
				len(operation.ValidDecisions) != 0 {
				t.Fatalf("unavailable operation has permission decisions: %+v", operation)
			}
			assertWorkbenchSchemaClosed(t, operation.ArgumentsSchema)
			assertWorkbenchSchemaHasNoTrustedFields(t, operation.ArgumentsSchema)
		}
	}

	query, _ := WorkbenchToolCatalogEntryFor(AutomationToolWorkbenchQuery)
	mutate, _ := WorkbenchToolCatalogEntryFor(AutomationToolWorkbenchMutate)
	allowOnce, _ := WorkbenchToolCatalogEntryFor(AutomationToolWorkbenchAllowOnce)
	if query.Risk != AutomationRiskR0 || query.SideEffect != WorkbenchSideEffectReadOnly ||
		len(query.ValidDecisions) != 0 {
		t.Fatalf("query metadata = %+v", query)
	}
	if mutate.Risk != AutomationRiskR1 || mutate.SideEffect != WorkbenchSideEffectReversible ||
		!containsWorkbenchDecision(mutate.ValidDecisions, AutomationPermissionAllowPersistent) {
		t.Fatalf("mutate metadata = %+v", mutate)
	}
	if allowOnce.Risk != AutomationRiskR3 || allowOnce.SideEffect != WorkbenchSideEffectHighImpact ||
		fmt.Sprint(allowOnce.ValidDecisions) != fmt.Sprint([]AutomationPermissionDecision{
			AutomationPermissionAllowOnce,
			AutomationPermissionDenyOnce,
		}) {
		t.Fatalf("allow_once decisions = %#v", allowOnce.ValidDecisions)
	}

	if _, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCatalogList,
	); !ok {
		t.Fatal("catalog.list is missing")
	}
	if _, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRecruitingSourcesList,
	); !ok {
		t.Fatal("recruiting_sources.list is missing")
	}
	if _, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationJobsUpdateCompensation,
	); !ok {
		t.Fatal("jobs.update_compensation is missing from workbench.allow_once")
	}
	if IsValidWorkbenchOperation(
		AutomationToolWorkbenchMutate,
		WorkbenchOperationJobsUpdateCompensation,
	) {
		t.Fatal("jobs.update_compensation leaked into workbench.mutate")
	}
}

func TestWorkbenchCatalogListPaginationArguments(t *testing.T) {
	entry, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCatalogList,
	)
	if !ok {
		t.Fatal("catalog.list is missing")
	}
	var schema map[string]any
	if err := json.Unmarshal(entry.ArgumentsSchema, &schema); err != nil {
		t.Fatalf("decode catalog.list schema: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	if len(properties) != 2 || properties["limit"] == nil || properties["offset"] == nil {
		t.Fatalf("catalog.list properties = %#v", properties)
	}
	if required := interfaceStrings(schema["required"]); len(required) != 0 {
		t.Fatalf("catalog.list required fields = %#v", required)
	}

	valid := []struct {
		arguments string
		limit     int
		offset    int
	}{
		{arguments: `{}`, limit: WorkbenchDefaultPageLimit},
		{arguments: `{"limit":0}`, limit: WorkbenchDefaultPageLimit},
		{arguments: `{"limit":1,"offset":0}`, limit: 1},
		{arguments: `{"limit":100,"offset":7}`, limit: WorkbenchMaxPageLimit, offset: 7},
	}
	for _, test := range valid {
		decoded, err := DecodeWorkbenchToolRequest(
			AutomationToolWorkbenchQuery,
			workbenchTestWrapper(t, WorkbenchOperationCatalogList, test.arguments),
		)
		if err != nil {
			t.Fatalf("catalog.list arguments %s rejected: %v", test.arguments, err)
		}
		arguments, ok := decoded.Arguments.(*WorkbenchPageArguments)
		if !ok {
			t.Fatalf("catalog.list arguments type = %T", decoded.Arguments)
		}
		if arguments.Limit != test.limit || arguments.Offset != test.offset {
			t.Fatalf("catalog.list arguments %s = %+v", test.arguments, arguments)
		}
		wantCanonical := fmt.Sprintf(`{"limit":%d,"offset":%d}`, test.limit, test.offset)
		if string(decoded.CanonicalArguments) != wantCanonical {
			t.Fatalf(
				"catalog.list canonical arguments = %s, want %s",
				decoded.CanonicalArguments,
				wantCanonical,
			)
		}
	}
	for _, arguments := range []string{
		`{"limit":-1,"offset":0}`,
		`{"limit":101,"offset":0}`,
		`{"limit":1,"offset":-1}`,
	} {
		if _, err := DecodeWorkbenchToolRequest(
			AutomationToolWorkbenchQuery,
			workbenchTestWrapper(t, WorkbenchOperationCatalogList, arguments),
		); err == nil {
			t.Fatalf("catalog.list arguments %s were accepted", arguments)
		}
	}
}

func TestWorkbenchCatalogListPagesFinalNinetyOneWithinEnvelope(t *testing.T) {
	catalog := workbenchFinalAvailableCatalog(t)
	total := workbenchAvailableCatalogOperationCount(catalog)
	if total != 91 {
		t.Fatalf("synthetic final catalog operations = %d, want 91", total)
	}
	if WorkbenchMaxResultJSONBytes != 32*1024 {
		t.Fatalf("workbench result limit = %d, want 32768", WorkbenchMaxResultJSONBytes)
	}
	var fundsLedger WorkbenchOperationCatalogEntry
	for _, tool := range catalog.Tools {
		for _, operation := range tool.Operations {
			if operation.Operation == WorkbenchOperationFundsLedger {
				fundsLedger = operation
			}
		}
	}
	var fundsLedgerSchema map[string]any
	if err := json.Unmarshal(fundsLedger.ArgumentsSchema, &fundsLedgerSchema); err != nil {
		t.Fatalf("decode final funds.ledger schema: %v", err)
	}
	fundsLedgerProperties := fundsLedgerSchema["properties"].(map[string]any)
	if len(fundsLedgerProperties) != 3 ||
		fundsLedgerProperties["id"] == nil ||
		fundsLedgerProperties["limit"] == nil ||
		fundsLedgerProperties["offset"] == nil ||
		fmt.Sprint(interfaceStrings(fundsLedgerSchema["required"])) != fmt.Sprint([]string{"id"}) {
		t.Fatalf("final funds.ledger schema = %#v", fundsLedgerSchema)
	}

	byteConstrained := false
	for offset := 0; offset <= total+1; offset++ {
		arguments := WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: offset}
		result := workbenchCatalogListResult(catalog, arguments)
		repeated := workbenchCatalogListResult(catalog, arguments)
		if !result.OK {
			t.Fatalf("synthetic catalog offset %d result = %+v", offset, result)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode synthetic catalog offset %d: %v", offset, err)
		}
		repeatedEncoded, err := json.Marshal(repeated)
		if err != nil {
			t.Fatalf("encode repeated synthetic catalog offset %d: %v", offset, err)
		}
		if !bytes.Equal(encoded, repeatedEncoded) {
			t.Fatalf("synthetic catalog offset %d is not deterministic", offset)
		}
		if len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf(
				"synthetic catalog offset %d bytes = %d, max %d",
				offset,
				len(encoded),
				WorkbenchMaxResultJSONBytes,
			)
		}
		if err := ValidateWorkbenchToolResult(result); err != nil {
			t.Fatalf("synthetic catalog offset %d validation: %v", offset, err)
		}
		pageCatalog := workbenchDecodeCatalogPage(t, result.Data)
		returned := workbenchAvailableCatalogOperationCount(pageCatalog)
		if result.Page == nil ||
			result.Page.Limit != WorkbenchMaxPageLimit ||
			result.Page.Offset != offset ||
			result.Page.Returned != returned ||
			result.Page.Total != total ||
			result.Page.HasMore != (offset+returned < total) {
			t.Fatalf("synthetic catalog offset %d page = %+v", offset, result.Page)
		}
		if offset < total && returned == 0 {
			t.Fatalf("synthetic catalog offset %d made no progress", offset)
		}
		if result.Page.HasMore {
			if result.Page.NextOffset != offset+returned {
				t.Fatalf("synthetic catalog offset %d next = %d", offset, result.Page.NextOffset)
			}
		} else if result.Page.NextOffset != 0 {
			t.Fatalf("synthetic terminal catalog offset %d next = %d", offset, result.Page.NextOffset)
		}

		remaining := total - offset
		if remaining > arguments.Limit {
			remaining = arguments.Limit
		}
		if remaining > 0 && returned < remaining {
			byteConstrained = true
			oneMore := returned + 1
			oversized := NewWorkbenchToolSuccess(
				WorkbenchOperationCatalogList,
				workbenchAvailableCatalogSlice(catalog, offset, oneMore),
				newWorkbenchPage(arguments.Limit, offset, total, oneMore),
			)
			if oversized.OK {
				t.Fatalf(
					"synthetic catalog offset %d returned %d but %d also fit",
					offset,
					returned,
					oneMore,
				)
			}
		}
	}
	if !byteConstrained {
		t.Fatal("synthetic final catalog did not exercise byte-constrained pagination")
	}

	single := workbenchCatalogListResult(
		catalog,
		WorkbenchPageArguments{Limit: 1, Offset: 0},
	)
	if !single.OK || single.Page == nil ||
		single.Page.Returned != 1 ||
		!single.Page.HasMore ||
		single.Page.NextOffset != 1 {
		t.Fatalf("limit-one catalog result = %+v", single)
	}
	for _, offset := range []int{total, total + 7} {
		terminal := workbenchCatalogListResult(
			catalog,
			WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: offset},
		)
		if !terminal.OK || terminal.Page == nil ||
			terminal.Page.Returned != 0 ||
			terminal.Page.Total != total ||
			terminal.Page.HasMore ||
			terminal.Page.NextOffset != 0 {
			t.Fatalf("terminal catalog offset %d result = %+v", offset, terminal)
		}
	}

	assembled := workbenchAvailableCatalogSlice(catalog, 0, 0)
	offset := 0
	for {
		result := workbenchCatalogListResult(
			catalog,
			WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: offset},
		)
		pageCatalog := workbenchDecodeCatalogPage(t, result.Data)
		for index := range assembled.Tools {
			assembled.Tools[index].Operations = append(
				assembled.Tools[index].Operations,
				pageCatalog.Tools[index].Operations...,
			)
		}
		if !result.Page.HasMore {
			break
		}
		offset = result.Page.NextOffset
	}
	if !reflect.DeepEqual(assembled, catalog) {
		t.Fatal("synthetic final catalog pages did not reassemble exactly")
	}

	page := workbenchAvailableCatalogSlice(catalog, 0, 1)
	page.Tools[0].Operations[0].ArgumentsSchema[0] = 'x'
	page.Tools[1].ValidDecisions[0] = AutomationPermissionDenyPersistent
	if !json.Valid(catalog.Tools[0].Operations[0].ArgumentsSchema) ||
		catalog.Tools[1].ValidDecisions[0] == AutomationPermissionDenyPersistent {
		t.Fatal("catalog page mutation escaped defensive copies")
	}
}

func TestWorkbenchCatalogAvailabilityTracksFinalizedRegistry(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	requiredBaseline := map[WorkbenchOperation]struct{}{
		WorkbenchOperationCatalogList:           {},
		WorkbenchOperationContactsSearch:        {},
		WorkbenchOperationMessagesList:          {},
		WorkbenchOperationMessagesGet:           {},
		WorkbenchOperationCandidatesList:        {},
		WorkbenchOperationCandidatesGet:         {},
		WorkbenchOperationJobsList:              {},
		WorkbenchOperationJobsGet:               {},
		WorkbenchOperationLeadsList:             {},
		WorkbenchOperationLeadsGet:              {},
		WorkbenchOperationOnboardingsList:       {},
		WorkbenchOperationOnboardingsGet:        {},
		WorkbenchOperationCandidatesCreate:      {},
		WorkbenchOperationCandidatesLinkMessage: {},
		WorkbenchOperationJobsCreate:            {},
		WorkbenchOperationLeadsCreate:           {},
		WorkbenchOperationLeadsTransition:       {},
		WorkbenchOperationNotesCreate:           {},
		WorkbenchOperationTasksCreate:           {},
		WorkbenchOperationTasksSetStatus:        {},
		WorkbenchOperationLeadsTerminal:         {},
		WorkbenchOperationMessagesSendPrivate:   {},
		WorkbenchOperationMessagesSendGroup:     {},
	}
	for _, tool := range WorkbenchProviderTools() {
		entry, ok := WorkbenchToolCatalogEntryFor(tool)
		if !ok {
			t.Fatalf("catalog entry missing for %s", tool)
		}
		for _, operation := range entry.Operations {
			definition, found := WorkbenchOperationDefinitionFor(tool, operation.Operation)
			if !found {
				t.Fatalf("registry definition missing for %s/%s", tool, operation.Operation)
			}
			expected := WorkbenchAvailabilityUnavailable
			if workbenchOperationEffectiveAvailable(definition) {
				expected = WorkbenchAvailabilityAvailable
			}
			if operation.Availability != expected {
				t.Errorf(
					"%s/%s availability = %s, want %s",
					tool,
					operation.Operation,
					operation.Availability,
					expected,
				)
			}
			if operation.Availability == WorkbenchAvailabilityAvailable {
				delete(requiredBaseline, operation.Operation)
			}
		}
	}
	if len(requiredBaseline) != 0 {
		t.Errorf("baseline available operations missing from catalog: %#v", requiredBaseline)
	}
}

func TestWorkbenchCatalogExclusionsAreExplicitAndOperationsStayBounded(t *testing.T) {
	catalog := WorkbenchToolCatalog()
	classes := make(map[string]bool, len(catalog.Exclusions))
	for _, exclusion := range catalog.Exclusions {
		if exclusion.Class == "" || exclusion.Description == "" {
			t.Fatalf("invalid exclusion: %+v", exclusion)
		}
		classes[exclusion.Class] = true
	}
	for _, required := range []string{
		"self_modification",
		"secrets",
		"exports",
		"raw_attachments",
		"arbitrary_execution",
		"runtime_targets",
		"payment_execution",
	} {
		if !classes[required] {
			t.Errorf("missing exclusion class %q", required)
		}
	}
	for _, tool := range catalog.Tools {
		for _, operation := range tool.Operations {
			name := string(operation.Operation)
			for _, forbidden := range []string{
				"prompt", "provider", "api_key", "secret", "policy", "permission",
				"export", "attachment", "http", "filesystem", "shell", "sql",
			} {
				if strings.Contains(name, forbidden) {
					t.Errorf("forbidden operation exposed: %s", name)
				}
			}
		}
	}
}

func TestWorkbenchProviderDefinitionsUseExactWrapperSchemas(t *testing.T) {
	definitions, err := WorkbenchProviderToolDefinitions()
	if err != nil {
		t.Fatalf("WorkbenchProviderToolDefinitions() error = %v", err)
	}
	if len(definitions) != 3 {
		t.Fatalf("definitions = %d, want 3", len(definitions))
	}
	names := make([]string, len(definitions))
	for i, definition := range definitions {
		names[i] = string(definition.Name)
		if !definition.Strict || definition.Description == "" {
			t.Fatalf("definition metadata = %+v", definition)
		}
		entry, ok := WorkbenchToolCatalogEntryFor(definition.Name)
		if !ok || definition.Description != entry.Description {
			t.Fatalf("definition description drift for %s", definition.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", definition.Name, err)
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s wrapper schema is not closed: %#v", definition.Name, schema)
		}
		properties := schema["properties"].(map[string]any)
		if len(properties) != 2 || properties["operation"] == nil || properties["arguments_json"] == nil {
			t.Fatalf("%s wrapper properties = %#v", definition.Name, properties)
		}
		argumentsJSON := properties["arguments_json"].(map[string]any)
		description, _ := argumentsJSON["description"].(string)
		if description == "" || len(description) > 4096 {
			t.Fatalf("%s arguments description length = %d", definition.Name, len(description))
		}
		required := interfaceStrings(schema["required"])
		if fmt.Sprint(required) != fmt.Sprint([]string{"operation", "arguments_json"}) {
			t.Fatalf("%s required = %#v", definition.Name, required)
		}
		enum := interfaceStrings(properties["operation"].(map[string]any)["enum"])
		wantOperations := make([]string, len(entry.Operations))
		for i := range entry.Operations {
			wantOperations[i] = string(entry.Operations[i].Operation)
		}
		if fmt.Sprint(enum) != fmt.Sprint(wantOperations) {
			t.Fatalf("%s operation enum = %#v, want %#v", definition.Name, enum, wantOperations)
		}
		switch definition.Name {
		case AutomationToolWorkbenchQuery:
			if !strings.Contains(description, "catalog.list()") ||
				!strings.Contains(description, "candidates.get(id)") {
				t.Fatalf("query arguments description = %q", description)
			}
		case AutomationToolWorkbenchMutate:
			for _, contract := range []string{
				"candidates.update(candidate_id,update)",
				"leads.transition(lead_id,expected_version,from_stage,to_stage)",
				"notes.create(target_type,target_id,body)",
				"tasks.set_status(task_id,request)",
			} {
				if !strings.Contains(description, contract) {
					t.Fatalf("mutate arguments description missing %q: %q", contract, description)
				}
			}
		case AutomationToolWorkbenchAllowOnce:
			if !strings.Contains(description, "messages.send_private(event_id,intent,text)") {
				t.Fatalf("allow_once arguments description = %q", description)
			}
		}
	}
	sort.Strings(names)
	if fmt.Sprint(names) != fmt.Sprint([]string{
		string(AutomationToolWorkbenchAllowOnce),
		string(AutomationToolWorkbenchMutate),
		string(AutomationToolWorkbenchQuery),
	}) {
		t.Fatalf("provider definitions = %#v", names)
	}
}

func TestWorkbenchCatalogReturnsDefensiveCopies(t *testing.T) {
	first, ok := WorkbenchToolCatalogEntryFor(AutomationToolWorkbenchAllowOnce)
	if !ok {
		t.Fatal("allow_once catalog missing")
	}
	sendPrivateIndex := -1
	for i, operation := range first.Operations {
		if operation.Operation == WorkbenchOperationMessagesSendPrivate {
			sendPrivateIndex = i
			break
		}
	}
	if sendPrivateIndex == -1 {
		t.Fatal("messages.send_private operation missing")
	}
	if len(first.Operations[sendPrivateIndex].ValidDecisions) == 0 {
		t.Fatal("messages.send_private decisions are empty")
	}

	first.ValidDecisions[0] = AutomationPermissionAllowPersistent
	first.Operations[sendPrivateIndex].ValidDecisions[0] = AutomationPermissionAllowPersistent
	first.Operations[sendPrivateIndex].ArgumentsSchema[0] = 'x'

	second, _ := WorkbenchToolCatalogEntryFor(AutomationToolWorkbenchAllowOnce)
	if second.ValidDecisions[0] != AutomationPermissionAllowOnce ||
		second.Operations[sendPrivateIndex].ValidDecisions[0] != AutomationPermissionAllowOnce ||
		!json.Valid(second.Operations[sendPrivateIndex].ArgumentsSchema) {
		t.Fatal("catalog mutation escaped defensive copies")
	}
}

func TestWorkbenchCatalogUnavailableOperationReturnsDefensiveCopies(t *testing.T) {
	first, ok := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFridaDetach,
	)
	if !ok {
		t.Fatal("unavailable operation missing")
	}
	if len(first.ValidDecisions) != 0 {
		t.Fatalf("unavailable operation decisions = %#v", first.ValidDecisions)
	}

	first.ValidDecisions = append(first.ValidDecisions, AutomationPermissionAllowOnce)
	first.ArgumentsSchema[0] = 'x'

	second, _ := WorkbenchOperationCatalogEntryFor(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFridaDetach,
	)
	if len(second.ValidDecisions) != 0 || !json.Valid(second.ArgumentsSchema) {
		t.Fatal("unavailable operation mutation escaped defensive copies")
	}
}

func assertWorkbenchSchemaClosed(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode operation schema: %v", err)
	}
	assertWorkbenchSchemaNodeClosed(t, schema)
}

func assertWorkbenchSchemaNodeClosed(t *testing.T, schema map[string]any) {
	t.Helper()
	if schema["type"] == "object" && schema["additionalProperties"] != false {
		t.Fatalf("object schema is not closed: %#v", schema)
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			if nested, ok := property.(map[string]any); ok {
				assertWorkbenchSchemaNodeClosed(t, nested)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		assertWorkbenchSchemaNodeClosed(t, items)
	}
}

func assertWorkbenchSchemaHasNoTrustedFields(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode operation schema: %v", err)
	}
	forbidden := map[string]bool{
		"actor": true, "author": true, "operator_id": true, "confirmed": true,
		"approval": true, "authorization": true, "recipient": true, "sink": true,
		"pid": true, "process": true, "process_name": true, "script": true,
		"path": true, "url": true, "token": true, "secret": true, "prompt": true,
		"policy": true, "permission": true,
	}
	var walk func(map[string]any)
	walk = func(node map[string]any) {
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, property := range properties {
				if forbidden[name] {
					t.Errorf("trusted field %q exposed in schema", name)
				}
				if nested, ok := property.(map[string]any); ok {
					walk(nested)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items)
		}
	}
	walk(schema)
}

func interfaceStrings(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.(string))
	}
	return result
}

func containsWorkbenchDecision(
	decisions []AutomationPermissionDecision,
	want AutomationPermissionDecision,
) bool {
	for _, decision := range decisions {
		if decision == want {
			return true
		}
	}
	return false
}

type workbenchFinalFundsLedgerArguments struct {
	ID     string `json:"id"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

func workbenchFinalAvailableCatalog(t *testing.T) WorkbenchAvailableCatalog {
	t.Helper()
	fundsLedgerSchema, err := workbenchArgumentsSchema(new(workbenchFinalFundsLedgerArguments))
	if err != nil {
		t.Fatalf("build final funds.ledger schema: %v", err)
	}
	fundsLedgerSchema, _ = workbenchOptionalQueryPagination(
		fundsLedgerSchema,
		[]string{"id", "limit", "offset"},
	)

	full := WorkbenchToolCatalog()
	tools := make([]WorkbenchToolCatalogEntry, len(full.Tools))
	for toolIndex, tool := range full.Tools {
		cloned := tool
		cloned.ValidDecisions = append([]AutomationPermissionDecision(nil), tool.ValidDecisions...)
		cloned.Operations = make([]WorkbenchOperationCatalogEntry, 0, len(tool.Operations))
		for _, operation := range tool.Operations {
			if operation.Exposure != WorkbenchExposureAllowed {
				continue
			}
			operation = cloneWorkbenchOperationCatalogEntry(operation)
			operation.ImplementationStatus = WorkbenchOperationWired
			operation.Availability = WorkbenchAvailabilityAvailable
			operation.ValidDecisions = append(
				[]AutomationPermissionDecision(nil),
				tool.ValidDecisions...,
			)
			if operation.Operation == WorkbenchOperationFundsLedger {
				operation.ArgumentsSchema = append(json.RawMessage(nil), fundsLedgerSchema...)
			}
			cloned.Operations = append(cloned.Operations, operation)
		}
		tools[toolIndex] = cloned
	}
	return WorkbenchAvailableCatalog{
		Version: full.Version,
		Tools:   tools,
	}
}
