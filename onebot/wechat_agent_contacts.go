package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

type contactCacheWechatAgentAdapter struct {
	cache *ContactCache

	mu          sync.RWMutex
	refreshedAt time.Time
}

func NewContactCacheWechatAgentAdapter(cache *ContactCache) WechatAgentContacts {
	if cache == nil {
		return nil
	}
	return &contactCacheWechatAgentAdapter{cache: cache}
}

func (a *contactCacheWechatAgentAdapter) SearchContacts(
	ctx context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentContactSearchResult{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = 50
	}
	total, contacts, loaded := a.cache.ListContacts(
		request.Keyword,
		request.LocalType,
		limit,
		request.Offset,
	)
	if !loaded {
		return AgentContactSearchResult{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact cache is unavailable", Retryable: true,
		}
	}
	return AgentContactSearchResult{
		Total: total, Contacts: contacts, Limit: limit, Offset: request.Offset,
	}, nil
}

func (a *contactCacheWechatAgentAdapter) Contact(
	ctx context.Context,
	username string,
) (Contact, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return Contact{}, err
	}
	contact, loaded, found := a.cache.ContactDetail(username)
	if !loaded {
		return Contact{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact cache is unavailable", Retryable: true,
		}
	}
	if !found {
		return Contact{}, &AgentError{Code: AgentErrorCodeNotFound, Message: "contact was not found"}
	}
	return contact, nil
}

func (a *contactCacheWechatAgentAdapter) RefreshContacts(
	ctx context.Context,
) (AgentContactCounts, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentContactCounts{}, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, contactCacheRefreshTimeout)
	defer cancel()
	if err := a.cache.RefreshContext(refreshCtx); err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return AgentContactCounts{}, &AgentError{
				Code: AgentErrorCodeDeadlineExceeded, Message: "contact refresh deadline exceeded", Retryable: true,
			}
		case errors.Is(err, context.Canceled):
			return AgentContactCounts{}, err
		case errors.Is(err, errContactCacheRefreshInProgress):
			return AgentContactCounts{}, &AgentError{
				Code: AgentErrorCodeConflict, Message: "contact refresh is already in progress", Retryable: true,
			}
		}
		return AgentContactCounts{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact refresh failed", Retryable: true,
		}
	}
	a.mu.Lock()
	a.refreshedAt = time.Now().UTC()
	a.mu.Unlock()
	return a.Counts(ctx)
}

func (a *contactCacheWechatAgentAdapter) Groups(ctx context.Context) ([]Group, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return nil, err
	}
	groups, loaded := a.cache.ListGroups()
	if !loaded {
		return nil, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact cache is unavailable", Retryable: true,
		}
	}
	return groups, nil
}

func (a *contactCacheWechatAgentAdapter) GroupMembers(
	ctx context.Context,
	groupID string,
) ([]GroupMember, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return nil, err
	}
	members, loaded, found := a.cache.GroupMembers(groupID)
	if !loaded {
		return nil, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact cache is unavailable", Retryable: true,
		}
	}
	if !found {
		return nil, &AgentError{Code: AgentErrorCodeNotFound, Message: "group was not found"}
	}
	return members, nil
}

func (a *contactCacheWechatAgentAdapter) Counts(
	ctx context.Context,
) (AgentContactCounts, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentContactCounts{}, err
	}
	contacts, groups, loaded := a.cache.Counts()
	if !loaded {
		return AgentContactCounts{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "contact cache is unavailable", Retryable: true,
		}
	}
	groupValues, _ := a.cache.ListGroups()
	members := 0
	for _, group := range groupValues {
		members += group.MemberCount
	}
	a.mu.RLock()
	refreshedAt := a.refreshedAt
	a.mu.RUnlock()
	result := AgentContactCounts{Contacts: contacts, Groups: groups, Members: members}
	if !refreshedAt.IsZero() {
		result.RefreshedAt = refreshedAt.Format(time.RFC3339)
	}
	return result, nil
}
