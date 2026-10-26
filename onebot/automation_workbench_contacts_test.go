package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestM101ContactsGetAndRefreshExactResultSchema(t *testing.T) {
	agent := &task003AgentClient{
		contactResult: Contact{
			Username:     "wxid_ada",
			NickName:     "Ada",
			Alias:        "ada",
			Remark:       "Candidate",
			LocalType:    3,
			BigHeadURL:   "https://img.example/ada-big",
			SmallHeadURL: "https://img.example/ada-small",
			HeadImgURL:   "https://img.example/ada",
			IsInChatRoom: 1,
			Description:  "First programmer",
		},
		refreshResult: AgentContactCounts{
			Contacts:    11,
			Groups:      3,
			Members:     27,
			RefreshedAt: "2026-07-30T03:30:00Z",
		},
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)

	detail := m101DispatchJSON(
		t,
		context.Background(),
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationContactsGet,
		`{"username":" wxid_ada "}`,
	)
	m101AssertExactJSON(t, detail, `{
		"operation":"contacts.get",
		"ok":true,
		"data":{
			"username":"wxid_ada",
			"nick_name":"Ada",
			"alias":"ada",
			"remark":"Candidate",
			"local_type":3,
			"big_head_url":"https://img.example/ada-big",
			"small_head_url":"https://img.example/ada-small",
			"head_img_url":"https://img.example/ada",
			"is_in_chat_room":1,
			"description":"First programmer"
		}
	}`)

	refreshed := m101DispatchJSON(
		t,
		context.Background(),
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationContactsRefresh,
		`{}`,
	)
	m101AssertExactJSON(t, refreshed, `{
		"operation":"contacts.refresh",
		"ok":true,
		"data":{
			"contacts":11,
			"groups":3,
			"members":27,
			"refreshed_at":"2026-07-30T03:30:00Z"
		}
	}`)
}

func TestM101GroupsListExactSchemaStableSortAndPagination(t *testing.T) {
	agent := &task003AgentClient{
		groupsResult: []Group{
			{
				Username: "zeta@chatroom", NickName: "Zeta first", Remark: "first",
				Owner: "owner-first", MemberCount: 4,
				BigHeadURL: "zeta-first-big", SmallHeadURL: "zeta-first-small",
				HeadImgURL: "zeta-first",
			},
			{
				Username: "alpha@chatroom", NickName: "Alpha", Remark: "alpha",
				Owner: "owner-alpha", MemberCount: 2,
				BigHeadURL: "alpha-big", SmallHeadURL: "alpha-small",
				HeadImgURL: "alpha",
			},
			{
				Username: "zeta@chatroom", NickName: "Zeta second", Remark: "second",
				Owner: "owner-second", MemberCount: 6,
				BigHeadURL: "zeta-second-big", SmallHeadURL: "zeta-second-small",
				HeadImgURL: "zeta-second",
			},
		},
	}
	result := m101DispatchJSON(
		t,
		context.Background(),
		NewDefaultWorkbenchToolDispatcher(nil, agent),
		AutomationToolWorkbenchQuery,
		WorkbenchOperationGroupsList,
		`{"limit":2,"offset":1}`,
	)
	m101AssertExactJSON(t, result, `{
		"operation":"groups.list",
		"ok":true,
		"data":{
			"groups":[
				{
					"username":"zeta@chatroom",
					"nick_name":"Zeta first",
					"remark":"first",
					"owner":"owner-first",
					"member_count":4,
					"big_head_url":"zeta-first-big",
					"small_head_url":"zeta-first-small",
					"head_img_url":"zeta-first"
				},
				{
					"username":"zeta@chatroom",
					"nick_name":"Zeta second",
					"remark":"second",
					"owner":"owner-second",
					"member_count":6,
					"big_head_url":"zeta-second-big",
					"small_head_url":"zeta-second-small",
					"head_img_url":"zeta-second"
				}
			]
		},
		"page":{
			"limit":2,
			"offset":1,
			"returned":2,
			"total":3,
			"has_more":false
		}
	}`)
}

func TestM101GroupMembersExactSchemaStableSortAndPagination(t *testing.T) {
	agent := &task003AgentClient{
		groupMembersResult: []GroupMember{
			{
				GroupID: "team@chatroom", Username: "wxid_zeta", NickName: "Zeta",
				Remark: "zeta", Alias: "z", LocalType: 4,
				BigHeadURL: "zeta-big", SmallHeadURL: "zeta-small",
				HeadImgURL: "zeta", DisplayName: "Zeta display",
			},
			{
				GroupID: "team@chatroom", Username: "wxid_alpha", NickName: "Alpha first",
				Remark: "first", Alias: "a1", LocalType: 1,
				BigHeadURL: "alpha-first-big", SmallHeadURL: "alpha-first-small",
				HeadImgURL: "alpha-first", DisplayName: "Alpha first display",
			},
			{
				GroupID: "team@chatroom", Username: "wxid_alpha", NickName: "Alpha second",
				Remark: "second", Alias: "a2", LocalType: 2,
				BigHeadURL: "alpha-second-big", SmallHeadURL: "alpha-second-small",
				HeadImgURL: "alpha-second", DisplayName: "Alpha second display",
			},
		},
	}
	result := m101DispatchJSON(
		t,
		context.Background(),
		NewDefaultWorkbenchToolDispatcher(nil, agent),
		AutomationToolWorkbenchQuery,
		WorkbenchOperationGroupsMembers,
		`{"group_id":"team@chatroom","limit":2,"offset":0}`,
	)
	m101AssertExactJSON(t, result, `{
		"operation":"groups.members",
		"ok":true,
		"data":{
			"members":[
				{
					"username":"wxid_alpha",
					"nick_name":"Alpha first",
					"remark":"first",
					"alias":"a1",
					"local_type":1,
					"big_head_url":"alpha-first-big",
					"small_head_url":"alpha-first-small",
					"head_img_url":"alpha-first",
					"display_name":"Alpha first display"
				},
				{
					"username":"wxid_alpha",
					"nick_name":"Alpha second",
					"remark":"second",
					"alias":"a2",
					"local_type":2,
					"big_head_url":"alpha-second-big",
					"small_head_url":"alpha-second-small",
					"head_img_url":"alpha-second",
					"display_name":"Alpha second display"
				}
			]
		},
		"page":{
			"limit":2,
			"offset":0,
			"returned":2,
			"total":3,
			"has_more":true,
			"next_offset":2
		}
	}`)
}

