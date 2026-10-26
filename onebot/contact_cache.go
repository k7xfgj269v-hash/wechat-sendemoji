package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxSnapshotContacts        = 100000
	maxSnapshotGroups          = 20000
	maxSnapshotMembers         = 500000
	contactCacheRefreshTimeout = 45 * time.Second
)

var errContactCacheRefreshInProgress = errors.New("联系人缓存正在刷新")
var errContactCacheRefreshSuperseded = errors.New("联系人缓存刷新结果已过期")

type contactCacheData struct {
	contacts           []Contact
	groups             []Group
	contactsByUsername map[string]Contact
	groupsByUsername   map[string]Group
	membersByGroup     map[string][]GroupMember
}

type ContactCache struct {
	refreshMu sync.Mutex
	refresh   *contactCacheRefresh
	mu        sync.RWMutex
	loader    ContactSnapshotLoader
	data      contactCacheData
	loaded    bool
}

type contactCacheRefresh struct {
	done          chan struct{}
	err           error
	shouldPublish func() bool
}

type contactDBRow struct {
	ID int64 `json:"id"`
	Contact
}

type groupDBRow struct {
	RoomID int64 `json:"room_id"`
	Group
}

type groupMemberDBRow struct {
	GroupID string `json:"group_id"`
	GroupMember
}

func NewContactCache(loader ContactSnapshotLoader) *ContactCache {
	return &ContactCache{
		loader: loader,
		data:   newContactCacheData(),
	}
}

func newContactCacheData() contactCacheData {
	return contactCacheData{
		contacts:           []Contact{},
		groups:             []Group{},
		contactsByUsername: make(map[string]Contact),
		groupsByUsername:   make(map[string]Group),
		membersByGroup:     make(map[string][]GroupMember),
	}
}

func loadContactSnapshot() (ContactSnapshot, error) {
	keyHex, err := loadContactKey()
	if err != nil {
		return ContactSnapshot{}, err
	}
	dbPath, err := findContactDB(keyHex)
	if err != nil {
		return ContactSnapshot{}, err
	}
	return loadContactSnapshotFrom(dbPath, keyHex)
}

func loadContactSnapshotFrom(dbPath, keyHex string) (ContactSnapshot, error) {
	db, err := openSQLCipherReadOnly(dbPath, keyHex)
	if err != nil {
		return ContactSnapshot{}, err
	}
	defer db.Close()

	contactRows, err := loadContactRows(db)
	if err != nil {
		return ContactSnapshot{}, fmt.Errorf("加载联系人失败: %w", err)
	}
	groupRows, err := loadGroupRows(db)
	if err != nil {
		return ContactSnapshot{}, fmt.Errorf("加载群聊失败: %w", err)
	}
	memberRows, err := loadGroupMemberRows(db)
	if err != nil {
		return ContactSnapshot{}, fmt.Errorf("加载群成员失败: %w", err)
	}

	contacts := make([]Contact, len(contactRows))
	for i := range contactRows {
		contacts[i] = contactRows[i].Contact
	}
	groups := make([]Group, len(groupRows))
	for i := range groupRows {
		groups[i] = groupRows[i].Group
	}
	members := make([]GroupMember, len(memberRows))
	for i := range memberRows {
		members[i] = memberRows[i].GroupMember
		members[i].GroupID = memberRows[i].GroupID
	}
	return ContactSnapshot{
		Contacts: contacts,
		Groups:   groups,
		Members:  members,
	}, nil
}

