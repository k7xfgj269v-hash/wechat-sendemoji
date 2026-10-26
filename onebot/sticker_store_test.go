package main

// Tests for the sticker library (T2).
//
// Every test builds its library under t.TempDir(): the real
// ~/.wechat-chatter/stickers directory is never read or written, because the
// broken-index case intentionally leaves a .broken-* backup next to the index.
//
// The image fixtures are hand-built format headers (PNG/GIF/WebP/JPEG) so the
// size gate is exercised through the same parser the collector uses, instead of
// hard-coding a width/height into the store.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixtures: minimal but format-correct image headers.
// ---------------------------------------------------------------------------

func stickerTestPNG(width, height int) []byte {
	buf := make([]byte, 33)
	copy(buf, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	binary.BigEndian.PutUint32(buf[8:12], 13) // IHDR chunk length
	copy(buf[12:16], "IHDR")
	binary.BigEndian.PutUint32(buf[16:20], uint32(width))
	binary.BigEndian.PutUint32(buf[20:24], uint32(height))
	buf[24] = 8 // bit depth
	buf[25] = 6 // colour type
	return buf
}

func stickerTestGIF(width, height int) []byte {
	buf := make([]byte, 20)
	copy(buf[0:3], "GIF")
	copy(buf[3:6], "89a")
	binary.LittleEndian.PutUint16(buf[6:8], uint16(width))
	binary.LittleEndian.PutUint16(buf[8:10], uint16(height))
	return buf
}

func stickerTestJPEG(width, height int) []byte {
	buf := make([]byte, 16)
	buf[0], buf[1] = 0xFF, 0xD8 // SOI
	buf[2], buf[3] = 0xFF, 0xC0 // SOF0
	binary.BigEndian.PutUint16(buf[4:6], 11)
	buf[6] = 8 // precision
	binary.BigEndian.PutUint16(buf[7:9], uint16(height))
	binary.BigEndian.PutUint16(buf[9:11], uint16(width))
	return buf
}

// stickerTestWebP builds a RIFF/WEBP container with one of the three chunk
// layouts stickerImageSize understands. Real payload bytes are not needed: only
// the header fields the parser reads are populated.
func stickerTestWebP(tag string, width, height int) []byte {
	buf := make([]byte, 40)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(buf)-8))
	copy(buf[8:12], "WEBP")
	copy(buf[12:16], tag)
	switch tag {
	case "VP8X":
		put := func(offset, value int) {
			buf[offset] = byte(value & 0xFF)
			buf[offset+1] = byte((value >> 8) & 0xFF)
			buf[offset+2] = byte((value >> 16) & 0xFF)
		}
		put(24, width-1)
		put(27, height-1)
	case "VP8 ":
		binary.LittleEndian.PutUint16(buf[26:28], uint16(width&0x3FFF))
		binary.LittleEndian.PutUint16(buf[28:30], uint16(height&0x3FFF))
	case "VP8L":
		bits := uint32(width-1) | uint32(height-1)<<14
		binary.LittleEndian.PutUint32(buf[21:25], bits)
	}
	return buf
}

// stickerTestStore builds an isolated library rooted in a temp directory.
func stickerTestStore(t *testing.T) *StickerStore {
	t.Helper()
	root := t.TempDir()
	store := newStickerStore(root)
	if store.Root() != root {
		t.Fatalf("store root = %q, want %q", store.Root(), root)
	}
	return store
}

func stickerTestWriteLibrary(t *testing.T, store *StickerStore, items []StickerItem) {
	t.Helper()
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("create sticker root: %v", err)
	}
	data, err := json.MarshalIndent(StickerLibrary{Items: items}, "", "  ")
	if err != nil {
		t.Fatalf("encode sticker library: %v", err)
	}
	if err := os.WriteFile(store.indexPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write sticker index: %v", err)
	}
}