func TestM101ListPaginationNormalizationAndNonNilEmptyArrays(t *testing.T) {
	agent := &task003AgentClient{}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)

	groupsDecoded := m101Decode(
		t,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationGroupsList,
		`{}`,
	)
	groupsArguments := groupsDecoded.Arguments.(*WorkbenchPageArguments)
	if groupsArguments.Limit != WorkbenchDefaultPageLimit || groupsArguments.Offset != 0 {
		t.Fatalf("groups.list normalized arguments = %+v", groupsArguments)
	}
	if groupsDecoded.Request.ArgumentsJSON != `{"limit":50,"offset":0}` {
		t.Fatalf("groups.list canonical arguments = %s", groupsDecoded.Request.ArgumentsJSON)
	}
	groupsResult := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, groupsDecoded)
	var groupsPayload struct {
		Groups []workbenchGroupProjection `json:"groups"`
	}
	if err := json.Unmarshal(groupsResult.Data, &groupsPayload); err != nil {
		t.Fatal(err)
	}
	if groupsPayload.Groups == nil || len(groupsPayload.Groups) != 0 {
		t.Fatalf("groups.list empty payload = %#v, want non-nil empty array", groupsPayload.Groups)
	}
	if groupsResult.Page == nil ||
		groupsResult.Page.Limit != WorkbenchDefaultPageLimit ||
		groupsResult.Page.Offset != 0 ||
		groupsResult.Page.Total != 0 ||
		groupsResult.Page.Returned != 0 ||
		groupsResult.Page.HasMore ||
		groupsResult.Page.NextOffset != 0 {
		t.Fatalf("groups.list empty page = %+v", groupsResult.Page)
	}

	agent.groupMembersResult = []GroupMember{{GroupID: "team@chatroom", Username: "wxid_one"}}
	membersDecoded := m101Decode(
		t,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationGroupsMembers,
		`{"group_id":" team@chatroom ","limit":100,"offset":4}`,
	)
	membersArguments := membersDecoded.Arguments.(*WorkbenchGroupMembersArguments)
	if membersArguments.GroupID != "team@chatroom" ||
		membersArguments.Limit != WorkbenchMaxPageLimit ||
		membersArguments.Offset != 4 {
		t.Fatalf("groups.members normalized arguments = %+v", membersArguments)
	}
	membersResult := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, membersDecoded)
	var membersPayload struct {
		Members []workbenchGroupMemberProjection `json:"members"`
	}
	if err := json.Unmarshal(membersResult.Data, &membersPayload); err != nil {
		t.Fatal(err)
	}
	if membersPayload.Members == nil || len(membersPayload.Members) != 0 {
		t.Fatalf("groups.members empty payload = %#v, want non-nil empty array", membersPayload.Members)
	}
	if membersResult.Page == nil ||
		membersResult.Page.Limit != WorkbenchMaxPageLimit ||
		membersResult.Page.Offset != 4 ||
		membersResult.Page.Total != 1 ||
		membersResult.Page.Returned != 0 ||
		membersResult.Page.HasMore ||
		membersResult.Page.NextOffset != 0 {
		t.Fatalf("groups.members empty page = %+v", membersResult.Page)
	}
}

func TestM101MaximumAgentFrameContactProjectionIsBoundedAndStable(t *testing.T) {
	contact := Contact{LocalType: 7, IsInChatRoom: 1}
	large := m101MaximumAgentFrameHTMLValue(t, func(value string) any {
		contact.Username = value
		contact.NickName = value
		contact.Alias = value
		contact.Remark = value
		contact.BigHeadURL = value
		contact.SmallHeadURL = value
		contact.HeadImgURL = value
		contact.Description = value
		return contact
	})
	contact.Username = large
	contact.NickName = large
	contact.Alias = large
	contact.Remark = large
	contact.BigHeadURL = large
	contact.SmallHeadURL = large
	contact.HeadImgURL = large
	contact.Description = large
	m101AssertAgentFrameNearMaximum(t, contact)

	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, &task003AgentClient{contactResult: contact})
	first := m101DispatchJSON(
		t,
		context.Background(),
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationContactsGet,
		`{"username":"wxid_lookup"}`,
	)
	second := m101DispatchJSON(
		t,
		context.Background(),
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationContactsGet,
		`{"username":"wxid_lookup"}`,
	)
	m101AssertBoundedStableResult(t, first, second)

	var projection workbenchContactProjection
	if err := json.Unmarshal(first.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Truncations) != 8 {
		t.Fatalf("contact truncations = %#v, want all eight string fields", projection.Truncations)
	}
	m101AssertTruncations(t, projection.Truncations)
}