func loadContactRows(db *sql.DB) ([]contactDBRow, error) {
	rows, err := db.Query(`
SELECT id,
       COALESCE(username, '') AS username,
       COALESCE(nick_name, '') AS nick_name,
       COALESCE(alias, '') AS alias,
       COALESCE(remark, '') AS remark,
       COALESCE(local_type, 0) AS local_type,
       COALESCE(big_head_url, '') AS big_head_url,
       COALESCE(small_head_url, '') AS small_head_url,
       COALESCE(is_in_chat_room, 0) AS is_in_chat_room,
       COALESCE(description, '') AS description
FROM contact
ORDER BY username
LIMIT ?`, maxSnapshotContacts+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]contactDBRow, 0)
	for rows.Next() {
		var row contactDBRow
		if err := rows.Scan(
			&row.ID,
			&row.Username,
			&row.NickName,
			&row.Alias,
			&row.Remark,
			&row.LocalType,
			&row.BigHeadURL,
			&row.SmallHeadURL,
			&row.IsInChatRoom,
			&row.Description,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > maxSnapshotContacts {
		return nil, fmt.Errorf("联系人数量超过上限 %d", maxSnapshotContacts)
	}
	return result, nil
}

func loadGroupRows(db *sql.DB) ([]groupDBRow, error) {
	rows, err := db.Query(`
SELECT cr.id AS room_id,
       COALESCE(cr.username, '') AS username,
       COALESCE(c.nick_name, '') AS nick_name,
       COALESCE(c.remark, '') AS remark,
       COALESCE(cr.owner, '') AS owner,
       (SELECT COUNT(*) FROM chatroom_member cm WHERE cm.room_id = cr.id) AS member_count,
       COALESCE(c.big_head_url, '') AS big_head_url,
       COALESCE(c.small_head_url, '') AS small_head_url
FROM chat_room cr
LEFT JOIN contact c ON c.username = cr.username
ORDER BY cr.username
LIMIT ?`, maxSnapshotGroups+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]groupDBRow, 0)
	for rows.Next() {
		var row groupDBRow
		if err := rows.Scan(
			&row.RoomID,
			&row.Username,
			&row.NickName,
			&row.Remark,
			&row.Owner,
			&row.MemberCount,
			&row.BigHeadURL,
			&row.SmallHeadURL,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > maxSnapshotGroups {
		return nil, fmt.Errorf("群聊数量超过上限 %d", maxSnapshotGroups)
	}
	return result, nil
}

func loadGroupMemberRows(db *sql.DB) ([]groupMemberDBRow, error) {
	rows, err := db.Query(`
SELECT COALESCE(cr.username, '') AS group_id,
       COALESCE(c.username, '') AS username,
       COALESCE(c.nick_name, '') AS nick_name,
       COALESCE(c.remark, '') AS remark,
       COALESCE(c.alias, '') AS alias,
       COALESCE(c.local_type, 0) AS local_type,
       COALESCE(c.big_head_url, '') AS big_head_url,
       COALESCE(c.small_head_url, '') AS small_head_url
FROM chatroom_member cm
JOIN chat_room cr ON cr.id = cm.room_id
JOIN contact c ON c.id = cm.member_id
ORDER BY cr.username, c.username
LIMIT ?`, maxSnapshotMembers+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]groupMemberDBRow, 0)
	for rows.Next() {
		var row groupMemberDBRow
		if err := rows.Scan(
			&row.GroupID,
			&row.Username,
			&row.NickName,
			&row.Remark,
			&row.Alias,
			&row.LocalType,
			&row.BigHeadURL,
			&row.SmallHeadURL,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > maxSnapshotMembers {
		return nil, fmt.Errorf("群成员数量超过上限 %d", maxSnapshotMembers)
	}
	return result, nil
}

func (c *ContactCache) Refresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), contactCacheRefreshTimeout)
	defer cancel()
	return c.RefreshContext(ctx)
}

func (c *ContactCache) RefreshContext(ctx context.Context) error {
	return c.refreshContext(ctx, nil)
}

func (c *ContactCache) RefreshContextIf(
	ctx context.Context,
	shouldPublish func() bool,
) error {
	if shouldPublish == nil {
		return c.RefreshContext(ctx)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.refreshContext(ctx, func() bool {
		return ctx.Err() == nil && shouldPublish()
	})
}

func (c *ContactCache) refreshContext(
	ctx context.Context,
	shouldPublish func() bool,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("刷新联系人缓存: %w", err)
	}

	c.refreshMu.Lock()
	if c.refresh != nil {
		c.refreshMu.Unlock()
		return errContactCacheRefreshInProgress
	}
	if c.loader == nil {
		c.refreshMu.Unlock()
		return fmt.Errorf("联系人缓存 loader 未配置")
	}
	refresh := &contactCacheRefresh{
		done:          make(chan struct{}),
		shouldPublish: shouldPublish,
	}
	c.refresh = refresh
	go c.runRefresh(refresh)
	c.refreshMu.Unlock()

	select {
	case <-ctx.Done():
		c.refreshMu.Lock()
		completed := c.refresh != refresh
		err := refresh.err
		c.refreshMu.Unlock()
		if completed {
			return err
		}
		return fmt.Errorf("刷新联系人缓存: %w", ctx.Err())
	case <-refresh.done:
		return refresh.err
	}
}

