package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
)

const (
	workbenchContactsFieldJSONBytes              = 3 << 10
	workbenchContactsInvalidAgentResponseMessage = "WeChat agent returned an invalid response"
	workbenchContactsIncompatibleAgentMessage    = "WeChat agent is incompatible"
)

type workbenchContactsTruncation struct {
	Field              string `json:"field"`
	OriginalCharacters int    `json:"original_characters"`
	ReturnedCharacters int    `json:"returned_characters"`
}

type workbenchContactProjection struct {
	Username     string                        `json:"username"`
	NickName     string                        `json:"nick_name"`
	Alias        string                        `json:"alias"`
	Remark       string                        `json:"remark"`
	LocalType    int                           `json:"local_type"`
	BigHeadURL   string                        `json:"big_head_url"`
	SmallHeadURL string                        `json:"small_head_url"`
	HeadImgURL   string                        `json:"head_img_url"`
	IsInChatRoom int                           `json:"is_in_chat_room"`
	Description  string                        `json:"description"`
	Truncations  []workbenchContactsTruncation `json:"truncations,omitempty"`
}

type workbenchContactRefreshProjection struct {
	Contacts    int                           `json:"contacts"`
	Groups      int                           `json:"groups"`
	Members     int                           `json:"members"`
	RefreshedAt string                        `json:"refreshed_at,omitempty"`
	Truncations []workbenchContactsTruncation `json:"truncations,omitempty"`
}

type workbenchGroupProjection struct {
	Username     string                        `json:"username"`
	NickName     string                        `json:"nick_name"`
	Remark       string                        `json:"remark"`
	Owner        string                        `json:"owner"`
	MemberCount  int                           `json:"member_count"`
	BigHeadURL   string                        `json:"big_head_url"`
	SmallHeadURL string                        `json:"small_head_url"`
	HeadImgURL   string                        `json:"head_img_url"`
	Truncations  []workbenchContactsTruncation `json:"truncations,omitempty"`
}

type workbenchGroupMemberProjection struct {
	Username     string                        `json:"username"`
	NickName     string                        `json:"nick_name"`
	Remark       string                        `json:"remark"`
	Alias        string                        `json:"alias"`
	LocalType    int                           `json:"local_type"`
	BigHeadURL   string                        `json:"big_head_url"`
	SmallHeadURL string                        `json:"small_head_url"`
	HeadImgURL   string                        `json:"head_img_url"`
	DisplayName  string                        `json:"display_name"`
	Truncations  []workbenchContactsTruncation `json:"truncations,omitempty"`
}

func init() {
	bindWorkbenchOperationHandlers("m1-contacts-groups",
		WorkbenchOperationBinding{Operation: WorkbenchOperationContactsGet, Handler: workbenchContactsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationContactsRefresh, Handler: workbenchContactsRefresh},
		WorkbenchOperationBinding{Operation: WorkbenchOperationGroupsList, Handler: workbenchGroupsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationGroupsMembers, Handler: workbenchGroupsMembers},
	)
}

func workbenchContactsGet(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationContactsGet
	if unavailable := workbenchContactsRequireAgent(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	contact, err := dispatcher.agent.Contact(ctx, raw.(*WorkbenchContactGetArguments).Username)
	if err != nil {
		return workbenchContactsAgentFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, workbenchContactsProjectContact(contact), nil)
}

func workbenchContactsRefresh(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	_ any,
) WorkbenchToolResult {
	operation := WorkbenchOperationContactsRefresh
	if unavailable := workbenchContactsRequireAgent(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	counts, err := dispatcher.agent.RefreshContacts(ctx)
	if err != nil {
		return workbenchContactsAgentFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, workbenchContactsProjectRefresh(counts), nil)
}

func workbenchGroupsList(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationGroupsList
	if unavailable := workbenchContactsRequireAgent(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	groups, err := dispatcher.agent.Groups(ctx)
	if err != nil {
		return workbenchContactsAgentFailure(operation, err)
	}
	groups = append([]Group(nil), groups...)
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].Username < groups[j].Username
	})
	return workbenchContactsBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		groups,
		workbenchContactsProjectGroup,
		func(values []workbenchGroupProjection) any {
			return struct {
				Groups []workbenchGroupProjection `json:"groups"`
			}{Groups: values}
		},
	)
}

func workbenchGroupsMembers(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationGroupsMembers
	if unavailable := workbenchContactsRequireAgent(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchGroupMembersArguments)
	members, err := dispatcher.agent.GroupMembers(ctx, arguments.GroupID)
	if err != nil {
		return workbenchContactsAgentFailure(operation, err)
	}
	members = append([]GroupMember(nil), members...)
	sort.SliceStable(members, func(i, j int) bool {
		return members[i].Username < members[j].Username
	})
	return workbenchContactsBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		members,
		workbenchContactsProjectGroupMember,
		func(values []workbenchGroupMemberProjection) any {
			return struct {
				Members []workbenchGroupMemberProjection `json:"members"`
			}{Members: values}
		},
	)
}

func workbenchContactsRequireAgent(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) *WorkbenchToolResult {
	if dispatcher != nil && !workbenchContactsAgentIsNil(dispatcher.agent) {
		return nil
	}
	result := NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorUnavailable,
		"WeChat agent is unavailable",
		true,
	)
	return &result
}