type m101RefreshSideEffectAgent struct {
	task003AgentClient

	result      AgentContactCounts
	sideEffects int
}

func (a *m101RefreshSideEffectAgent) RefreshContacts(context.Context) (AgentContactCounts, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "contacts.refresh")
	a.sideEffects++
	return a.result, nil
}

func TestM101MaximumAgentFrameRefreshProjectionCommitsOnceAndReturnsBounded(t *testing.T) {
	counts := AgentContactCounts{Contacts: 101, Groups: 17, Members: 809}
	large := m101MaximumAgentFrameHTMLValue(t, func(value string) any {
		counts.RefreshedAt = value
		return counts
	})
	counts.RefreshedAt = large
	m101AssertAgentFrameNearMaximum(t, counts)

	agent := &m101RefreshSideEffectAgent{result: counts}
	result := m101DispatchJSON(
		t,
		context.Background(),
		NewDefaultWorkbenchToolDispatcher(nil, agent),
		AutomationToolWorkbenchMutate,
		WorkbenchOperationContactsRefresh,
		`{}`,
	)
	if !result.OK {
		t.Fatalf("contacts.refresh = %+v", result)
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatalf("ValidateWorkbenchToolResult() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("result size = %d, want <= %d", len(encoded), WorkbenchMaxResultJSONBytes)
	}

	var projection workbenchContactRefreshProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Contacts != counts.Contacts ||
		projection.Groups != counts.Groups ||
		projection.Members != counts.Members {
		t.Fatalf("refresh projection counts = %+v, want %+v", projection, counts)
	}
	if projection.RefreshedAt == counts.RefreshedAt || len(projection.Truncations) != 1 {
		t.Fatalf("refresh projection = %+v, want one truncated refreshed_at", projection)
	}
	truncation := projection.Truncations[0]
	if truncation.Field != "refreshed_at" ||
		truncation.OriginalCharacters != len([]rune(counts.RefreshedAt)) {
		t.Fatalf("refresh truncation = %+v", truncation)
	}
	m101AssertTruncations(t, projection.Truncations)

	agent.mu.Lock()
	sideEffects := agent.sideEffects
	agent.mu.Unlock()
	if sideEffects != 1 || agent.callCount("contacts.refresh") != 1 {
		t.Fatalf(
			"refresh side effects = %d calls = %d, want one each",
			sideEffects,
			agent.callCount("contacts.refresh"),
		)
	}
	m101AssertNoFridaCalls(t, &agent.task003AgentClient)
}

func TestM101MaximumAgentFrameGroupsTraverseWithoutGapsOrDuplicates(t *testing.T) {
	const count = 20
	groups := make([]Group, count)
	large := m101MaximumAgentFrameHTMLValue(t, func(value string) any {
		for index := range groups {
			groups[index] = Group{
				Username:     fmt.Sprintf("group-%03d@chatroom", count-index-1),
				NickName:     value,
				Remark:       value,
				Owner:        value,
				MemberCount:  index + 1,
				BigHeadURL:   value,
				SmallHeadURL: value,
				HeadImgURL:   value,
			}
		}
		return groups
	})
	for index := range groups {
		groups[index] = Group{
			Username:     fmt.Sprintf("group-%03d@chatroom", count-index-1),
			NickName:     large,
			Remark:       large,
			Owner:        large,
			MemberCount:  index + 1,
			BigHeadURL:   large,
			SmallHeadURL: large,
			HeadImgURL:   large,
		}
	}
	m101AssertAgentFrameNearMaximum(t, groups)

	agent := &task003AgentClient{groupsResult: groups}
	m101AssertCompleteGroupTraversal(t, agent, count)
}

func TestM101MaximumAgentFrameMembersTraverseWithoutGapsOrDuplicates(t *testing.T) {
	const count = 20
	members := make([]GroupMember, count)
	large := m101MaximumAgentFrameHTMLValue(t, func(value string) any {
		for index := range members {
			members[index] = GroupMember{
				GroupID:      "large@chatroom",
				Username:     fmt.Sprintf("wxid_member_%03d", count-index-1),
				NickName:     value,
				Remark:       value,
				Alias:        value,
				LocalType:    index + 1,
				BigHeadURL:   value,
				SmallHeadURL: value,
				HeadImgURL:   value,
				DisplayName:  value,
			}
		}
		return members
	})
	for index := range members {
		members[index] = GroupMember{
			GroupID:      "large@chatroom",
			Username:     fmt.Sprintf("wxid_member_%03d", count-index-1),
			NickName:     large,
			Remark:       large,
			Alias:        large,
			LocalType:    index + 1,
			BigHeadURL:   large,
			SmallHeadURL: large,
			HeadImgURL:   large,
			DisplayName:  large,
		}
	}
	m101AssertAgentFrameNearMaximum(t, members)

	agent := &task003AgentClient{groupMembersResult: members}
	m101AssertCompleteMemberTraversal(t, agent, count)
}

func TestM101ContactDetailAndGroupMembersMapNotFound(t *testing.T) {
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
		call      string
		agent     *task003AgentClient
	}{
		{
			name:      "contact detail",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationContactsGet,
			arguments: `{"username":"wxid_missing"}`,
			call:      "contacts.get",
			agent: &task003AgentClient{
				contactErr: &AgentError{
					Code: AgentErrorCodeNotFound, Message: "contact was not found",
				},
			},
		},
		{
			name:      "group members",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationGroupsMembers,
			arguments: `{"group_id":"missing@chatroom"}`,
			call:      "groups.members",
			agent: &task003AgentClient{
				groupMembersErr: &AgentError{
					Code: AgentErrorCodeNotFound, Message: "group was not found",
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := m101DispatchJSON(
				t,
				context.Background(),
				NewDefaultWorkbenchToolDispatcher(nil, test.agent),
				test.tool,
				test.operation,
				test.arguments,
			)
			m101AssertFailure(t, result, WorkbenchErrorNotFound, false)
			if test.agent.callCount(test.call) != 1 {
				t.Fatalf("%s calls = %d, want 1", test.call, test.agent.callCount(test.call))
			}
		})
	}
}

func TestM101RejectsInvalidArguments(t *testing.T) {
	tooLongUsername, err := json.Marshal(map[string]string{
		"username": strings.Repeat("x", 257),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{"contact missing username", AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, `{}`},
		{"contact blank username", AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, `{"username":"   "}`},
		{"contact oversized username", AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, string(tooLongUsername)},
		{"contact unknown field", AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, `{"username":"wxid_a","extra":true}`},
		{"refresh null arguments", AutomationToolWorkbenchMutate, WorkbenchOperationContactsRefresh, `null`},
		{"refresh unknown field", AutomationToolWorkbenchMutate, WorkbenchOperationContactsRefresh, `{"force":true}`},
		{"groups limit above maximum", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsList, `{"limit":101}`},
		{"groups negative offset", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsList, `{"offset":-1}`},
		{"members missing group", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{}`},
		{"members invalid group", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"wxid_not_a_group"}`},
		{"members limit above maximum", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"team@chatroom","limit":101}`},
		{"members negative offset", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"team@chatroom","offset":-1}`},
		{"members unknown field", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"team@chatroom","cursor":"raw"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := m101RawRequest(t, test.operation, test.arguments)
			if _, err := DecodeWorkbenchToolRequest(test.tool, raw); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("DecodeWorkbenchToolRequest() error = %v, want validation error", err)
			}
		})
	}
}