func stickerTestDigest(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func stickerTestMD5(seed string) string {
	sum := md5.Sum([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func stickerTestIndexPath(store *StickerStore) string {
	return filepath.Join(store.Root(), stickerIndexFileName)
}

func stickerTestDigests(items []StickerItem) []string {
	digests := make([]string, 0, len(items))
	for _, item := range items {
		digests = append(digests, item.MD5)
	}
	return digests
}

func stickerTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 1. Add / dedupe.
// ---------------------------------------------------------------------------

func TestStickerStoreAddIsContentAddressedAndIdempotent(t *testing.T) {
	store := stickerTestStore(t)
	buf := stickerTestPNG(400, 400)
	digest := stickerTestDigest(buf)

	item, existed, err := store.Add(buf, StickerMeta{From: stickerFromCollected, Name: "开心", Tags: []string{"开心"}})
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if existed {
		t.Fatal("first Add reported existed = true")
	}
	if item.MD5 != digest {
		t.Fatalf("item.MD5 = %q, want %q", item.MD5, digest)
	}
	if item.Width != 400 || item.Height != 400 {
		t.Fatalf("item size = %dx%d, want 400x400", item.Width, item.Height)
	}
	if item.From != stickerFromCollected {
		t.Fatalf("item.From = %q, want %q", item.From, stickerFromCollected)
	}
	if item.Bytes != len(buf) {
		t.Fatalf("item.Bytes = %d, want %d", item.Bytes, len(buf))
	}
	if item.At == 0 {
		t.Fatal("item.At was not stamped")
	}
	path, err := store.ImagePath(digest)
	if err != nil {
		t.Fatalf("ImagePath: %v", err)
	}
	if want := filepath.Join(store.Root(), "collected-"+digest+".png"); path != want {
		t.Fatalf("image path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sticker file was not written: %v", err)
	}

	// Same bytes, different metadata: the existing entry wins and the library
	// does not grow.
	second, existed, err := store.Add(buf, StickerMeta{From: stickerFromManual, Name: "别的名字"})
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if !existed {
		t.Fatal("second Add reported existed = false")
	}
	if second.MD5 != digest || second.Name != item.Name || second.From != item.From {
		t.Fatalf("second Add returned %+v, want the stored entry %+v", second, item)
	}
	if got := len(store.Items()); got != 1 {
		t.Fatalf("library length = %d after duplicate Add, want 1", got)
	}

	t.Run("empty content is rejected", func(t *testing.T) {
		if _, _, err := store.Add(nil, StickerMeta{}); err == nil {
			t.Fatal("Add(nil) returned no error")
		}
		if got := len(store.Items()); got != 1 {
			t.Fatalf("library length = %d after rejected Add, want 1", got)
		}
	})

	t.Run("index survives a reload", func(t *testing.T) {
		reloaded := newStickerStore(store.Root())
		items := reloaded.Items()
		if len(items) != 1 || items[0].MD5 != digest {
			t.Fatalf("reloaded items = %+v", items)
		}
		if !reloaded.Has(digest) {
			t.Fatal("reloaded store does not have the digest")
		}
		if _, ok := reloaded.Get(strings.ToUpper(digest)); !ok {
			t.Fatal("Get is not case-insensitive")
		}
	})
}

// ---------------------------------------------------------------------------
// 2. Size gate and empty-library behaviour.
// ---------------------------------------------------------------------------

func TestStickerSizeGate(t *testing.T) {
	for _, test := range []struct {
		name   string
		width  int
		height int
		bytes  int
		want   bool
	}{
		{name: "400x400 passes", width: 400, height: 400, bytes: 1024, want: true},
		{name: "400x200 passes", width: 400, height: 200, bytes: 1024, want: true},
		{name: "401x401 is rejected", width: 401, height: 401, bytes: 1024, want: false},
		{name: "1200x200 is rejected (long side)", width: 1200, height: 200, bytes: 1024, want: false},
		{name: "200x1200 is rejected (long side)", width: 200, height: 1200, bytes: 1024, want: false},
		{name: "400x100 hits the aspect ratio guard", width: 400, height: 100, bytes: 1024, want: false},
		{name: "399x100 hits the aspect ratio guard", width: 399, height: 100, bytes: 1024, want: false},
		{name: "300x100 is exactly the ratio limit", width: 300, height: 100, bytes: 1024, want: false},
		{name: "299x100 is just inside the ratio limit", width: 299, height: 100, bytes: 1024, want: true},
		{name: "exactly 1.5MiB passes", width: 400, height: 400, bytes: stickerMaxBytes, want: true},
		{name: "1.5MiB+1 is rejected", width: 400, height: 400, bytes: stickerMaxBytes + 1, want: false},
		{name: "empty payload is rejected", width: 400, height: 400, bytes: 0, want: false},
		{name: "unknown dimensions fall back to the byte cap", width: 0, height: 0, bytes: 1024, want: true},
		{name: "unknown dimensions still respect the byte cap", width: 0, height: 0, bytes: stickerMaxBytes + 1, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := stickerSizeOK(test.width, test.height, test.bytes); got != test.want {
				t.Fatalf("stickerSizeOK(%d, %d, %d) = %v, want %v",
					test.width, test.height, test.bytes, got, test.want)
			}
		})
	}
}

func TestStickerImageHeaderParsing(t *testing.T) {
	for _, test := range []struct {
		name   string
		buf    []byte
		width  int
		height int
		ext    string
		known  bool
	}{
		{name: "png", buf: stickerTestPNG(400, 400), width: 400, height: 400, ext: "png", known: true},
		{name: "gif", buf: stickerTestGIF(120, 130), width: 120, height: 130, ext: "gif", known: true},
		{name: "jpeg", buf: stickerTestJPEG(200, 300), width: 200, height: 300, ext: "jpg", known: true},
		{name: "webp vp8x", buf: stickerTestWebP("VP8X", 64, 96), width: 64, height: 96, ext: "webp", known: true},
		{name: "webp vp8", buf: stickerTestWebP("VP8 ", 32, 48), width: 32, height: 48, ext: "webp", known: true},
		{name: "webp vp8l", buf: stickerTestWebP("VP8L", 50, 70), width: 50, height: 70, ext: "webp", known: true},
		{name: "unknown bytes", buf: []byte("hello sticker"), ext: "jpg", known: false},
		// Only the 2-byte magic is needed to name the extension; the dimension
		// parser needs a full 24-byte header, so this yields 0x0.
		{name: "truncated png", buf: []byte{0x89, 'P', 'N', 'G'}, ext: "png", known: false},
		{name: "empty", buf: nil, ext: "jpg", known: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if width, height := stickerImageSize(test.buf); width != test.width || height != test.height {
				t.Fatalf("stickerImageSize = %dx%d, want %dx%d", width, height, test.width, test.height)
			}
			if got := stickerImageExt(test.buf); got != test.ext {
				t.Fatalf("stickerImageExt = %q, want %q", got, test.ext)
			}
			if got := stickerImageKnown(test.buf); got != test.known {
				t.Fatalf("stickerImageKnown = %v, want %v", got, test.known)
			}
		})
	}
}

func TestStickerStoreMissingIndexIsAnEmptyLibrary(t *testing.T) {
	root := t.TempDir()
	store := newStickerStore(root)

	items := store.Items()
	if len(items) != 0 {
		t.Fatalf("items = %+v, want an empty library", items)
	}
	if store.Has(stickerTestMD5("nope")) {
		t.Fatal("Has reported a hit in an empty library")
	}
	if _, ok := store.Get(stickerTestMD5("nope")); ok {
		t.Fatal("Get reported a hit in an empty library")
	}
	if _, err := store.ImagePath(stickerTestMD5("nope")); err == nil {
		t.Fatal("ImagePath returned no error for a missing sticker")
	}
	if err := store.Remove(stickerTestMD5("nope")); err == nil {
		t.Fatal("Remove returned no error for a missing sticker")
	}
	if err := store.MarkUsed(stickerTestMD5("nope")); err == nil {
		t.Fatal("MarkUsed returned no error for a missing sticker")
	}
	if err := store.Rename(stickerTestMD5("nope"), "x", nil); err == nil {
		t.Fatal("Rename returned no error for a missing sticker")
	}
	// Reading a missing index must not create files behind the caller's back.
	if _, err := os.Stat(stickerTestIndexPath(store)); !os.IsNotExist(err) {
		t.Fatalf("index.json was created by a read: %v", err)
	}
	settings := store.Settings()
	if !settings.Enabled || !settings.Collect || settings.DryRun {
		t.Fatalf("default settings = %+v, want enabled+collect, no dry run", settings)
	}
	if settings.MaxPerHour != stickerDefaultMaxHour || settings.MaxItems != stickerDefaultMaxItem {
		t.Fatalf("default limits = %+v", settings)
	}

	// An empty (whitespace-only) index is also just an empty library.
	if err := os.WriteFile(stickerTestIndexPath(store), []byte("\n  \n"), 0o600); err != nil {
		t.Fatalf("write empty index: %v", err)
	}
	blank := newStickerStore(root)
	if got := len(blank.Items()); got != 0 {
		t.Fatalf("blank index items = %d, want 0", got)
	}
	matches, err := filepath.Glob(filepath.Join(root, stickerIndexFileName+".broken-*"))
	if err != nil {
		t.Fatalf("glob broken backups: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("blank index produced backups: %v", matches)
	}
}

// ---------------------------------------------------------------------------
// 3. Broken index.
// ---------------------------------------------------------------------------

func TestStickerStoreBrokenIndexIsBackedUpAndTreatedAsEmpty(t *testing.T) {
	root := t.TempDir()
	store := newStickerStore(root)
	broken := []byte("{\"items\": [ this is not json")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := os.WriteFile(store.indexPath, broken, 0o600); err != nil {
		t.Fatalf("write broken index: %v", err)
	}

	items := store.Items()
	if len(items) != 0 {
		t.Fatalf("broken index produced items: %+v", items)
	}

	// The original index file must still be there, byte for byte.
	original, err := os.ReadFile(store.indexPath)
	if err != nil {
		t.Fatalf("original index.json was removed: %v", err)
	}
	if !bytes.Equal(original, broken) {
		t.Fatalf("original index.json changed: %q", original)
	}

	backups, err := filepath.Glob(store.indexPath + ".broken-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one index.json.broken-<ts>", backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, broken) {
		t.Fatalf("backup content = %q, want the broken bytes", backup)
	}

	// Loading is lazy and memoised: a second read must not pile up backups.
	_ = store.Items()
	again, err := filepath.Glob(store.indexPath + ".broken-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("backups after a second read = %v, want still one", again)
	}

	// A fresh store on the same directory behaves the same way; the library is
	// usable afterwards and writes a clean index.
	reopened := newStickerStore(root)
	if len(reopened.Items()) != 0 {
		t.Fatal("reopened store did not treat the broken index as empty")
	}
	buf := stickerTestPNG(300, 200)
	if _, _, err := reopened.Add(buf, StickerMeta{From: stickerFromCollected}); err != nil {
		t.Fatalf("Add after a broken index: %v", err)
	}
	var library StickerLibrary
	data, err := os.ReadFile(store.indexPath)
	if err != nil {
		t.Fatalf("read rewritten index: %v", err)
	}
	if err := json.Unmarshal(data, &library); err != nil {
		t.Fatalf("rewritten index is not valid JSON: %v", err)
	}
	if len(library.Items) != 1 {
		t.Fatalf("rewritten index items = %d, want 1", len(library.Items))
	}
}

// ---------------------------------------------------------------------------
// 4. Concurrency.
// ---------------------------------------------------------------------------

func TestStickerStoreConcurrentAddsStayConsistent(t *testing.T) {
	store := stickerTestStore(t)
	const writers = 8

	var waitGroup sync.WaitGroup
	errs := make([]error, writers)
	waitGroup.Add(writers)
	for index := 0; index < writers; index++ {
		go func(index int) {
			defer waitGroup.Done()
			// Distinct payloads: the PNG header carries the index, so every
			// writer computes a different md5.
			buf := stickerTestPNG(10+index, 10+index)
			_, _, err := store.Add(buf, StickerMeta{
				From: stickerFromCollected,
				Name: fmt.Sprintf("并发-%d", index),
			})
			errs[index] = err
		}(index)
	}
	waitGroup.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", index, err)
		}
	}
	items := store.Items()
	if len(items) != writers {
		t.Fatalf("library length = %d, want %d", len(items), writers)
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, duplicate := seen[item.MD5]; duplicate {
			t.Fatalf("duplicate md5 %q after concurrent Adds", item.MD5)
		}
		seen[item.MD5] = struct{}{}
		if _, err := os.Stat(filepath.Join(store.Root(), item.File)); err != nil {
			t.Fatalf("file for %q is missing: %v", item.MD5, err)
		}
	}

	// The index on disk must be complete and parseable.
	data, err := os.ReadFile(store.indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var library StickerLibrary
	if err := json.Unmarshal(data, &library); err != nil {
		t.Fatalf("index is not valid JSON after concurrent Adds: %v", err)
	}
	if len(library.Items) != writers {
		t.Fatalf("on-disk items = %d, want %d", len(library.Items), writers)
	}

	reloaded := newStickerStore(store.Root())
	if got := len(reloaded.Items()); got != writers {
		t.Fatalf("reloaded items = %d, want %d", got, writers)
	}
}

// ---------------------------------------------------------------------------
// 5. Match / PickForReply.
// ---------------------------------------------------------------------------

func TestStickerStoreMatchScoring(t *testing.T) {
	for _, test := range []struct {
		name  string
		items []StickerItem
		query string
		want  bool
		md5   string // expected winner when want is true
	}{
		{
			name:  "exact name wins",
			items: []StickerItem{{MD5: stickerTestMD5("exact"), File: "manual-exact.png", Name: "开心"}},
			query: "开心",
			want:  true,
			md5:   stickerTestMD5("exact"),
		},
		{
			name:  "substring match",
			items: []StickerItem{{MD5: stickerTestMD5("contains"), File: "manual-contains.png", Name: "今天很开心"}},
			query: "开心",
			want:  true,
			md5:   stickerTestMD5("contains"),
		},
		{
			name: "character overlap above the threshold",
			// Nothing contains anything, so only the overlap tier can fire:
			// 2 shared runes over a 5 rune query = 1.6 >= 1.5.
			items: []StickerItem{{MD5: stickerTestMD5("overlap"), File: "manual-overlap.png", Name: "开a心b"}},
			query: "开心xyz",
			want:  true,
			md5:   stickerTestMD5("overlap"),
		},
		{
			name:  "character overlap below the threshold",
			items: []StickerItem{{MD5: stickerTestMD5("below"), File: "manual-below.png", Name: "开a心b"}},
			query: "开心xyzw", // 2/6*4 = 1.33 < 1.5
			want:  false,
		},
		{
			name:  "a single shared character is not enough",
			items: []StickerItem{{MD5: stickerTestMD5("single"), File: "manual-single.png", Name: "开a心b"}},
			query: "开xxxxxx",
			want:  false,
		},
		{
			name: "a tag match outranks a weak name match",
			items: []StickerItem{
				{MD5: stickerTestMD5("weak"), File: "manual-weak.png", Name: "猫"},
				{MD5: stickerTestMD5("tag"), File: "manual-tag.png", Tags: []string{"开心"}},
			},
			query: "开心",
			want:  true,
			md5:   stickerTestMD5("tag"),
		},
		{
			name:  "empty query never matches",
			items: []StickerItem{{MD5: stickerTestMD5("empty"), File: "manual-empty.png", Name: "开心"}},
			query: "   ",
			want:  false,
		},
		{
			name:  "empty library never matches",
			items: nil,
			query: "开心",
			want:  false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := stickerTestStore(t)
			if len(test.items) > 0 {
				stickerTestWriteLibrary(t, store, test.items)
				store = newStickerStore(store.Root())
			}
			item, ok := store.Match(test.query)
			if ok != test.want {
				t.Fatalf("Match(%q) ok = %v, want %v (item=%+v)", test.query, ok, test.want, item)
			}
			if test.want && item.MD5 != test.md5 {
				t.Fatalf("Match(%q) = %q, want %q", test.query, item.MD5, test.md5)
			}
			if !test.want && item.MD5 != "" {
				t.Fatalf("Match(%q) returned an item on a miss: %+v", test.query, item)
			}
		})
	}
}

func TestStickerStorePickForReplyPrefersTagsThenRandom(t *testing.T) {
	store := stickerTestStore(t)
	tagged := StickerItem{MD5: stickerTestMD5("tagged"), File: "manual-tagged.png", Tags: []string{"猫猫"}, From: stickerFromManual}
	other := StickerItem{MD5: stickerTestMD5("other"), File: "manual-other.png", Tags: []string{"无语"}, From: stickerFromManual}
	stickerTestWriteLibrary(t, store, []StickerItem{other, tagged})
	store = newStickerStore(store.Root())

	picked, ok := store.PickForReply("给我一张猫猫表情")
	if !ok {
		t.Fatal("PickForReply did not return a sticker for a plain tag hit")
	}
	if picked.MD5 != tagged.MD5 {
		t.Fatalf("PickForReply = %q, want the tagged sticker %q", picked.MD5, tagged.MD5)
	}

	// A text without any known tag falls back to a random library entry.
	for attempt := 0; attempt < 5; attempt++ {
		picked, ok := store.PickForReply("和标签无关的一句话")
		if !ok {
			t.Fatal("PickForReply returned no sticker for a fallback")
		}
		if picked.MD5 != tagged.MD5 && picked.MD5 != other.MD5 {
			t.Fatalf("PickForReply returned an unknown sticker: %+v", picked)
		}
	}

	empty := newStickerStore(t.TempDir())
	if picked, ok := empty.PickForReply("开心"); ok || picked.MD5 != "" {
		t.Fatalf("PickForReply on an empty library = %+v, %v", picked, ok)
	}
}

// ---------------------------------------------------------------------------
// 6. Trim.
// ---------------------------------------------------------------------------

func TestStickerStoreTrimOnlyEvictsCollected(t *testing.T) {
	store := stickerTestStore(t)
	// Explicit timestamps keep the "oldest first" tie-break deterministic.
	oldest := StickerItem{
		MD5: stickerTestMD5("oldest"), File: "collected-oldest.png",
		From: stickerFromCollected, At: 1_000, Uses: 0,
	}
	newer := StickerItem{
		MD5: stickerTestMD5("newer"), File: "collected-newer.png",
		From: stickerFromCollected, At: 2_000, Uses: 0,
	}
	used := StickerItem{
		MD5: stickerTestMD5("used"), File: "collected-used.png",
		From: stickerFromCollected, At: 500, Uses: 5, LastUsed: 3_000,
	}
	manual := StickerItem{
		MD5: stickerTestMD5("manual"), File: "manual-keep.png",
		From: stickerFromManual, At: 1, Uses: 0,
	}
	stickerTestWriteLibrary(t, store, []StickerItem{oldest, newer, used, manual})
	for _, item := range []StickerItem{oldest, newer, used, manual} {
		if err := os.WriteFile(filepath.Join(store.Root(), item.File), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed sticker file: %v", err)
		}
	}
	store = newStickerStore(store.Root())

	removed, err := store.Trim(3)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	// The manual entry is the oldest and least used, but it must stay; among the
	// collected entries uses wins first, then age, so "newer" outlives "oldest".
	if len(removed) != 1 || removed[0] != oldest.MD5 {
		t.Fatalf("Trim(3) removed %v, want [%s]", removed, oldest.MD5)
	}
	remaining := stickerTestDigests(store.Items())
	if len(remaining) != 3 {
		t.Fatalf("remaining = %v, want 3 items", remaining)
	}
	for _, want := range []string{newer.MD5, used.MD5, manual.MD5} {
		if !stickerTestContains(remaining, want) {
			t.Fatalf("Trim removed %q unexpectedly; remaining = %v", want, remaining)
		}
	}
	if _, err := os.Stat(filepath.Join(store.Root(), oldest.File)); !os.IsNotExist(err) {
		t.Fatalf("evicted sticker file still exists: %v", err)
	}

	// Next round: only "newer" (uses 0) can go; "used" has uses 5.
	removed, err = store.Trim(2)
	if err != nil {
		t.Fatalf("second Trim: %v", err)
	}
	if len(removed) != 1 || removed[0] != newer.MD5 {
		t.Fatalf("Trim(2) removed %v, want [%s]", removed, newer.MD5)
	}
	remaining = stickerTestDigests(store.Items())
	if !stickerTestContains(remaining, manual.MD5) || !stickerTestContains(remaining, used.MD5) {
		t.Fatalf("remaining = %v, want the manual and the most-used sticker", remaining)
	}

	t.Run("manual-only library is never trimmed", func(t *testing.T) {
		store := stickerTestStore(t)
		manuals := []StickerItem{
			{MD5: stickerTestMD5("m1"), File: "manual-1.png", From: stickerFromManual, At: 1},
			{MD5: stickerTestMD5("m2"), File: "manual-2.png", From: stickerFromManual, At: 2},
			{MD5: stickerTestMD5("m3"), File: "manual-3.png", From: stickerFromManual, At: 3},
		}
		stickerTestWriteLibrary(t, store, manuals)
		store = newStickerStore(store.Root())
		removed, err := store.Trim(1)
		if err != nil {
			t.Fatalf("Trim: %v", err)
		}
		if len(removed) != 0 {
			t.Fatalf("Trim removed manual stickers: %v", removed)
		}
		if got := len(store.Items()); got != 3 {
			t.Fatalf("manual library length = %d after Trim(1), want 3", got)
		}
	})

	t.Run("nothing to do when already within the limit", func(t *testing.T) {
		store := stickerTestStore(t)
		if _, _, err := store.Add(stickerTestPNG(200, 200), StickerMeta{From: stickerFromCollected}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		removed, err := store.Trim(10)
		if err != nil {
			t.Fatalf("Trim: %v", err)
		}
		if len(removed) != 0 {
			t.Fatalf("Trim removed %v on an under-limit library", removed)
		}
	})

	t.Run("maxItems is capped by the per-call guard", func(t *testing.T) {
		store := stickerTestStore(t)
		items := make([]StickerItem, 0, stickerTrimGuard+16)
		for index := 0; index < stickerTrimGuard+16; index++ {
			items = append(items, StickerItem{
				MD5:  stickerTestMD5(fmt.Sprintf("bulk-%d", index)),
				File: fmt.Sprintf("collected-bulk-%d.png", index),
				From: stickerFromCollected,
				At:   int64(index + 1),
			})
		}
		stickerTestWriteLibrary(t, store, items)
		store = newStickerStore(store.Root())
		removed, err := store.Trim(1)
		if err != nil {
			t.Fatalf("Trim: %v", err)
		}
		if len(removed) != stickerTrimGuard {
			t.Fatalf("Trim removed %d items, want the per-call guard %d", len(removed), stickerTrimGuard)
		}
		if got := len(store.Items()); got != len(items)-stickerTrimGuard {
			t.Fatalf("library length = %d after a guarded Trim, want %d", got, len(items)-stickerTrimGuard)
		}
	})
}

// ---------------------------------------------------------------------------
// 7. MarkUsed.
// ---------------------------------------------------------------------------

func TestStickerStoreMarkUsedCountsSends(t *testing.T) {
	store := stickerTestStore(t)
	buf := stickerTestPNG(256, 256)
	item, _, err := store.Add(buf, StickerMeta{From: stickerFromManual, Name: "点头"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.Uses != 0 || item.LastUsed != 0 {
		t.Fatalf("new item = uses %d, lastUsed %d; want zeros", item.Uses, item.LastUsed)
	}

	if err := store.MarkUsed(item.MD5); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	used, ok := store.Get(item.MD5)
	if !ok {
		t.Fatal("sticker disappeared after MarkUsed")
	}
	if used.Uses != 1 {
		t.Fatalf("uses = %d, want 1", used.Uses)
	}
	if used.LastUsed <= 0 {
		t.Fatalf("lastUsed = %d, want a millisecond stamp", used.LastUsed)
	}
	first := used.LastUsed

	if err := store.MarkUsed(item.MD5); err != nil {
		t.Fatalf("second MarkUsed: %v", err)
	}
	used, _ = store.Get(item.MD5)
	if used.Uses != 2 {
		t.Fatalf("uses = %d, want 2", used.Uses)
	}
	if used.LastUsed < first {
		t.Fatalf("lastUsed went backwards: %d -> %d", first, used.LastUsed)
	}

	// The bump is persisted, not just in memory.
	reloaded := newStickerStore(store.Root())
	persisted, ok := reloaded.Get(item.MD5)
	if !ok || persisted.Uses != 2 || persisted.LastUsed != used.LastUsed {
		t.Fatalf("persisted item = %+v (ok=%v), want uses 2", persisted, ok)
	}

	// A malformed digest is a miss, not a panic.
	if err := store.MarkUsed("not-a-digest"); err == nil {
		t.Fatal("MarkUsed accepted a malformed digest")
	}
}

// ---------------------------------------------------------------------------
// 8. Rename clamping.
// ---------------------------------------------------------------------------

func TestStickerStoreRenameClampsNameAndTags(t *testing.T) {
	store := stickerTestStore(t)
	item, _, err := store.Add(stickerTestPNG(128, 128), StickerMeta{From: stickerFromManual})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	longName := "这是一个超过十二个字的表情名字"
	tags := []string{"一", "二", "三", "四", "五", "六", "七"}
	if err := store.Rename(item.MD5, longName, tags); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	renamed, ok := store.Get(item.MD5)
	if !ok {
		t.Fatal("sticker disappeared after Rename")
	}
	if runes := []rune(renamed.Name); len(runes) != stickerNameMaxRunes {
		t.Fatalf("name runes = %d (%q), want %d", len(runes), renamed.Name, stickerNameMaxRunes)
	}
	if got := renamed.Name; got != string([]rune(longName)[:stickerNameMaxRunes]) {
		t.Fatalf("name = %q, want the first %d runes of %q", got, stickerNameMaxRunes, longName)
	}
	if len(renamed.Tags) != stickerTagMaxCount {
		t.Fatalf("tags = %v, want %d entries", renamed.Tags, stickerTagMaxCount)
	}
	if strings.Join(renamed.Tags, ",") != "一,二,三,四,五" {
		t.Fatalf("tags = %v, want the first five in order", renamed.Tags)
	}

	t.Run("tags are normalised", func(t *testing.T) {
		longTag := strings.Repeat("長", stickerTagMaxRunes+4)
		err := store.Rename(item.MD5, "  带引号的  ", []string{" #前缀", "长" + longTag, "前缀", "   "})
		if err != nil {
			t.Fatalf("Rename: %v", err)
		}
		updated, _ := store.Get(item.MD5)
		if updated.Name != "带引号的" {
			t.Fatalf("name = %q, want the trimmed name", updated.Name)
		}
		if got := updated.Tags; len(got) != 2 || got[0] != "前缀" {
			t.Fatalf("tags = %v, want # stripped, blank dropped, duplicates removed", got)
		}
		if runes := []rune(updated.Tags[1]); len(runes) != stickerTagMaxRunes {
			t.Fatalf("long tag = %q (%d runes), want clamped to %d", updated.Tags[1], len(runes), stickerTagMaxRunes)
		}
	})

	t.Run("rename is persisted", func(t *testing.T) {
		reloaded := newStickerStore(store.Root())
		persisted, ok := reloaded.Get(item.MD5)
		if !ok {
			t.Fatal("sticker missing after reload")
		}
		if persisted.Name != "带引号的" {
			t.Fatalf("persisted name = %q", persisted.Name)
		}
	})
}

// ---------------------------------------------------------------------------
// Collector: the only caller of the size gate.
// ---------------------------------------------------------------------------

func TestStickerCollectReceivedSticker(t *testing.T) {
	writeFile := func(t *testing.T, dir, name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
		return path
	}

	t.Run("collects a valid sticker", func(t *testing.T) {
		store := stickerTestStore(t)
		source := writeFile(t, t.TempDir(), "face.png", stickerTestPNG(400, 400))

		if err := collectReceivedSticker(store, ReceivedEmoji{Path: source, Summary: "[微笑]"}); err != nil {
			t.Fatalf("collect: %v", err)
		}
		items := store.Items()
		if len(items) != 1 {
			t.Fatalf("items = %+v, want one collected sticker", items)
		}
		if items[0].From != stickerFromCollected {
			t.Fatalf("item.From = %q, want %q", items[0].From, stickerFromCollected)
		}
		if items[0].Summary != "微笑" {
			t.Fatalf("summary = %q, want the brackets stripped", items[0].Summary)
		}
		// The source file must survive: inbound messages still point at it.
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("collector consumed the source file: %v", err)
		}
	})

	t.Run("rejects oversized pixels, extreme ratios and oversized payloads", func(t *testing.T) {
		for _, fixture := range []struct {
			name string
			data []byte
		}{
			{name: "401x401", data: stickerTestPNG(401, 401)},
			{name: "1200x200", data: stickerTestPNG(1200, 200)},
			{name: "1.6MiB", data: append(stickerTestPNG(400, 400), make([]byte, stickerMaxBytes)...)},
		} {
			t.Run(fixture.name, func(t *testing.T) {
				store := stickerTestStore(t)
				source := writeFile(t, t.TempDir(), "face.png", fixture.data)
				if err := collectReceivedSticker(store, ReceivedEmoji{Path: source}); err != nil {
					t.Fatalf("collect returned an error for a merely unsuitable sticker: %v", err)
				}
				if got := len(store.Items()); got != 0 {
					t.Fatalf("library grew to %d entries for %s", got, fixture.name)
				}
			})
		}
	})

	t.Run("400x400 is accepted", func(t *testing.T) {
		store := stickerTestStore(t)
		source := writeFile(t, t.TempDir(), "face.png", stickerTestPNG(400, 400))
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: source}); err != nil {
			t.Fatalf("collect: %v", err)
		}
		if got := len(store.Items()); got != 1 {
			t.Fatalf("library = %d entries, want 1", got)
		}
	})

	t.Run("duplicates are skipped", func(t *testing.T) {
		store := stickerTestStore(t)
		source := writeFile(t, t.TempDir(), "face.png", stickerTestPNG(300, 300))
		for i := 0; i < 2; i++ {
			if err := collectReceivedSticker(store, ReceivedEmoji{Path: source}); err != nil {
				t.Fatalf("collect #%d: %v", i+1, err)
			}
		}
		if got := len(store.Items()); got != 1 {
			t.Fatalf("library = %d entries after a duplicate collect, want 1", got)
		}
	})

	t.Run("hourly quota", func(t *testing.T) {
		store := stickerTestStore(t)
		if err := os.WriteFile(store.settingsPath, []byte(`{"max_per_hour":1}`), 0o600); err != nil {
			t.Fatalf("write settings: %v", err)
		}
		store = newStickerStore(store.Root())
		dir := t.TempDir()
		first := writeFile(t, dir, "a.png", stickerTestPNG(200, 200))
		second := writeFile(t, dir, "b.png", stickerTestPNG(210, 210))
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: first}); err != nil {
			t.Fatalf("first collect: %v", err)
		}
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: second}); err != nil {
			t.Fatalf("second collect: %v", err)
		}
		if got := len(store.Items()); got != 1 {
			t.Fatalf("library = %d entries with max_per_hour=1, want 1", got)
		}
	})

	t.Run("disabled or non-collecting settings skip silently", func(t *testing.T) {
		for _, settings := range []string{`{"enabled":false}`, `{"enabled":true,"collect":false}`} {
			store := stickerTestStore(t)
			if err := os.WriteFile(store.settingsPath, []byte(settings), 0o600); err != nil {
				t.Fatalf("write settings: %v", err)
			}
			store = newStickerStore(store.Root())
			source := writeFile(t, t.TempDir(), "face.png", stickerTestPNG(100, 100))
			if err := collectReceivedSticker(store, ReceivedEmoji{Path: source}); err != nil {
				t.Fatalf("collect with %s: %v", settings, err)
			}
			if got := len(store.Items()); got != 0 {
				t.Fatalf("library = %d entries while collection was off (%s)", got, settings)
			}
		}
	})

	t.Run("unusable input surfaces an error", func(t *testing.T) {
		store := stickerTestStore(t)
		if err := collectReceivedSticker(nil, ReceivedEmoji{Path: "x"}); err == nil {
			t.Fatal("collect with a nil store returned no error")
		}
		if err := collectReceivedSticker(store, ReceivedEmoji{}); err == nil {
			t.Fatal("collect with an empty path returned no error")
		}
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: filepath.Join(t.TempDir(), "missing.png")}); err == nil {
			t.Fatal("collect with a missing file returned no error")
		}
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: t.TempDir()}); err == nil {
			t.Fatal("collect with a directory returned no error")
		}
		empty := filepath.Join(t.TempDir(), "empty.png")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatalf("write empty fixture: %v", err)
		}
		if err := collectReceivedSticker(store, ReceivedEmoji{Path: empty}); err == nil {
			t.Fatal("collect of an empty file returned no error")
		}
	})
}