func workbenchContactsAgentIsNil(agent AgentClient) bool {
	if agent == nil {
		return true
	}
	value := reflect.ValueOf(agent)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func workbenchContactsAgentFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return workbenchAgentFailure(operation, err)
	}
	var agentErr *AgentError
	if errors.As(err, &agentErr) {
		switch agentErr.Code {
		case AgentErrorCodeInvalidFrame, AgentErrorCodeFrameTooLarge:
			return NewWorkbenchToolFailure(
				operation,
				WorkbenchErrorInternal,
				workbenchContactsInvalidAgentResponseMessage,
				false,
			)
		case AgentErrorCodeUnsupportedVersion, AgentErrorCodeUnsupportedOperation:
			return NewWorkbenchToolFailure(
				operation,
				WorkbenchErrorUnavailable,
				workbenchContactsIncompatibleAgentMessage,
				true,
			)
		}
	}
	return workbenchAgentFailure(operation, err)
}

func workbenchContactsProjectContact(contact Contact) workbenchContactProjection {
	var truncations []workbenchContactsTruncation
	return workbenchContactProjection{
		Username:     workbenchContactsProjectField("username", contact.Username, &truncations),
		NickName:     workbenchContactsProjectField("nick_name", contact.NickName, &truncations),
		Alias:        workbenchContactsProjectField("alias", contact.Alias, &truncations),
		Remark:       workbenchContactsProjectField("remark", contact.Remark, &truncations),
		LocalType:    contact.LocalType,
		BigHeadURL:   workbenchContactsProjectField("big_head_url", contact.BigHeadURL, &truncations),
		SmallHeadURL: workbenchContactsProjectField("small_head_url", contact.SmallHeadURL, &truncations),
		HeadImgURL:   workbenchContactsProjectField("head_img_url", contact.HeadImgURL, &truncations),
		IsInChatRoom: contact.IsInChatRoom,
		Description:  workbenchContactsProjectField("description", contact.Description, &truncations),
		Truncations:  truncations,
	}
}

func workbenchContactsProjectRefresh(counts AgentContactCounts) workbenchContactRefreshProjection {
	var truncations []workbenchContactsTruncation
	return workbenchContactRefreshProjection{
		Contacts:    counts.Contacts,
		Groups:      counts.Groups,
		Members:     counts.Members,
		RefreshedAt: workbenchContactsProjectField("refreshed_at", counts.RefreshedAt, &truncations),
		Truncations: truncations,
	}
}

func workbenchContactsProjectGroup(group Group) workbenchGroupProjection {
	var truncations []workbenchContactsTruncation
	return workbenchGroupProjection{
		Username:     workbenchContactsProjectField("username", group.Username, &truncations),
		NickName:     workbenchContactsProjectField("nick_name", group.NickName, &truncations),
		Remark:       workbenchContactsProjectField("remark", group.Remark, &truncations),
		Owner:        workbenchContactsProjectField("owner", group.Owner, &truncations),
		MemberCount:  group.MemberCount,
		BigHeadURL:   workbenchContactsProjectField("big_head_url", group.BigHeadURL, &truncations),
		SmallHeadURL: workbenchContactsProjectField("small_head_url", group.SmallHeadURL, &truncations),
		HeadImgURL:   workbenchContactsProjectField("head_img_url", group.HeadImgURL, &truncations),
		Truncations:  truncations,
	}
}

func workbenchContactsProjectGroupMember(member GroupMember) workbenchGroupMemberProjection {
	var truncations []workbenchContactsTruncation
	return workbenchGroupMemberProjection{
		Username:     workbenchContactsProjectField("username", member.Username, &truncations),
		NickName:     workbenchContactsProjectField("nick_name", member.NickName, &truncations),
		Remark:       workbenchContactsProjectField("remark", member.Remark, &truncations),
		Alias:        workbenchContactsProjectField("alias", member.Alias, &truncations),
		LocalType:    member.LocalType,
		BigHeadURL:   workbenchContactsProjectField("big_head_url", member.BigHeadURL, &truncations),
		SmallHeadURL: workbenchContactsProjectField("small_head_url", member.SmallHeadURL, &truncations),
		HeadImgURL:   workbenchContactsProjectField("head_img_url", member.HeadImgURL, &truncations),
		DisplayName:  workbenchContactsProjectField("display_name", member.DisplayName, &truncations),
		Truncations:  truncations,
	}
}

func workbenchContactsProjectField(
	field string,
	value string,
	truncations *[]workbenchContactsTruncation,
) string {
	encoded, _ := json.Marshal(value)
	if len(encoded) <= workbenchContactsFieldJSONBytes {
		return value
	}

	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		candidate, _ := json.Marshal(string(runes[:middle]))
		if len(candidate) <= workbenchContactsFieldJSONBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	*truncations = append(*truncations, workbenchContactsTruncation{
		Field:              field,
		OriginalCharacters: len(runes),
		ReturnedCharacters: low,
	})
	return string(runes[:low])
}

func workbenchContactsBoundedPage[S any, P any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	values []S,
	project func(S) P,
	data func([]P) any,
) WorkbenchToolResult {
	total := len(values)
	if offset >= len(values) {
		return NewWorkbenchToolSuccess(
			operation,
			data([]P{}),
			newWorkbenchPage(limit, offset, total, 0),
		)
	}
	end := offset + limit
	if end > total {
		end = total
	}

	accepted := make([]P, 0, end-offset)
	var result WorkbenchToolResult
	for index := offset; index < end; index++ {
		candidate := make([]P, len(accepted)+1)
		copy(candidate, accepted)
		candidate[len(accepted)] = project(values[index])
		candidateResult := NewWorkbenchToolSuccess(
			operation,
			data(candidate),
			newWorkbenchPage(limit, offset, total, len(candidate)),
		)
		if !candidateResult.OK {
			if len(accepted) == 0 {
				return NewWorkbenchToolFailure(
					operation,
					WorkbenchErrorInternal,
					"projected contact record could not fit in the result envelope",
					false,
				)
			}
			break
		}
		accepted = candidate
		result = candidateResult
	}
	return result
}