func TestM101ContactsOperationsHandleNilUnavailableAndCanceledAgent(t *testing.T) {
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
		call      string
		setError  func(*task003AgentClient, error)
	}{
		{
			name: "contacts.get", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationContactsGet,
			arguments: `{"username":"wxid_a"}`, call: "contacts.get",
			setError: func(agent *task003AgentClient, err error) { agent.contactErr = err },
		},
		{
			name: "contacts.refresh", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationContactsRefresh,
			arguments: `{}`, call: "contacts.refresh",
			setError: func(agent *task003AgentClient, err error) { agent.refreshErr = err },
		},
		{
			name: "groups.list", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationGroupsList,
			arguments: `{}`, call: "groups.list",
			setError: func(agent *task003AgentClient, err error) { agent.groupsErr = err },
		},
		{
			name: "groups.members", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationGroupsMembers,
			arguments: `{"group_id":"team@chatroom"}`, call: "groups.members",
			setError: func(agent *task003AgentClient, err error) { agent.groupMembersErr = err },
		},
	}
	for _, test := range tests {
		t.Run(test.name+"/nil", func(t *testing.T) {
			result := m101DispatchJSON(
				t,
				context.Background(),
				NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{}),
				test.tool,
				test.operation,
				test.arguments,
			)
			m101AssertFailure(t, result, WorkbenchErrorUnavailable, true)
			if result.Error.Message != "WeChat agent is unavailable" {
				t.Fatalf("nil agent message = %q", result.Error.Message)
			}
		})

		t.Run(test.name+"/unavailable", func(t *testing.T) {
			agent := &task003AgentClient{}
			test.setError(agent, &AgentError{
				Code:      AgentErrorCodeUnavailable,
				Message:   "contact cache is unavailable",
				Retryable: true,
			})
			result := m101DispatchJSON(
				t,
				context.Background(),
				NewDefaultWorkbenchToolDispatcher(nil, agent),
				test.tool,
				test.operation,
				test.arguments,
			)
			m101AssertFailure(t, result, WorkbenchErrorUnavailable, true)
			if result.Error.Message != "contact cache is unavailable" {
				t.Fatalf("unavailable agent message = %q", result.Error.Message)
			}
			if agent.callCount(test.call) != 1 {
				t.Fatalf("%s calls = %d, want 1", test.call, agent.callCount(test.call))
			}
		})

		t.Run(test.name+"/canceled", func(t *testing.T) {
			agent := &task003AgentClient{}
			test.setError(agent, context.Canceled)
			result := m101DispatchJSON(
				t,
				context.Background(),
				NewDefaultWorkbenchToolDispatcher(nil, agent),
				test.tool,
				test.operation,
				test.arguments,
			)
			m101AssertFailure(t, result, WorkbenchErrorCancelled, false)
			if agent.callCount(test.call) != 1 {
				t.Fatalf("%s calls = %d, want 1", test.call, agent.callCount(test.call))
			}
		})
	}
}

var m101TypedNilAgentCalls atomic.Int64

type m101TypedNilAgent struct {
	*task003AgentClient
}

var _ AgentClient = (*m101TypedNilAgent)(nil)

func (*m101TypedNilAgent) Contact(context.Context, string) (Contact, error) {
	m101TypedNilAgentCalls.Add(1)
	return Contact{}, nil
}

