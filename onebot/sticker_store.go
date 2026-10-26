package main

// 表情库：移植自 au_rabot src/features/stickers.js 的本地表情收藏能力。
//
// 磁盘布局（与 au_rabot 兼容，默认根目录 $HOME/.wechat-chatter/stickers）：
//
//	<root>/index.json                       索引（StickerLibrary）
//	<root>/collected-<md5>.<ext>            收到的表情（可被 Trim 淘汰）
//	<root>/manual-<md5>.<ext>               手工上传（永不淘汰）
//	<root>/config.json                      可选开关（缺省使用内置默认值）
//
// index.json 损坏时备份为 index.json.broken-<ts>，原文件保留，内存按空库处理。

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	stickerIndexFileName    = "index.json"
	stickerSettingsFileName = "config.json"

	stickerFromCollected = "collected"
	stickerFromManual    = "manual"

	stickerMaxSide        = 400            // 单边像素上限
	stickerMaxBytes       = 3 * 512 * 1024 // 1.5 MiB
	stickerMaxAspectRatio = 3.0            // 长短边之比上限
	stickerDefaultMaxHour = 20             // 每小时收集上限
	stickerDefaultMaxItem = 300            // 库容量上限（超出淘汰 collected）
	stickerCollectWindow  = time.Hour      // 收集限额的滚动窗口
	stickerCollectMaxRead = 16 << 20       // 读回收集源文件的大小上限（防呆）
	stickerTrimGuard      = 64             // Trim 单次最多淘汰条数（防死循环）
	stickerMatchMinScore  = 1.5            // 模糊匹配命中阈值（与 au_rabot 一致）
	stickerNameMaxRunes   = 12             // 名字长度上限
	stickerTagMaxCount    = 5              // 标签个数上限
	stickerTagMaxRunes    = 12             // 单个标签长度上限
	stickerSummaryMaxRune = 60             // 摘要长度上限
	stickerMD5HexLength   = 32             // md5 的十六进制长度
)

var (
	// errStickerNotFound 表示库里没有这张表情（删除/改名/标记使用时返回）。
	errStickerNotFound = errors.New("sticker not found")
	// errStickerEmpty 表示传入的表情字节为空。
	errStickerEmpty = errors.New("sticker content is empty")
)

