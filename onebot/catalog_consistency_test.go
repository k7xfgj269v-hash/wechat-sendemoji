package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkbenchCatalogAvailableOperationsMatchRegistryHandlers(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	available := workbenchAvailableOperationSet(t)
	dispatchable := make(map[WorkbenchOperation]struct{})
	for _, definition := range workbenchAllOperationDefinitions() {
		if workbenchOperationEffectiveAvailable(definition) {
			if definition.Handler == nil {
				t.Fatalf("effectively available operation has no handler: %s/%s", definition.Tool, definition.Operation)
			}
			dispatchable[definition.Operation] = struct{}{}
		}
	}

	if len(available) != len(dispatchable) {
		t.Fatalf("available operations = %d, registry handlers = %d", len(available), len(dispatchable))
	}
	for operation := range available {
		if _, ok := dispatchable[operation]; !ok {
			t.Fatalf("catalog available operation missing from registry dispatch: %s", operation)
		}
	}
	for operation := range dispatchable {
		if _, ok := available[operation]; !ok {
			t.Fatalf("registry dispatch operation missing from catalog: %s", operation)
		}
	}
	if len(dispatchable) < 23 || len(dispatchable) > 91 {
		t.Fatalf("registry dispatch operations = %d, want incremental range 23..91", len(dispatchable))
	}
}

func TestWorkbenchCatalogListDispatchReturnsSanitizedAvailableCatalog(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	got := workbenchTraverseCatalogList(t)
	want := WorkbenchAvailableToolCatalog()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog.list view = %#v, want %#v", got, want)
	}
	total := workbenchAvailableCatalogOperationCount(got)
	if total < 61 || total > 91 {
		t.Fatalf("catalog.list operations = %d, want integrated range 61..91", total)
	}
}

func TestWorkbenchCatalogListPaginationTracksDenyOverride(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	operation := WorkbenchOperationContactsSearch
	baseline := workbenchTraverseCatalogList(t)
	if !workbenchCatalogContainsOperation(baseline, operation) {
		t.Fatalf("baseline catalog is missing %s", operation)
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(AutomationToolWorkbenchQuery, operation): {},
	})
	denied := workbenchTraverseCatalogList(t)
	if workbenchCatalogContainsOperation(denied, operation) {
		t.Fatalf("denied catalog still contains %s", operation)
	}
	if !reflect.DeepEqual(denied, WorkbenchAvailableToolCatalog()) {
		t.Fatal("denied paginated catalog does not match the effective availability snapshot")
	}
	for _, tool := range denied.Tools {
		for _, entry := range tool.Operations {
			definition, ok := WorkbenchOperationDefinitionFor(tool.Name, entry.Operation)
			if !ok || definition.Handler == nil || !workbenchOperationEffectiveAvailable(definition) {
				t.Fatalf("catalog operation is not effectively dispatchable: %s/%s", tool.Name, entry.Operation)
			}
		}
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{})
	restored := workbenchTraverseCatalogList(t)
	if !reflect.DeepEqual(restored, baseline) {
		t.Fatal("clearing the deny override did not restore the paginated catalog")
	}
}