func (*m101TypedNilAgent) RefreshContacts(context.Context) (AgentContactCounts, error) {
	m101TypedNilAgentCalls.Add(1)
	return AgentContactCounts{}, nil
}

func (*m101TypedNilAgent) Groups(context.Context) ([]Group, error) {
	m101TypedNilAgentCalls.Add(1)
	return nil, nil
}

func (*m101TypedNilAgent) GroupMembers(context.Context, string) ([]GroupMember, error) {
	m101TypedNilAgentCalls.Add(1)
	return nil, nil
}

func TestM101ContactsOperationsTreatTypedNilAgentAsUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{"contacts.get", AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, `{"username":"wxid_a"}`},
		{"contacts.refresh", AutomationToolWorkbenchMutate, WorkbenchOperationContactsRefresh, `{}`},
		{"groups.list", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsList, `{}`},
		{"groups.members", AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"team@chatroom"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m101TypedNilAgentCalls.Store(0)
			var agent *m101TypedNilAgent
			result := m101DispatchJSON(
				t,
				context.Background(),
				NewDefaultWorkbenchToolDispatcher(nil, agent),
				test.tool,
				test.operation,
				test.arguments,
			)
			m101AssertFailure(t, result, WorkbenchErrorUnavailable, true)
			if result.Error.Message != "WeChat agent is unavailable" {
				t.Fatalf("typed-nil agent message = %q", result.Error.Message)
			}
			if calls := m101TypedNilAgentCalls.Load(); calls != 0 {
				t.Fatalf("typed-nil agent calls = %d, want 0", calls)
			}
		})
	}
}

func TestM101ContactsOperationsMapUpstreamProtocolFailures(t *testing.T) {
	operations := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
		call      string
		setError  func(*task003AgentClient, error)
	}{
		{
			name: "contacts.get", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationContactsGet,
			arguments: `{"username":"wxid_a"}`, call: "contacts.get",
			setError: func(agent *task003AgentClient, err error) { agent.contactErr = err },
		},
		{
			name: "contacts.refresh", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationContactsRefresh,
			arguments: `{}`, call: "contacts.refresh",
			setError: func(agent *task003AgentClient, err error) { agent.refreshErr = err },
		},
		{
			name: "groups.list", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationGroupsList,
			arguments: `{}`, call: "groups.list",
			setError: func(agent *task003AgentClient, err error) { agent.groupsErr = err },
		},
		{
			name: "groups.members", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationGroupsMembers,
			arguments: `{"group_id":"team@chatroom"}`, call: "groups.members",
			setError: func(agent *task003AgentClient, err error) { agent.groupMembersErr = err },
		},
	}
	failures := []struct {
		code        string
		wantCode    WorkbenchErrorCode
		retryable   bool
		wantMessage string
	}{
		{
			code: AgentErrorCodeInvalidFrame, wantCode: WorkbenchErrorInternal,
			wantMessage: workbenchContactsInvalidAgentResponseMessage,
		},
		{
			code: AgentErrorCodeFrameTooLarge, wantCode: WorkbenchErrorInternal,
			wantMessage: workbenchContactsInvalidAgentResponseMessage,
		},
		{
			code: AgentErrorCodeUnsupportedVersion, wantCode: WorkbenchErrorUnavailable,
			retryable: true, wantMessage: workbenchContactsIncompatibleAgentMessage,
		},
		{
			code: AgentErrorCodeUnsupportedOperation, wantCode: WorkbenchErrorUnavailable,
			retryable: true, wantMessage: workbenchContactsIncompatibleAgentMessage,
		},
	}
	for _, operation := range operations {
		for _, failure := range failures {
			t.Run(operation.name+"/"+failure.code, func(t *testing.T) {
				agent := &task003AgentClient{}
				operation.setError(agent, &AgentError{
					Code:      failure.code,
					Message:   "/Users/operator/.ssh/private.pem token=secret",
					Retryable: true,
				})
				result := m101DispatchJSON(
					t,
					context.Background(),
					NewDefaultWorkbenchToolDispatcher(nil, agent),
					operation.tool,
					operation.operation,
					operation.arguments,
				)
				m101AssertFailure(t, result, failure.wantCode, failure.retryable)
				if result.Error.Message != failure.wantMessage {
					t.Fatalf("%s message = %q, want %q", failure.code, result.Error.Message, failure.wantMessage)
				}
				if agent.callCount(operation.call) != 1 {
					t.Fatalf("%s calls = %d, want 1", operation.call, agent.callCount(operation.call))
				}
			})
		}
	}
}