func (c *ContactCache) runRefresh(refresh *contactCacheRefresh) {
	snapshot, err := c.loader()
	if err == nil && refresh.shouldPublish != nil && !refresh.shouldPublish() {
		err = errContactCacheRefreshSuperseded
	}
	if err == nil {
		data := buildContactCacheData(snapshot)

		c.mu.Lock()
		c.data = data
		c.loaded = true
		c.mu.Unlock()
	}

	c.refreshMu.Lock()
	refresh.err = err
	if c.refresh == refresh {
		c.refresh = nil
	}
	close(refresh.done)
	c.refreshMu.Unlock()
}

func buildContactCacheData(snapshot ContactSnapshot) contactCacheData {
	data := newContactCacheData()
	data.contacts = append(data.contacts, snapshot.Contacts...)
	for i := range data.contacts {
		setContactAvatar(&data.contacts[i])
		data.contactsByUsername[data.contacts[i].Username] = data.contacts[i]
	}
	sort.Slice(data.contacts, func(i, j int) bool {
		return data.contacts[i].Username < data.contacts[j].Username
	})

	data.groups = append(data.groups, snapshot.Groups...)
	for i := range data.groups {
		setGroupAvatar(&data.groups[i])
		data.groupsByUsername[data.groups[i].Username] = data.groups[i]
	}
	sort.Slice(data.groups, func(i, j int) bool {
		return data.groups[i].Username < data.groups[j].Username
	})

	for _, member := range snapshot.Members {
		setGroupMemberDisplay(&member)
		data.membersByGroup[member.GroupID] = append(data.membersByGroup[member.GroupID], member)
	}
	for groupID := range data.membersByGroup {
		sort.Slice(data.membersByGroup[groupID], func(i, j int) bool {
			return data.membersByGroup[groupID][i].Username < data.membersByGroup[groupID][j].Username
		})
	}
	return data
}

func setContactAvatar(contact *Contact) {
	contact.HeadImgURL = firstNonempty(contact.BigHeadURL, contact.SmallHeadURL)
}

func setGroupAvatar(group *Group) {
	group.HeadImgURL = firstNonempty(group.BigHeadURL, group.SmallHeadURL)
}

func setGroupMemberDisplay(member *GroupMember) {
	member.HeadImgURL = firstNonempty(member.BigHeadURL, member.SmallHeadURL)
	member.DisplayName = firstNonempty(member.Remark, member.NickName, member.Username)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (c *ContactCache) ListContacts(keyword string, localType *int, limit, offset int) (int, []Contact, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return 0, nil, false
	}

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	filtered := make([]Contact, 0, len(c.data.contacts))
	for _, contact := range c.data.contacts {
		if localType != nil && contact.LocalType != *localType {
			continue
		}
		if keyword != "" {
			searchable := strings.ToLower(strings.Join([]string{
				contact.Username,
				contact.NickName,
				contact.Remark,
				contact.Alias,
			}, "\x00"))
			if !strings.Contains(searchable, keyword) {
				continue
			}
		}
		filtered = append(filtered, contact)
	}

	total := len(filtered)
	if offset >= total {
		return total, []Contact{}, true
	}
	end := offset + limit
	if end > total {
		end = total
	}
	result := append([]Contact(nil), filtered[offset:end]...)
	return total, result, true
}

func (c *ContactCache) ListGroups() ([]Group, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return nil, false
	}
	result := make([]Group, len(c.data.groups))
	copy(result, c.data.groups)
	return result, true
}

func (c *ContactCache) GroupMembers(groupID string) ([]GroupMember, bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return nil, false, false
	}
	if _, ok := c.data.groupsByUsername[groupID]; !ok {
		return nil, true, false
	}
	members := c.data.membersByGroup[groupID]
	result := make([]GroupMember, len(members))
	copy(result, members)
	return result, true, true
}

func (c *ContactCache) ContactDetail(username string) (Contact, bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return Contact{}, false, false
	}
	contact, ok := c.data.contactsByUsername[username]
	return contact, true, ok
}

func (c *ContactCache) Counts() (int, int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return 0, 0, false
	}
	return len(c.data.contacts), len(c.data.groups), true
}