// ---------------------------------------------------------------------------
// Small surface: settings, defaults, remove, path safety, nil receiver.
// ---------------------------------------------------------------------------

func TestStickerStoreSurface(t *testing.T) {
	t.Run("settings come from config.json and fall back on garbage", func(t *testing.T) {
		store := stickerTestStore(t)
		if err := os.WriteFile(store.settingsPath, []byte(`{"enabled":false,"max_items":7}`), 0o600); err != nil {
			t.Fatalf("write settings: %v", err)
		}
		reloaded := newStickerStore(store.Root())
		settings := reloaded.Settings()
		if settings.Enabled || settings.MaxItems != 7 {
			t.Fatalf("settings = %+v, want enabled=false max_items=7", settings)
		}
		if settings.MaxPerHour != stickerDefaultMaxHour {
			t.Fatalf("max_per_hour = %d, want the default", settings.MaxPerHour)
		}

		if err := os.WriteFile(store.settingsPath, []byte("not json"), 0o600); err != nil {
			t.Fatalf("write broken settings: %v", err)
		}
		broken := newStickerStore(store.Root())
		if got := broken.Settings(); !got.Enabled || !got.Collect {
			t.Fatalf("broken settings = %+v, want defaults", got)
		}
	})

	t.Run("remove deletes index entry and file", func(t *testing.T) {
		store := stickerTestStore(t)
		item, _, err := store.Add(stickerTestPNG(150, 150), StickerMeta{From: stickerFromManual})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		path, _ := store.ImagePath(item.MD5)
		if err := store.Remove(item.MD5); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if store.Has(item.MD5) {
			t.Fatal("sticker still indexed after Remove")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("sticker file survived Remove: %v", err)
		}
		if err := store.Remove(item.MD5); err == nil {
			t.Fatal("second Remove returned no error")
		}
	})

	t.Run("file paths are contained by the library root", func(t *testing.T) {
		store := stickerTestStore(t)
		stickerTestWriteLibrary(t, store, []StickerItem{{
			MD5:  stickerTestMD5("traversal"),
			File: "../../../../etc/passwd",
			From: stickerFromManual,
		}})
		reopened := newStickerStore(store.Root())
		item, ok := reopened.Get(stickerTestMD5("traversal"))
		if !ok {
			t.Fatal("item was dropped instead of normalised")
		}
		if strings.Contains(item.File, "..") || strings.ContainsRune(item.File, filepath.Separator) {
			t.Fatalf("stored file name = %q, want a bare base name", item.File)
		}
		path, err := reopened.ImagePath(stickerTestMD5("traversal"))
		if err != nil {
			t.Fatalf("ImagePath: %v", err)
		}
		if filepath.Dir(path) != reopened.Root() {
			t.Fatalf("image path %q escapes the root %q", path, reopened.Root())
		}
	})

	t.Run("broken entries are dropped on load", func(t *testing.T) {
		store := stickerTestStore(t)
		stickerTestWriteLibrary(t, store, []StickerItem{
			{MD5: "not-a-digest", File: "x.png", From: stickerFromManual},
			{MD5: stickerTestMD5("no-file"), File: "", From: stickerFromManual},
			{MD5: stickerTestMD5("good"), File: "manual-good.png", From: "unknown-source", Uses: -3, Bytes: -1},
		})
		reopened := newStickerStore(store.Root())
		items := reopened.Items()
		if len(items) != 1 {
			t.Fatalf("items = %+v, want only the loadable entry", items)
		}
		if items[0].From != stickerFromManual {
			t.Fatalf("unknown source normalised to %q, want %q", items[0].From, stickerFromManual)
		}
		if items[0].Uses != 0 || items[0].Bytes != 0 {
			t.Fatalf("negative counters not clamped: %+v", items[0])
		}
	})

	t.Run("the default directory lives under HOME", func(t *testing.T) {
		// Deliberately does not touch the process-wide default store: HOME is
		// redirected to a temp dir so no real ~/.wechat-chatter is ever read or
		// written, and no global state leaks into other tests.
		home := t.TempDir()
		t.Setenv("HOME", home)

		dir, err := DefaultStickerDataDir()
		if err != nil {
			t.Fatalf("DefaultStickerDataDir: %v", err)
		}
		if want := filepath.Join(home, ".wechat-chatter", "stickers"); dir != want {
			t.Fatalf("default dir = %q, want %q", dir, want)
		}
		// An empty root asks newStickerStore to resolve the same default.
		store := newStickerStore("")
		if store.Root() != dir {
			t.Fatalf("newStickerStore(\"\") root = %q, want %q", store.Root(), dir)
		}
		if got := len(store.Items()); got != 0 {
			t.Fatalf("a fresh temp HOME produced %d items", got)
		}
	})

	t.Run("nil receiver is safe", func(t *testing.T) {
		var store *StickerStore
		if store.Root() != "" {
			t.Fatal("nil store Root() is not empty")
		}
		if store.Items() != nil {
			t.Fatal("nil store Items() is not nil")
		}
		if _, ok := store.Get(stickerTestMD5("x")); ok {
			t.Fatal("nil store reported a hit")
		}
		if store.Has(stickerTestMD5("x")) {
			t.Fatal("nil store reported Has")
		}
		if _, _, err := store.Add([]byte("x"), StickerMeta{}); err == nil {
			t.Fatal("nil store Add returned no error")
		}
		if _, err := store.Trim(1); err == nil {
			t.Fatal("nil store Trim returned no error")
		}
		if _, ok := store.Match("x"); ok {
			t.Fatal("nil store Match reported a hit")
		}
		if _, ok := store.PickForReply("x"); ok {
			t.Fatal("nil store PickForReply reported a hit")
		}
		if err := store.Remove(stickerTestMD5("x")); err == nil {
			t.Fatal("nil store Remove returned no error")
		}
		if store.Settings().MaxItems != stickerDefaultMaxItem {
			t.Fatal("nil store Settings did not fall back to defaults")
		}
	})

	t.Run("store without a root refuses to write", func(t *testing.T) {
		store := &StickerStore{settings: defaultStickerSettings()}
		if _, _, err := store.Add(stickerTestPNG(10, 10), StickerMeta{}); err == nil {
			t.Fatal("Add without a root returned no error")
		}
		if _, ok := store.Match("x"); ok {
			t.Fatal("Match on a rootless store reported a hit")
		}
	})
}