func TestWorkbenchCatalogSnapshotIsLinearizableDuringDenyReplacement(t *testing.T) {
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	first := map[string]struct{}{}
	second := map[string]struct{}{}
	index := 0
	for _, definition := range workbenchAllOperationDefinitions() {
		if !workbenchOperationBaseAvailable(definition) ||
			definition.Operation == WorkbenchOperationCatalogList {
			continue
		}
		key := workbenchOperationDenyKey(definition.Tool, definition.Operation)
		if index%2 == 0 {
			first[key] = struct{}{}
		} else {
			second[key] = struct{}{}
		}
		index++
	}
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("deny configurations are incomplete: first=%d second=%d", len(first), len(second))
	}

	applyWorkbenchOperationDenySet(first)
	wantFirstCatalog := WorkbenchAvailableToolCatalog()
	applyWorkbenchOperationDenySet(second)
	wantSecondCatalog := WorkbenchAvailableToolCatalog()
	if reflect.DeepEqual(wantFirstCatalog, wantSecondCatalog) {
		t.Fatal("deny configurations produced identical catalogs")
	}
	arguments := WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: 0}
	wantFirstResult := workbenchCatalogListResult(wantFirstCatalog, arguments)
	wantSecondResult := workbenchCatalogListResult(wantSecondCatalog, arguments)
	decoded, err := DecodeWorkbenchToolRequest(
		AutomationToolWorkbenchQuery,
		workbenchTestWrapper(
			t,
			WorkbenchOperationCatalogList,
			`{"limit":100,"offset":0}`,
		),
	)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolRequest() error = %v", err)
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil)

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			applyWorkbenchOperationDenySet(first)
			runtime.Gosched()
			applyWorkbenchOperationDenySet(second)
			runtime.Gosched()
		}
	}()
	defer func() {
		stop.Store(true)
		<-done
	}()

	for attempt := 0; attempt < 2000; attempt++ {
		catalog := WorkbenchAvailableToolCatalog()
		if !reflect.DeepEqual(catalog, wantFirstCatalog) &&
			!reflect.DeepEqual(catalog, wantSecondCatalog) {
			t.Fatalf(
				"catalog construction observed a hybrid deny snapshot on attempt %d",
				attempt,
			)
		}
		result := dispatcher.Dispatch(
			context.Background(),
			WorkbenchExecutionContext{},
			decoded,
		)
		if !reflect.DeepEqual(result, wantFirstResult) &&
			!reflect.DeepEqual(result, wantSecondResult) {
			t.Fatalf(
				"catalog.list dispatch observed a hybrid deny snapshot on attempt %d",
				attempt,
			)
		}
	}
}

func workbenchTraverseCatalogList(t *testing.T) WorkbenchAvailableCatalog {
	t.Helper()
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil)
	assembled := WorkbenchAvailableCatalog{}
	offset := 0
	pageIndex := 0
	for {
		argumentsJSON := `{}`
		if pageIndex > 0 {
			argumentsJSON = fmt.Sprintf(
				`{"limit":%d,"offset":%d}`,
				WorkbenchDefaultPageLimit,
				offset,
			)
		}
		result := workbenchDispatchCatalogPage(t, dispatcher, argumentsJSON)
		repeated := workbenchDispatchCatalogPage(t, dispatcher, argumentsJSON)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode catalog.list result: %v", err)
		}
		repeatedEncoded, err := json.Marshal(repeated)
		if err != nil {
			t.Fatalf("encode repeated catalog.list result: %v", err)
		}
		if !bytes.Equal(encoded, repeatedEncoded) {
			t.Fatalf("catalog.list page at offset %d is not deterministic", offset)
		}
		if len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf(
				"catalog.list result bytes = %d, max %d",
				len(encoded),
				WorkbenchMaxResultJSONBytes,
			)
		}
		if err := ValidateWorkbenchToolResult(result); err != nil {
			t.Fatalf("ValidateWorkbenchToolResult() error = %v", err)
		}
		pageCatalog := workbenchDecodeCatalogPage(t, result.Data)
		returned := workbenchAvailableCatalogOperationCount(pageCatalog)
		if result.Page == nil ||
			result.Page.Limit != WorkbenchDefaultPageLimit ||
			result.Page.Offset != offset ||
			result.Page.Returned != returned ||
			result.Page.Total < returned ||
			result.Page.HasMore != (offset+returned < result.Page.Total) {
			t.Fatalf("catalog.list page metadata = %+v, returned=%d", result.Page, returned)
		}
		if result.Page.HasMore {
			if returned == 0 || result.Page.NextOffset != offset+returned {
				t.Fatalf("catalog.list page did not advance: %+v", result.Page)
			}
		} else if result.Page.NextOffset != 0 {
			t.Fatalf("terminal catalog.list next offset = %d", result.Page.NextOffset)
		}

		if pageIndex == 0 {
			assembled = pageCatalog
		} else {
			if assembled.Version != pageCatalog.Version ||
				len(assembled.Tools) != len(pageCatalog.Tools) {
				t.Fatal("catalog.list metadata changed between pages")
			}
			for index := range assembled.Tools {
				assembledMetadata := assembled.Tools[index]
				pageMetadata := pageCatalog.Tools[index]
				assembledMetadata.Operations = nil
				pageMetadata.Operations = nil
				if !reflect.DeepEqual(assembledMetadata, pageMetadata) {
					t.Fatalf("catalog.list tool metadata changed at page %d tool %d", pageIndex, index)
				}
				assembled.Tools[index].Operations = append(
					assembled.Tools[index].Operations,
					pageCatalog.Tools[index].Operations...,
				)
			}
		}
		if !result.Page.HasMore {
			if offset+returned != result.Page.Total {
				t.Fatalf("terminal catalog.list coverage = %d, total %d", offset+returned, result.Page.Total)
			}
			return assembled
		}
		offset = result.Page.NextOffset
		pageIndex++
	}
}