func TestM101CanceledContextPreventsAgentCalls(t *testing.T) {
	agent := &task003AgentClient{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := m101DispatchJSON(
		t,
		ctx,
		NewDefaultWorkbenchToolDispatcher(nil, agent),
		AutomationToolWorkbenchMutate,
		WorkbenchOperationContactsRefresh,
		`{}`,
	)
	m101AssertFailure(t, result, WorkbenchErrorCancelled, false)
	if len(m101AgentCalls(agent)) != 0 {
		t.Fatalf("pre-canceled dispatch reached agent: %#v", m101AgentCalls(agent))
	}
}

func TestM101OperationDenyBlocksAllContactAgentCalls(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(
			AutomationToolWorkbenchQuery,
			WorkbenchOperationContactsGet,
		): {},
		workbenchOperationDenyKey(
			AutomationToolWorkbenchMutate,
			WorkbenchOperationContactsRefresh,
		): {},
		workbenchOperationDenyKey(
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsList,
		): {},
		workbenchOperationDenyKey(
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsMembers,
		): {},
	})
	t.Cleanup(func() {
		applyWorkbenchOperationDenySet(map[string]struct{}{})
	})

	agent := &task003AgentClient{}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)
	for _, test := range []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{AutomationToolWorkbenchQuery, WorkbenchOperationContactsGet, `{"username":"wxid_a"}`},
		{AutomationToolWorkbenchMutate, WorkbenchOperationContactsRefresh, `{}`},
		{AutomationToolWorkbenchQuery, WorkbenchOperationGroupsList, `{}`},
		{AutomationToolWorkbenchQuery, WorkbenchOperationGroupsMembers, `{"group_id":"team@chatroom"}`},
	} {
		result := m101DispatchJSON(
			t,
			context.Background(),
			dispatcher,
			test.tool,
			test.operation,
			test.arguments,
		)
		m101AssertFailure(t, result, WorkbenchErrorUnavailable, false)
		if result.Error.Message != workbenchUnavailableMessage {
			t.Fatalf("%s deny message = %q", test.operation, result.Error.Message)
		}
	}
	if len(m101AgentCalls(agent)) != 0 {
		t.Fatalf("denied operations reached agent: %#v", m101AgentCalls(agent))
	}
}

func TestM101RefreshDelegatesExactlyOnceWithoutFridaLifecycle(t *testing.T) {
	agent := &task003AgentClient{
		refreshResult: AgentContactCounts{Contacts: 5, Groups: 2, Members: 8},
	}
	result := m101DispatchJSON(
		t,
		context.Background(),
		NewDefaultWorkbenchToolDispatcher(nil, agent),
		AutomationToolWorkbenchMutate,
		WorkbenchOperationContactsRefresh,
		`{}`,
	)
	if !result.OK {
		t.Fatalf("contacts.refresh = %+v", result)
	}
	if calls := m101AgentCalls(agent); !reflect.DeepEqual(calls, []string{"contacts.refresh"}) {
		t.Fatalf("contacts.refresh agent calls = %#v, want one refresh call", calls)
	}
	m101AssertNoFridaCalls(t, agent)
}

type m101RefreshContextKey struct{}

type m101RefreshIsolationAgent struct {
	task003AgentClient

	entered chan string
	release chan struct{}
	results map[string]AgentContactCounts
	seen    map[string]int
}

func (a *m101RefreshIsolationAgent) RefreshContacts(ctx context.Context) (AgentContactCounts, error) {
	requestID, _ := ctx.Value(m101RefreshContextKey{}).(string)
	a.mu.Lock()
	a.calls = append(a.calls, "contacts.refresh")
	a.seen[requestID]++
	result, ok := a.results[requestID]
	a.mu.Unlock()
	if !ok {
		return AgentContactCounts{}, errors.New("missing refresh request id")
	}
	select {
	case a.entered <- requestID:
	case <-ctx.Done():
		return AgentContactCounts{}, ctx.Err()
	}
	select {
	case <-a.release:
		return result, nil
	case <-ctx.Done():
		return AgentContactCounts{}, ctx.Err()
	}
}