// StickerItem 是表情库里的一条记录，JSON 字段与 au_rabot 的 index.json 兼容。
type StickerItem struct {
	MD5      string   `json:"md5"`
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	From     string   `json:"from"`
	At       int64    `json:"at"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Bytes    int      `json:"bytes"`
	Uses     int      `json:"uses"`
	LastUsed int64    `json:"lastUsed"`
	Summary  string   `json:"summary"`
}

// StickerLibrary 是 index.json 的顶层结构。
type StickerLibrary struct {
	Items []StickerItem `json:"items"`
}

// StickerMeta 是新增表情时的元信息。From 只有 "collected" / "manual" 两种归一口径。
type StickerMeta struct {
	From    string
	Name    string
	Tags    []string
	Summary string
}

// StickerSettings 是表情库开关。默认 enabled=true、collect=true、dry_run=false
// （onebot 侧没有全局 dry-run 开关）；可从 <root>/config.json 覆盖。
type StickerSettings struct {
	Enabled    bool `json:"enabled"`
	Collect    bool `json:"collect"`
	DryRun     bool `json:"dry_run"`
	MaxPerHour int  `json:"max_per_hour"`
	MaxItems   int  `json:"max_items"`
}

// ReceivedEmoji 是 worker.go face 分支交给表情库的入站表情。
// Path 是 DetectAndSaveImage 落盘的本地文件；MD5/Summary 来自表情 XML（可能为空）。
type ReceivedEmoji struct {
	Path    string
	MD5     string
	Summary string
}

// StickerStore 是表情库的内存索引 + 磁盘目录。所有导出方法都用 mu 保护。
type StickerStore struct {
	root         string
	indexPath    string
	settingsPath string

	mu       sync.Mutex
	loaded   bool
	items    []StickerItem
	settings StickerSettings
	recent   []time.Time // 收集时间戳，用于每小时限额
}

// DefaultStickerDataDir 返回默认表情库目录 $HOME/.wechat-chatter/stickers。
func DefaultStickerDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".wechat-chatter", "stickers"), nil
}

// newStickerStore 构造表情库句柄（懒加载：第一次访问才读 index.json）。
func newStickerStore(root string) *StickerStore {
	root = strings.TrimSpace(root)
	if root == "" {
		if dir, err := DefaultStickerDataDir(); err == nil {
			root = dir
		}
	}
	if root != "" {
		root = filepath.Clean(root)
	}
	return &StickerStore{
		root:         root,
		indexPath:    filepath.Join(root, stickerIndexFileName),
		settingsPath: filepath.Join(root, stickerSettingsFileName),
		settings:     defaultStickerSettings(),
	}
}

var (
	defaultStickerStoreMu    sync.Mutex
	defaultStickerStoreValue *StickerStore
)

// defaultStickerStore 返回进程级默认表情库（目录无法解析时返回 nil）。
func defaultStickerStore() *StickerStore {
	defaultStickerStoreMu.Lock()
	defer defaultStickerStoreMu.Unlock()
	if defaultStickerStoreValue != nil {
		return defaultStickerStoreValue
	}
	root, err := DefaultStickerDataDir()
	if err != nil {
		Warn("表情库目录不可用", "err", err)
		return nil
	}
	defaultStickerStoreValue = newStickerStore(root)
	return defaultStickerStoreValue
}

// setDefaultStickerStore 覆盖进程级默认表情库（测试/嵌入用；传 nil 表示清空）。
func setDefaultStickerStore(store *StickerStore) {
	defaultStickerStoreMu.Lock()
	defaultStickerStoreValue = store
	defaultStickerStoreMu.Unlock()
}

// Root 返回表情库目录。
func (s *StickerStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Settings 返回当前开关（拷贝）。
func (s *StickerStore) Settings() StickerSettings {
	if s == nil {
		return defaultStickerSettings()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	return s.settings
}

// Items 返回表情列表的深拷贝。
func (s *StickerStore) Items() []StickerItem {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	items := make([]StickerItem, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, cloneStickerItem(item))
	}
	return items
}

// Get 按 md5 取一条记录。
func (s *StickerStore) Get(digest string) (StickerItem, bool) {
	if s == nil {
		return StickerItem{}, false
	}
	normalized := normalizeStickerMD5(digest)
	if normalized == "" {
		return StickerItem{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	index := s.indexOfLocked(normalized)
	if index < 0 {
		return StickerItem{}, false
	}
	return cloneStickerItem(s.items[index]), true
}

// Has 判断库里是否已有该 md5。
func (s *StickerStore) Has(digest string) bool {
	_, ok := s.Get(digest)
	return ok
}

// ImagePath 返回某张表情的本地图片路径。
func (s *StickerStore) ImagePath(digest string) (string, error) {
	if s == nil {
		return "", errStickerNotFound
	}
	normalized := normalizeStickerMD5(digest)
	if normalized == "" {
		return "", errStickerNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	index := s.indexOfLocked(normalized)
	if index < 0 {
		return "", errStickerNotFound
	}
	return s.filePathLocked(s.items[index].File)
}

// Add 写入一张表情；md5 已存在时返回 (现有条目, true, nil)。
func (s *StickerStore) Add(buf []byte, meta StickerMeta) (StickerItem, bool, error) {
	return s.addBytes(buf, meta, "")
}

// addBytes 是 Add 的内部实现；sourcePath 非空时优先硬链接，避免重复落盘。
func (s *StickerStore) addBytes(buf []byte, meta StickerMeta, sourcePath string) (StickerItem, bool, error) {
	if s == nil {
		return StickerItem{}, false, errors.New("sticker store is nil")
	}
	if len(buf) == 0 {
		return StickerItem{}, false, errStickerEmpty
	}
	if s.root == "" {
		return StickerItem{}, false, errors.New("sticker root directory is empty")
	}
	sum := md5.Sum(buf)
	digest := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()

	if index := s.indexOfLocked(digest); index >= 0 {
		return cloneStickerItem(s.items[index]), true, nil
	}

	width, height := stickerImageSize(buf)
	from := normalizeStickerFrom(meta.From)
	file := fmt.Sprintf("%s-%s.%s", from, digest, stickerImageExt(buf))
	target, err := s.filePathLocked(file)
	if err != nil {
		return StickerItem{}, false, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return StickerItem{}, false, fmt.Errorf("create sticker directory: %w", err)
	}
	if err := writeStickerFile(target, buf, sourcePath); err != nil {
		return StickerItem{}, false, err
	}

	item := StickerItem{
		MD5:     digest,
		File:    file,
		Name:    clampStickerName(meta.Name),
		Tags:    normalizeStickerTags(meta.Tags),
		From:    from,
		At:      time.Now().UnixMilli(),
		Width:   width,
		Height:  height,
		Bytes:   len(buf),
		Summary: normalizeStickerSummary(meta.Summary),
	}
	s.items = append(s.items, item)
	if err := s.saveLocked(); err != nil {
		s.items = s.items[:len(s.items)-1]
		_ = os.Remove(target)
		return StickerItem{}, false, err
	}
	return cloneStickerItem(item), false, nil
}

// Remove 删除一张表情（索引 + 文件）。
func (s *StickerStore) Remove(digest string) error {
	if s == nil {
		return errStickerNotFound
	}
	normalized := normalizeStickerMD5(digest)
	if normalized == "" {
		return errStickerNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	index := s.indexOfLocked(normalized)
	if index < 0 {
		return errStickerNotFound
	}
	return s.removeIndexLocked(index)
}

// Rename 改写名字与标签；name 截断到 12 个字，tags 最多保留 5 个。
func (s *StickerStore) Rename(digest, name string, tags []string) error {
	if s == nil {
		return errStickerNotFound
	}
	normalized := normalizeStickerMD5(digest)
	if normalized == "" {
		return errStickerNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	index := s.indexOfLocked(normalized)
	if index < 0 {
		return errStickerNotFound
	}
	previous := s.items[index]
	updated := previous
	updated.Name = clampStickerName(name)
	updated.Tags = normalizeStickerTags(tags)
	s.items[index] = updated
	if err := s.saveLocked(); err != nil {
		s.items[index] = previous
		return err
	}
	return nil
}

// Match 按名字/标签模糊匹配：完全等于 10、包含 6、字符重合 ≥2 时按比例给分。
func (s *StickerStore) Match(query string) (StickerItem, bool) {
	if s == nil {
		return StickerItem{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	return matchStickerItems(s.items, query)
}

// PickForReply 优先按回复文本里的标签命中，未命中随机挑一张。
func (s *StickerStore) PickForReply(text string) (StickerItem, bool) {
	if s == nil {
		return StickerItem{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	if len(s.items) == 0 {
		return StickerItem{}, false
	}
	for _, tag := range stickerCandidateTags(s.items) {
		if tag == "" || !strings.Contains(text, tag) {
			continue
		}
		if item, ok := matchStickerItems(s.items, tag); ok {
			return item, true
		}
	}
	return cloneStickerItem(s.items[rand.Intn(len(s.items))]), true
}

// MarkUsed 记录一次发送（uses++ / lastUsed）。
func (s *StickerStore) MarkUsed(digest string) error {
	if s == nil {
		return errStickerNotFound
	}
	normalized := normalizeStickerMD5(digest)
	if normalized == "" {
		return errStickerNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	index := s.indexOfLocked(normalized)
	if index < 0 {
		return errStickerNotFound
	}
	previous := s.items[index]
	updated := previous
	updated.Uses++
	updated.LastUsed = time.Now().UnixMilli()
	s.items[index] = updated
	if err := s.saveLocked(); err != nil {
		s.items[index] = previous
		return err
	}
	return nil
}

// Trim 把库压到 maxItems 以内：只淘汰 from=collected，按 uses 最少、最老优先。
// 返回被淘汰的 md5 列表。maxItems<=0 时使用设置里的上限（默认 300）。
func (s *StickerStore) Trim(maxItems int) ([]string, error) {
	if s == nil {
		return nil, errors.New("sticker store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()

	limit := maxItems
	if limit <= 0 {
		limit = s.settings.MaxItems
	}
	if limit <= 0 {
		limit = stickerDefaultMaxItem
	}
	if len(s.items) <= limit {
		return nil, nil
	}

	previous := s.items // removeAt 每次都新建切片，原数组保持可回滚
	var evicted []StickerItem
	for guard := 0; guard < stickerTrimGuard && len(s.items) > limit; guard++ {
		index := stickerEvictIndex(s.items)
		if index < 0 {
			break
		}
		evicted = append(evicted, s.items[index])
		s.items = removeStickerAt(s.items, index)
	}
	if len(evicted) == 0 {
		return nil, nil
	}
	if err := s.saveLocked(); err != nil {
		s.items = previous
		return nil, err
	}
	removed := make([]string, 0, len(evicted))
	for _, item := range evicted {
		s.deleteFileLocked(item)
		removed = append(removed, item.MD5)
	}
	return removed, nil
}

// reserveCollectSlot 占用一个每小时名额；窗口外的时间戳会被清掉。
func (s *StickerStore) reserveCollectSlot(now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	limit := s.settings.MaxPerHour
	if limit <= 0 {
		limit = stickerDefaultMaxHour
	}
	cutoff := now.Add(-stickerCollectWindow)
	kept := s.recent[:0]
	for _, at := range s.recent {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	s.recent = kept
	if len(s.recent) >= limit {
		return false
	}
	s.recent = append(s.recent, now)
	return true
}

// collectReceivedSticker 把 face 分支落盘的表情收进表情库。
// 跳过的情形（开关关闭、尺寸不合、重复、超额）返回 nil，真正的 IO 错误才返回 error。
func collectReceivedSticker(store *StickerStore, emoji ReceivedEmoji) error {
	if store == nil {
		return errors.New("sticker store is unavailable")
	}
	settings := store.Settings()
	if !settings.Enabled || !settings.Collect {
		return nil
	}

	path := strings.TrimSpace(emoji.Path)
	if path == "" {
		return errors.New("received sticker path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat received sticker: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("received sticker is not a regular file: %s", filepath.Base(path))
	}
	if info.Size() > stickerCollectMaxRead {
		return fmt.Errorf("received sticker is too large: %d bytes", info.Size())
	}

	buf, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read received sticker: %w", err)
	}
	if len(buf) == 0 {
		return errStickerEmpty
	}

	width, height := stickerImageSize(buf)
	if !stickerSizeOK(width, height, len(buf)) {
		Debug("收到的表情尺寸不像表情，跳过",
			"width", width, "height", height, "bytes", len(buf))
		return nil
	}

	sum := md5.Sum(buf)
	digest := hex.EncodeToString(sum[:])
	// 消息自带的 md5 与本地算的一致时优先用自带的（微信侧同一个值，便于对照）。
	if provided := strings.TrimSpace(emoji.MD5); provided != "" && strings.EqualFold(provided, digest) {
		digest = strings.ToLower(provided)
	}
	if store.Has(digest) {
		return nil
	}
	if !store.reserveCollectSlot(time.Now()) {
		Debug("这小时收够了", "md5", digest)
		return nil
	}

	item, existed, err := store.addBytes(buf, StickerMeta{
		From:    stickerFromCollected,
		Summary: emoji.Summary,
	}, path)
	if err != nil {
		return err
	}
	if existed {
		return nil
	}
	if _, err := store.Trim(settings.MaxItems); err != nil {
		Warn("表情库淘汰失败", "err", err)
	}
	Info("收了一张表情入库",
		"md5", item.MD5, "width", item.Width, "height", item.Height, "bytes", item.Bytes)
	return nil
}

// stickerSizeOK 是表情的尺寸门：体积 ≤1.5MiB、单边 ≤400、长短边比 <3。
// 读不出宽高时只看体积（与 au_rabot 一致）。
func stickerSizeOK(width, height, bytes int) bool {
	if bytes <= 0 || bytes > stickerMaxBytes {
		return false
	}
	if width <= 0 || height <= 0 {
		return true
	}
	longSide, shortSide := width, height
	if height > width {
		longSide, shortSide = height, width
	}
	if longSide > stickerMaxSide {
		return false
	}
	if shortSide <= 0 {
		return true
	}
	return float64(longSide)/float64(shortSide) < stickerMaxAspectRatio
}

// stickerImageSize 手写解析 png/gif/jpeg/webp 头拿宽高，读不出返回 (0, 0)。
func stickerImageSize(buf []byte) (int, int) {
	size := len(buf)
	switch {
	case size >= 24 && buf[0] == 0x89 && buf[1] == 0x50:
		return stickerPositiveSize(
			int(binary.BigEndian.Uint32(buf[16:20])),
			int(binary.BigEndian.Uint32(buf[20:24])),
		)
	case size >= 10 && buf[0] == 'G' && buf[1] == 'I' && buf[2] == 'F':
		return stickerPositiveSize(
			int(binary.LittleEndian.Uint16(buf[6:8])),
			int(binary.LittleEndian.Uint16(buf[8:10])),
		)
	case size >= 16 && stickerHasRIFF(buf):
		switch string(buf[12:16]) {
		case "VP8X":
			if size < 30 {
				return 0, 0
			}
			return stickerPositiveSize(
				1+stickerUintLE(buf[24:27]),
				1+stickerUintLE(buf[27:30]),
			)
		case "VP8 ":
			if size < 30 {
				return 0, 0
			}
			return stickerPositiveSize(
				int(binary.LittleEndian.Uint16(buf[26:28]))&0x3fff,
				int(binary.LittleEndian.Uint16(buf[28:30]))&0x3fff,
			)
		case "VP8L":
			if size < 25 {
				return 0, 0
			}
			bits := binary.LittleEndian.Uint32(buf[21:25])
			return stickerPositiveSize(
				int(bits&0x3fff)+1,
				int((bits>>14)&0x3fff)+1,
			)
		}
	case size >= 4 && buf[0] == 0xff && buf[1] == 0xd8:
		return stickerJPEGSize(buf)
	}
	return 0, 0
}

func stickerHasRIFF(buf []byte) bool {
	if len(buf) < 12 {
		return false
	}
	return string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WEBP"
}

func stickerPositiveSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	return width, height
}

func stickerUintLE(buf []byte) int {
	value := 0
	for i := len(buf) - 1; i >= 0; i-- {
		value = value<<8 | int(buf[i])
	}
	return value
}

// stickerJPEGSize 扫描 JPEG 段找 SOFn（跳过 0xc4/0xc8/0xcc 的 DHT/JPG/DAC）。
func stickerJPEGSize(buf []byte) (int, int) {
	offset := 2
	for offset+9 < len(buf) {
		if buf[offset] != 0xff {
			offset++
			continue
		}
		marker := buf[offset+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			offset += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(buf[offset+2 : offset+4]))
		if length < 2 {
			break
		}
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			height := int(binary.BigEndian.Uint16(buf[offset+5 : offset+7]))
			width := int(binary.BigEndian.Uint16(buf[offset+7 : offset+9]))
			return stickerPositiveSize(width, height)
		}
		offset += 2 + length
	}
	return 0, 0
}

// stickerImageExt 从字节判断扩展名；认不出按 jpg 处理（与 au_rabot 的 ext() 一致）。
func stickerImageExt(buf []byte) string {
	switch {
	case len(buf) >= 2 && buf[0] == 0x89 && buf[1] == 0x50:
		return "png"
	case len(buf) >= 2 && buf[0] == 'G' && buf[1] == 'I':
		return "gif"
	case len(buf) >= 2 && buf[0] == 0xff && buf[1] == 0xd8:
		return "jpg"
	case stickerHasRIFF(buf):
		return "webp"
	}
	return "jpg"
}

// stickerImageKnown 判断字节是不是支持的表情格式（上传入口用）。
func stickerImageKnown(buf []byte) bool {
	switch {
	case len(buf) >= 24 && buf[0] == 0x89 && buf[1] == 0x50:
		return true
	case len(buf) >= 10 && buf[0] == 'G' && buf[1] == 'I' && buf[2] == 'F':
		return true
	case len(buf) >= 4 && buf[0] == 0xff && buf[1] == 0xd8:
		return true
	case stickerHasRIFF(buf):
		return true
	}
	return false
}

func matchStickerItems(items []StickerItem, query string) (StickerItem, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || len(items) == 0 {
		return StickerItem{}, false
	}
	best := -1
	bestScore := 0.0
	for index := range items {
		score := stickerMatchScore(items[index], trimmed)
		if score > bestScore {
			best = index
			bestScore = score
		}
	}
	if best < 0 || bestScore < stickerMatchMinScore {
		return StickerItem{}, false
	}
	return cloneStickerItem(items[best]), true
}

func stickerMatchScore(item StickerItem, query string) float64 {
	haystacks := make([]string, 0, len(item.Tags)+1)
	if item.Name != "" {
		haystacks = append(haystacks, item.Name)
	}
	haystacks = append(haystacks, item.Tags...)

	queryRunes := []rune(query)
	queryLength := len(queryRunes)
	best := 0.0
	for _, hay := range haystacks {
		if hay == "" {
			continue
		}
		if hay == query {
			if best < 10 {
				best = 10
			}
			continue
		}
		if strings.Contains(hay, query) || strings.Contains(query, hay) {
			if best < 6 {
				best = 6
			}
			continue
		}
		common := 0
		for _, char := range queryRunes {
			if strings.ContainsRune(hay, char) {
				common++
			}
		}
		if common >= 2 && queryLength > 0 {
			score := float64(common) / float64(queryLength) * 4
			if score > best {
				best = score
			}
		}
	}
	return best
}

func stickerCandidateTags(items []StickerItem) []string {
	seen := make(map[string]struct{}, len(items)*2+len(stickerDefaultTags))
	tags := make([]string, 0, len(items)*2+len(stickerDefaultTags))
	appendTag := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	for _, tag := range stickerDefaultTags {
		appendTag(tag)
	}
	for _, item := range items {
		for _, tag := range item.Tags {
			appendTag(tag)
		}
	}
	return tags
}

// stickerDefaultTags 是自动配表情时优先扫描的通用情绪词。
var stickerDefaultTags = []string{"开心", "卖萌", "生气", "委屈", "无语", "嘲讽", "疑惑", "早晚安"}

// stickerEvictIndex 选出该被淘汰的一条：只看 collected，uses 最少、最老优先。
func stickerEvictIndex(items []StickerItem) int {
	best := -1
	for index := range items {
		if items[index].From != stickerFromCollected {
			continue
		}
		if best < 0 || stickerOlder(items[index], items[best]) {
			best = index
		}
	}
	return best
}

func stickerOlder(left, right StickerItem) bool {
	if left.Uses != right.Uses {
		return left.Uses < right.Uses
	}
	leftAt := left.LastUsed
	if leftAt == 0 {
		leftAt = left.At
	}
	rightAt := right.LastUsed
	if rightAt == 0 {
		rightAt = right.At
	}
	if leftAt != rightAt {
		return leftAt < rightAt
	}
	return left.MD5 < right.MD5
}

func (s *StickerStore) removeIndexLocked(index int) error {
	previous := s.items
	item := s.items[index]
	s.items = removeStickerAt(s.items, index)
	if err := s.saveLocked(); err != nil {
		s.items = previous
		return err
	}
	s.deleteFileLocked(item)
	return nil
}

func removeStickerAt(items []StickerItem, index int) []StickerItem {
	next := make([]StickerItem, 0, len(items)-1)
	next = append(next, items[:index]...)
	next = append(next, items[index+1:]...)
	return next
}

// deleteFileLocked 删除条目对应的图片文件（尽力而为，失败只记日志）。
func (s *StickerStore) deleteFileLocked(item StickerItem) {
	path, err := s.filePathLocked(item.File)
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		Warn("删除表情文件失败", "file", filepath.Base(path), "err", err)
	}
}

// filePathLocked 把条目里的文件名解析成库内绝对路径，拒绝路径穿越。
func (s *StickerStore) filePathLocked(file string) (string, error) {
	if s.root == "" {
		return "", errors.New("sticker root directory is empty")
	}
	name := filepath.Base(strings.TrimSpace(file))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", errStickerNotFound
	}
	return filepath.Join(s.root, name), nil
}

func (s *StickerStore) indexOfLocked(digest string) int {
	for index := range s.items {
		if s.items[index].MD5 == digest {
			return index
		}
	}
	return -1
}

// ensureLoadedLocked 懒加载索引与设置（调用方必须持有 mu）。
func (s *StickerStore) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.settings = s.loadSettingsLocked()
	s.items = []StickerItem{}

	data, err := os.ReadFile(s.indexPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			Warn("读取表情库索引失败，按空库处理", "err", err)
		}
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	var library StickerLibrary
	if err := json.Unmarshal(data, &library); err != nil {
		backup := s.backupBrokenIndexLocked(data)
		Warn("表情库索引损坏，已备份并当空库处理", "err", err, "backup", backup)
		return
	}
	items := make([]StickerItem, 0, len(library.Items))
	for _, item := range library.Items {
		normalized, ok := normalizeLoadedStickerItem(item)
		if !ok {
			continue
		}
		items = append(items, normalized)
	}
	s.items = items
}

// backupBrokenIndexLocked 把损坏的索引内容另存为 index.json.broken-<ts>（原文件不动）。
func (s *StickerStore) backupBrokenIndexLocked(data []byte) string {
	stamp := time.Now().UTC().Format("20060102-150405.000000000")
	path := fmt.Sprintf("%s.broken-%s", s.indexPath, stamp)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		Warn("备份损坏的表情库索引失败", "err", err)
		return ""
	}
	return path
}

func (s *StickerStore) loadSettingsLocked() StickerSettings {
	settings := defaultStickerSettings()
	data, err := os.ReadFile(s.settingsPath)
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		Warn("表情库配置无法解析，使用默认开关", "err", err)
		return defaultStickerSettings()
	}
	return normalizeStickerSettings(settings)
}

func (s *StickerStore) saveLocked() error {
	if s.root == "" {
		return errors.New("sticker root directory is empty")
	}
	library := StickerLibrary{Items: s.items}
	if library.Items == nil {
		library.Items = []StickerItem{}
	}
	data, err := json.MarshalIndent(library, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sticker index: %w", err)
	}
	data = append(data, '\n')
	return writeStickerIndexAtomic(s.indexPath, data)
}

// writeStickerIndexAtomic 原子写索引：临时文件 → fsync → rename → fsync 目录。
func writeStickerIndexAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create sticker directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary sticker index: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary sticker index permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary sticker index: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary sticker index: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary sticker index: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace sticker index: %w", err)
	}
	cleanup = false
	syncStickerDirectory(directory)
	return nil
}

func syncStickerDirectory(directory string) {
	handle, err := os.Open(directory)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// writeStickerFile 把表情字节落到库里。sourcePath 可用时优先硬链接：
// 收集路径的源文件仍被入站消息的 file:// 引用（forward.go 会去读），不能 move 走，
// 硬链接既不复制数据块、也不破坏入站链路；文件系统不支持硬链接时才退回复制。
func writeStickerFile(path string, buf []byte, sourcePath string) error {
	if source := strings.TrimSpace(sourcePath); source != "" {
		if err := os.Link(source, path); err == nil {
			return nil
		}
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return fmt.Errorf("write sticker file: %w", err)
	}
	return nil
}

func defaultStickerSettings() StickerSettings {
	return StickerSettings{
		Enabled:    true,
		Collect:    true,
		DryRun:     false,
		MaxPerHour: stickerDefaultMaxHour,
		MaxItems:   stickerDefaultMaxItem,
	}
}

func normalizeStickerSettings(settings StickerSettings) StickerSettings {
	if settings.MaxPerHour <= 0 {
		settings.MaxPerHour = stickerDefaultMaxHour
	}
	if settings.MaxItems <= 0 {
		settings.MaxItems = stickerDefaultMaxItem
	}
	return settings
}

func normalizeLoadedStickerItem(item StickerItem) (StickerItem, bool) {
	digest := normalizeStickerMD5(item.MD5)
	if digest == "" {
		return StickerItem{}, false
	}
	item.MD5 = digest
	item.File = filepath.Base(strings.TrimSpace(item.File))
	if item.File == "" || item.File == "." {
		return StickerItem{}, false
	}
	item.Name = clampStickerName(item.Name)
	item.Tags = normalizeStickerTags(item.Tags)
	item.From = normalizeStickerFrom(item.From)
	item.Summary = normalizeStickerSummary(item.Summary)
	if item.Bytes < 0 {
		item.Bytes = 0
	}
	if item.Uses < 0 {
		item.Uses = 0
	}
	return item, true
}

func normalizeStickerFrom(from string) string {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case stickerFromCollected:
		return stickerFromCollected
	case stickerFromManual:
		return stickerFromManual
	case "":
		return stickerFromManual
	default:
		return stickerFromManual
	}
}

// normalizeStickerMD5 只接受 32 位十六进制，其余返回空串。
func normalizeStickerMD5(digest string) string {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if len(digest) != stickerMD5HexLength {
		return ""
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return ""
	}
	return digest
}

func clampStickerName(name string) string {
	trimmed := strings.TrimSpace(strings.Trim(name, "「」\"'“”"))
	runes := []rune(trimmed)
	if len(runes) > stickerNameMaxRunes {
		runes = runes[:stickerNameMaxRunes]
	}
	return string(runes)
}

func normalizeStickerTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
		if cleaned == "" {
			continue
		}
		runes := []rune(cleaned)
		if len(runes) > stickerTagMaxRunes {
			runes = runes[:stickerTagMaxRunes]
		}
		cleaned = string(runes)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
		if len(normalized) >= stickerTagMaxCount {
			break
		}
	}
	return normalized
}

func normalizeStickerSummary(summary string) string {
	cleaned := strings.TrimSpace(strings.NewReplacer("[", "", "]", "").Replace(summary))
	runes := []rune(cleaned)
	if len(runes) > stickerSummaryMaxRune {
		runes = runes[:stickerSummaryMaxRune]
	}
	return string(runes)
}

func cloneStickerItem(item StickerItem) StickerItem {
	cloned := item
	if item.Tags == nil {
		cloned.Tags = []string{}
		return cloned
	}
	cloned.Tags = append([]string{}, item.Tags...)
	return cloned
}