func workbenchDispatchCatalogPage(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	argumentsJSON string,
) WorkbenchToolResult {
	t.Helper()
	decoded, err := DecodeWorkbenchToolRequest(
		AutomationToolWorkbenchQuery,
		workbenchTestWrapper(t, WorkbenchOperationCatalogList, argumentsJSON),
	)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolRequest() error = %v", err)
	}
	result := dispatcher.Dispatch(
		context.Background(),
		WorkbenchExecutionContext{
			OperatorID:         "operator-1",
			PermissionDecision: AutomationPermissionAllowOnce,
		},
		decoded,
	)
	if !result.OK {
		t.Fatalf("catalog.list result = %+v", result)
	}
	return result
}

func workbenchDecodeCatalogPage(
	t *testing.T,
	data json.RawMessage,
) WorkbenchAvailableCatalog {
	t.Helper()
	if !json.Valid(data) {
		t.Fatalf("catalog.list data is not valid JSON: %s", data)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode catalog.list payload: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("catalog.list payload keys = %v", mapKeys(raw))
	}
	if _, ok := raw["version"]; !ok {
		t.Fatal("catalog.list payload missing version")
	}
	if _, ok := raw["tools"]; !ok {
		t.Fatal("catalog.list payload missing tools")
	}
	assertWorkbenchCatalogPageSanitized(t, data)

	var catalog WorkbenchAvailableCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("decode catalog.list view: %v", err)
	}
	wantTools := WorkbenchProviderTools()
	if len(catalog.Tools) != len(wantTools) {
		t.Fatalf("catalog.list tools = %d, want %d", len(catalog.Tools), len(wantTools))
	}
	for index, want := range wantTools {
		if catalog.Tools[index].Name != want {
			t.Fatalf("catalog.list tool[%d] = %s, want %s", index, catalog.Tools[index].Name, want)
		}
	}
	return catalog
}

func assertWorkbenchCatalogPageSanitized(t *testing.T, data json.RawMessage) {
	t.Helper()
	payload := strings.ToLower(string(data))
	for _, prohibited := range []string{
		`"exclusions"`,
		`"api_key"`,
		`"token"`,
		`"cookie"`,
		`"secret"`,
		`"private_key"`,
		`"path"`,
		`"handler"`,
		`"provider_payload"`,
		"internal handler",
		"/private/",
		"/users/",
		`c:\\`,
	} {
		if strings.Contains(payload, prohibited) {
			t.Fatalf("catalog.list payload contains prohibited text %q", prohibited)
		}
	}
}

func workbenchCatalogContainsOperation(
	catalog WorkbenchAvailableCatalog,
	operation WorkbenchOperation,
) bool {
	for _, tool := range catalog.Tools {
		for _, entry := range tool.Operations {
			if entry.Operation == operation {
				return true
			}
		}
	}
	return false
}

func workbenchAvailableOperationSet(t *testing.T) map[WorkbenchOperation]struct{} {
	t.Helper()
	catalog := WorkbenchToolCatalog()
	available := make(map[WorkbenchOperation]struct{})
	for _, tool := range catalog.Tools {
		for _, operation := range tool.Operations {
			if operation.Availability == WorkbenchAvailabilityAvailable {
				available[operation.Operation] = struct{}{}
			}
		}
	}
	return available
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
