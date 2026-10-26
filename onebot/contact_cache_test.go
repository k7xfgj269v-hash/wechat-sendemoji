package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestContactCacheBuildsSortedSnapshot(t *testing.T) {
	cache := NewContactCache(func() (ContactSnapshot, error) {
		return ContactSnapshot{
			Contacts: []Contact{
				{Username: "wxid_b", NickName: "Beta", SmallHeadURL: "small-b"},
				{Username: "wxid_a", Remark: "Alpha", BigHeadURL: "big-a", LocalType: 3},
			},
			Groups: []Group{
				{Username: "group_b@chatroom", MemberCount: 1},
				{Username: "group_a@chatroom", MemberCount: 1, SmallHeadURL: "group-a"},
			},
			Members: []GroupMember{
				{GroupID: "group_a@chatroom", Username: "wxid_b", NickName: "Beta"},
				{GroupID: "group_a@chatroom", Username: "wxid_a", Remark: "Alpha"},
			},
		}, nil
	})
	if err := cache.Refresh(); err != nil {
		t.Fatal(err)
	}

	total, contacts, loaded := cache.ListContacts("alpha", nil, 10, 0)
	if !loaded || total != 1 || len(contacts) != 1 {
		t.Fatalf("ListContacts = loaded:%v total:%d contacts:%v", loaded, total, contacts)
	}
	if contacts[0].Username != "wxid_a" || contacts[0].HeadImgURL != "big-a" {
		t.Fatalf("contact = %#v", contacts[0])
	}

	localType := 3
	total, contacts, _ = cache.ListContacts("", &localType, 1, 0)
	if total != 1 || len(contacts) != 1 || contacts[0].Username != "wxid_a" {
		t.Fatalf("local type filter = total:%d contacts:%v", total, contacts)
	}

	groups, loaded := cache.ListGroups()
	if !loaded || len(groups) != 2 || groups[0].Username != "group_a@chatroom" || groups[0].HeadImgURL != "group-a" {
		t.Fatalf("groups = %#v", groups)
	}

	members, loaded, found := cache.GroupMembers("group_a@chatroom")
	if !loaded || !found || len(members) != 2 {
		t.Fatalf("GroupMembers = loaded:%v found:%v members:%v", loaded, found, members)
	}
	if members[0].Username != "wxid_a" || members[0].DisplayName != "Alpha" {
		t.Fatalf("members = %#v", members)
	}
}

func TestContactCacheKeepsLastSnapshotOnRefreshFailure(t *testing.T) {
	calls := 0
	cache := NewContactCache(func() (ContactSnapshot, error) {
		calls++
		if calls == 1 {
			return ContactSnapshot{Contacts: []Contact{{Username: "wxid_ok"}}}, nil
		}
		return ContactSnapshot{}, errors.New("database unavailable")
	})
	if err := cache.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Refresh(); err == nil {
		t.Fatal("Refresh error = nil, want error")
	}

	contact, loaded, found := cache.ContactDetail("wxid_ok")
	if !loaded || !found || contact.Username != "wxid_ok" {
		t.Fatalf("ContactDetail = loaded:%v found:%v contact:%#v", loaded, found, contact)
	}
}

func TestContactCacheRefreshContextBoundsStuckLoaderWithoutQueueing(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})

	cache := NewContactCache(func() (ContactSnapshot, error) {
		switch calls.Add(1) {
		case 1:
			return ContactSnapshot{
				Contacts: []Contact{{Username: "wxid_old"}},
			}, nil
		case 2:
			close(started)
			<-release
			return ContactSnapshot{
				Contacts: []Contact{{Username: "wxid_new"}},
			}, nil
		default:
			return ContactSnapshot{}, errors.New("unexpected extra refresh")
		}
	})
	if err := cache.Refresh(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := cache.RefreshContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RefreshContext error = %v, want deadline exceeded", err)
	}
	<-started

	start := time.Now()
	err = cache.RefreshContext(context.Background())
	if !errors.Is(err, errContactCacheRefreshInProgress) {
		t.Fatalf("concurrent RefreshContext error = %v, want refresh in progress", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("concurrent refresh returned after %v, want immediate conflict", elapsed)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}

	contact, loaded, found := cache.ContactDetail("wxid_old")
	if !loaded || !found || contact.Username != "wxid_old" {
		t.Fatalf("last snapshot = loaded:%v found:%v contact:%#v", loaded, found, contact)
	}

	close(release)
	released = true
	deadline := time.Now().Add(time.Second)
	for {
		contact, loaded, found = cache.ContactDetail("wxid_new")
		if loaded && found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed refresh did not publish its snapshot")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestContactCacheConditionalRefreshDropsSupersededSnapshot(t *testing.T) {
	cache := NewContactCache(func() (ContactSnapshot, error) {
		return ContactSnapshot{
			Contacts: []Contact{{Username: "wxid_old"}},
		}, nil
	})
	if err := cache.Refresh(); err != nil {
		t.Fatal(err)
	}

	cache.loader = func() (ContactSnapshot, error) {
		return ContactSnapshot{
			Contacts: []Contact{{Username: "wxid_stale"}},
		}, nil
	}
	err := cache.RefreshContextIf(context.Background(), func() bool {
		return false
	})
	if !errors.Is(err, errContactCacheRefreshSuperseded) {
		t.Fatalf("conditional refresh error = %v, want superseded", err)
	}

	contact, loaded, found := cache.ContactDetail("wxid_old")
	if !loaded || !found || contact.Username != "wxid_old" {
		t.Fatalf("last valid snapshot = loaded:%v found:%v contact:%#v", loaded, found, contact)
	}
	if _, _, found := cache.ContactDetail("wxid_stale"); found {
		t.Fatal("superseded snapshot was published")
	}
}

func TestContactCacheWechatAgentAdapterPreservesRefreshDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	cache := NewContactCache(func() (ContactSnapshot, error) {
		close(started)
		<-release
		return ContactSnapshot{}, nil
	})
	contacts := NewContactCacheWechatAgentAdapter(cache)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := contacts.RefreshContacts(ctx)
	<-started
	var agentError *AgentError
	if !errors.As(err, &agentError) || agentError.Code != AgentErrorCodeDeadlineExceeded {
		t.Fatalf("RefreshContacts error = %v, want deadline_exceeded", err)
	}
}