func TestM101ConcurrentRefreshRequestIsolation(t *testing.T) {
	expected := map[string]AgentContactCounts{
		"request-a": {Contacts: 1, Groups: 11, Members: 21, RefreshedAt: "2026-07-30T03:31:01Z"},
		"request-b": {Contacts: 2, Groups: 12, Members: 22, RefreshedAt: "2026-07-30T03:31:02Z"},
		"request-c": {Contacts: 3, Groups: 13, Members: 23, RefreshedAt: "2026-07-30T03:31:03Z"},
		"request-d": {Contacts: 4, Groups: 14, Members: 24, RefreshedAt: "2026-07-30T03:31:04Z"},
		"request-e": {Contacts: 5, Groups: 15, Members: 25, RefreshedAt: "2026-07-30T03:31:05Z"},
		"request-f": {Contacts: 6, Groups: 16, Members: 26, RefreshedAt: "2026-07-30T03:31:06Z"},
	}
	agent := &m101RefreshIsolationAgent{
		entered: make(chan string, len(expected)),
		release: make(chan struct{}),
		results: expected,
		seen:    make(map[string]int, len(expected)),
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)
	type refreshResult struct {
		requestID string
		result    WorkbenchToolResult
	}
	results := make(chan refreshResult, len(expected))
	for requestID := range expected {
		requestID := requestID
		go func() {
			ctx := context.WithValue(context.Background(), m101RefreshContextKey{}, requestID)
			results <- refreshResult{
				requestID: requestID,
				result: m101DispatchJSON(
					t,
					ctx,
					dispatcher,
					AutomationToolWorkbenchMutate,
					WorkbenchOperationContactsRefresh,
					`{}`,
				),
			}
		}()
	}

	entered := make(map[string]struct{}, len(expected))
	for range expected {
		select {
		case requestID := <-agent.entered:
			entered[requestID] = struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent refresh calls")
		}
	}
	if len(entered) != len(expected) {
		t.Fatalf("entered refresh requests = %#v", entered)
	}
	close(agent.release)

	for range expected {
		select {
		case got := <-results:
			if !got.result.OK {
				t.Fatalf("%s refresh result = %+v", got.requestID, got.result)
			}
			var counts AgentContactCounts
			if err := json.Unmarshal(got.result.Data, &counts); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(counts, expected[got.requestID]) {
				t.Fatalf("%s counts = %+v, want %+v", got.requestID, counts, expected[got.requestID])
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent refresh result")
		}
	}

	agent.mu.Lock()
	seen := make(map[string]int, len(agent.seen))
	for requestID, count := range agent.seen {
		seen[requestID] = count
	}
	agent.mu.Unlock()
	if len(seen) != len(expected) {
		t.Fatalf("refresh request identities = %#v", seen)
	}
	for requestID := range expected {
		if seen[requestID] != 1 {
			t.Fatalf("%s refresh calls = %d, want 1", requestID, seen[requestID])
		}
	}
	if agent.callCount("contacts.refresh") != len(expected) {
		t.Fatalf("contacts.refresh calls = %d, want %d", agent.callCount("contacts.refresh"), len(expected))
	}
	m101AssertNoFridaCalls(t, &agent.task003AgentClient)
}

func m101RawRequest(
	t *testing.T,
	operation WorkbenchOperation,
	argumentsJSON string,
) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: argumentsJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func m101Decode(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	argumentsJSON string,
) WorkbenchDecodedToolRequest {
	t.Helper()
	decoded, err := DecodeWorkbenchToolRequest(
		tool,
		m101RawRequest(t, operation, argumentsJSON),
	)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return decoded
}

func m101DispatchJSON(
	t *testing.T,
	ctx context.Context,
	dispatcher WorkbenchToolDispatcher,
	tool AutomationToolName,
	operation WorkbenchOperation,
	argumentsJSON string,
) WorkbenchToolResult {
	t.Helper()
	return dispatcher.Dispatch(
		ctx,
		WorkbenchExecutionContext{},
		m101Decode(t, tool, operation, argumentsJSON),
	)
}

func m101AssertExactJSON(t *testing.T, got any, want string) {
	t.Helper()
	gotData, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue any
	if err := json.Unmarshal(gotData, &gotValue); err != nil {
		t.Fatal(err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", gotData, strings.TrimSpace(want))
	}
}

func m101AssertFailure(
	t *testing.T,
	result WorkbenchToolResult,
	code WorkbenchErrorCode,
	retryable bool,
) {
	t.Helper()
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != code ||
		result.Error.Retryable != retryable ||
		len(result.Data) != 0 ||
		result.Page != nil {
		t.Fatalf("result = %+v, want failure %s retryable=%t", result, code, retryable)
	}
}

func m101AgentCalls(agent *task003AgentClient) []string {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return append([]string(nil), agent.calls...)
}

func m101AssertNoFridaCalls(t *testing.T, agent *task003AgentClient) {
	t.Helper()
	for _, call := range []string{
		"frida.status",
		"frida.initialize",
		"frida.rescan",
		"frida.detach",
	} {
		if count := agent.callCount(call); count != 0 {
			t.Fatalf("%s calls = %d, want 0", call, count)
		}
	}
}

func m101MaximumAgentFrameHTMLValue(t *testing.T, body func(string) any) string {
	t.Helper()
	low, high := 0, AgentMaxFrameBytes
	for low < high {
		middle := low + (high-low+1)/2
		value := strings.Repeat("<", middle)
		if m101AgentFrameSize(t, body(value)) <= AgentMaxFrameBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return strings.Repeat("<", low)
}

func m101AssertAgentFrameNearMaximum(t *testing.T, body any) {
	t.Helper()
	size := m101AgentFrameSize(t, body)
	if size > AgentMaxFrameBytes || size < AgentMaxFrameBytes-(8<<10) {
		t.Fatalf("agent frame size = %d, want [%d,%d]", size, AgentMaxFrameBytes-(8<<10), AgentMaxFrameBytes)
	}
}

func m101AgentFrameSize(t *testing.T, body any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(AgentResponse{
		Version: AgentProtocolVersion,
		ID:      "m101-max-frame",
		OK:      true,
		Body:    raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(frame)
}

func m101AssertBoundedStableResult(
	t *testing.T,
	first WorkbenchToolResult,
	second WorkbenchToolResult,
) {
	t.Helper()
	if !first.OK {
		t.Fatalf("first result = %+v", first)
	}
	if err := ValidateWorkbenchToolResult(first); err != nil {
		t.Fatalf("ValidateWorkbenchToolResult() error = %v", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstJSON) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("result size = %d, want <= %d", len(firstJSON), WorkbenchMaxResultJSONBytes)
	}
	if !reflect.DeepEqual(firstJSON, secondJSON) {
		t.Fatalf("stable replay mismatch\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func m101AssertTruncations(t *testing.T, truncations []workbenchContactsTruncation) {
	t.Helper()
	for _, truncation := range truncations {
		if truncation.Field == "" ||
			truncation.OriginalCharacters <= truncation.ReturnedCharacters ||
			truncation.ReturnedCharacters <= 0 {
			t.Fatalf("invalid truncation metadata: %+v", truncation)
		}
	}
}

func m101AssertCompleteGroupTraversal(t *testing.T, agent *task003AgentClient, total int) {
	t.Helper()
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)
	usernames := make([]string, 0, total)
	for offset := 0; ; {
		arguments := fmt.Sprintf(`{"limit":100,"offset":%d}`, offset)
		first := m101DispatchJSON(
			t,
			context.Background(),
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsList,
			arguments,
		)
		second := m101DispatchJSON(
			t,
			context.Background(),
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsList,
			arguments,
		)
		m101AssertBoundedStableResult(t, first, second)
		var payload struct {
			Groups []workbenchGroupProjection `json:"groups"`
		}
		if err := json.Unmarshal(first.Data, &payload); err != nil {
			t.Fatal(err)
		}
		m101AssertTraversalPage(t, first.Page, offset, total, len(payload.Groups))
		for _, group := range payload.Groups {
			usernames = append(usernames, group.Username)
			if len(group.Truncations) != 6 {
				t.Fatalf("group truncations = %#v, want six large fields", group.Truncations)
			}
			m101AssertTruncations(t, group.Truncations)
		}
		m101AssertGroupPageIsLargestPrefix(t, first, agent.groupsResult, payload.Groups)
		if !first.Page.HasMore {
			break
		}
		offset = first.Page.NextOffset
	}
	m101AssertOrderedUniqueValues(t, usernames, "group-%03d@chatroom")
}

func m101AssertCompleteMemberTraversal(t *testing.T, agent *task003AgentClient, total int) {
	t.Helper()
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil, agent)
	usernames := make([]string, 0, total)
	for offset := 0; ; {
		arguments := fmt.Sprintf(`{"group_id":"large@chatroom","limit":100,"offset":%d}`, offset)
		first := m101DispatchJSON(
			t,
			context.Background(),
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsMembers,
			arguments,
		)
		second := m101DispatchJSON(
			t,
			context.Background(),
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationGroupsMembers,
			arguments,
		)
		m101AssertBoundedStableResult(t, first, second)
		var payload struct {
			Members []workbenchGroupMemberProjection `json:"members"`
		}
		if err := json.Unmarshal(first.Data, &payload); err != nil {
			t.Fatal(err)
		}
		m101AssertTraversalPage(t, first.Page, offset, total, len(payload.Members))
		for _, member := range payload.Members {
			usernames = append(usernames, member.Username)
			if len(member.Truncations) != 7 {
				t.Fatalf("member truncations = %#v, want seven large fields", member.Truncations)
			}
			m101AssertTruncations(t, member.Truncations)
		}
		m101AssertMemberPageIsLargestPrefix(t, first, agent.groupMembersResult, payload.Members)
		if !first.Page.HasMore {
			break
		}
		offset = first.Page.NextOffset
	}
	m101AssertOrderedUniqueValues(t, usernames, "wxid_member_%03d")
}

func m101AssertTraversalPage(
	t *testing.T,
	page *WorkbenchPage,
	offset int,
	total int,
	returned int,
) {
	t.Helper()
	if page == nil ||
		page.Limit != WorkbenchMaxPageLimit ||
		page.Offset != offset ||
		page.Total != total ||
		page.Returned != returned ||
		returned <= 0 {
		t.Fatalf("page = %+v, returned records = %d", page, returned)
	}
	if page.HasMore {
		if page.NextOffset != offset+returned {
			t.Fatalf("next offset = %d, want %d", page.NextOffset, offset+returned)
		}
	} else if offset+returned != total || page.NextOffset != 0 {
		t.Fatalf("terminal page = %+v", page)
	}
}

func m101AssertGroupPageIsLargestPrefix(
	t *testing.T,
	result WorkbenchToolResult,
	values []Group,
	returned []workbenchGroupProjection,
) {
	t.Helper()
	if result.Page == nil || !result.Page.HasMore {
		return
	}
	sorted := append([]Group(nil), values...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Username < sorted[j].Username
	})
	candidate := append([]workbenchGroupProjection(nil), returned...)
	candidate = append(candidate, workbenchContactsProjectGroup(sorted[result.Page.NextOffset]))
	oversized := NewWorkbenchToolSuccess(
		WorkbenchOperationGroupsList,
		struct {
			Groups []workbenchGroupProjection `json:"groups"`
		}{Groups: candidate},
		newWorkbenchPage(
			result.Page.Limit,
			result.Page.Offset,
			result.Page.Total,
			len(candidate),
		),
	)
	if oversized.OK {
		t.Fatalf("groups page omitted a fitting next record: %+v", oversized.Page)
	}
}

func m101AssertMemberPageIsLargestPrefix(
	t *testing.T,
	result WorkbenchToolResult,
	values []GroupMember,
	returned []workbenchGroupMemberProjection,
) {
	t.Helper()
	if result.Page == nil || !result.Page.HasMore {
		return
	}
	sorted := append([]GroupMember(nil), values...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Username < sorted[j].Username
	})
	candidate := append([]workbenchGroupMemberProjection(nil), returned...)
	candidate = append(candidate, workbenchContactsProjectGroupMember(sorted[result.Page.NextOffset]))
	oversized := NewWorkbenchToolSuccess(
		WorkbenchOperationGroupsMembers,
		struct {
			Members []workbenchGroupMemberProjection `json:"members"`
		}{Members: candidate},
		newWorkbenchPage(
			result.Page.Limit,
			result.Page.Offset,
			result.Page.Total,
			len(candidate),
		),
	)
	if oversized.OK {
		t.Fatalf("members page omitted a fitting next record: %+v", oversized.Page)
	}
}

func m101AssertOrderedUniqueValues(t *testing.T, values []string, format string) {
	t.Helper()
	if len(values) == 0 {
		t.Fatal("traversal returned no values")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		want := fmt.Sprintf(format, index)
		if value != want {
			t.Fatalf("value[%d] = %q, want %q", index, value, want)
		}
		if _, exists := seen[value]; exists {
			t.Fatalf("duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
}
