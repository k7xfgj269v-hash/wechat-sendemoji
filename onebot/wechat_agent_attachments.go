package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type fileWechatAgentAttachmentRecord struct {
	Attachment AgentAttachment `json:"attachment"`
	FileName   string          `json:"file_name"`
}

type fileWechatAgentAttachmentState struct {
	Version int                                        `json:"version"`
	Records map[string]fileWechatAgentAttachmentRecord `json:"records"`
}

type FileWechatAgentAttachmentStore struct {
	directory string
	indexPath string

	mu      sync.RWMutex
	records map[string]fileWechatAgentAttachmentRecord
	closed  bool
}

func OpenFileWechatAgentAttachmentStore(directory string) (*FileWechatAgentAttachmentStore, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, errors.New("wechat agent attachment directory is unsafe")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	store := &FileWechatAgentAttachmentStore{
		directory: directory,
		indexPath: filepath.Join(directory, "attachments.json"),
		records:   make(map[string]fileWechatAgentAttachmentRecord),
	}
	data, err := os.ReadFile(store.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := store.persistLocked(store.records); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var state fileWechatAgentAttachmentState
	if err := decodeWechatAgentJSON(data, &state); err != nil ||
		state.Version != 1 || state.Records == nil {
		return nil, errors.New("wechat agent attachment index is invalid")
	}
	for id, record := range state.Records {
		if id != record.Attachment.ID ||
			validateWechatAgentPublicAttachment(record.Attachment) != nil ||
			record.FileName != id+".bin" {
			return nil, errors.New("wechat agent attachment index is invalid")
		}
	}
	store.records = state.Records
	return store, nil
}

func (s *FileWechatAgentAttachmentStore) PutBytes(
	ctx context.Context,
	name string,
	mediaType string,
	data []byte,
) (AgentAttachment, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentAttachment{}, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	id := "att-" + hash
	name = strings.TrimSpace(name)
	if name != "" {
		name = filepath.Base(name)
	}
	attachment := AgentAttachment{
		ID:        id,
		Name:      name,
		MediaType: mediaType,
		Size:      int64(len(data)),
		SHA256:    hash,
		CreatedAt: time.Now().UTC(),
	}
	if attachment.MediaType == "" {
		attachment.MediaType = mime.TypeByExtension(filepath.Ext(attachment.Name))
	}
	if err := validateWechatAgentPublicAttachment(attachment); err != nil {
		return AgentAttachment{}, err
	}
	fileName := id + ".bin"

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return AgentAttachment{}, errors.New("wechat agent attachment store is closed")
	}
	if existing, ok := s.records[id]; ok {
		return existing.Attachment, nil
	}
	path := filepath.Join(s.directory, fileName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return AgentAttachment{}, err
	}
	working := cloneWechatAgentAttachmentRecords(s.records)
	working[id] = fileWechatAgentAttachmentRecord{Attachment: attachment, FileName: fileName}
	if err := s.persistLocked(working); err != nil {
		_ = os.Remove(path)
		return AgentAttachment{}, err
	}
	s.records = working
	return attachment, nil
}

func (s *FileWechatAgentAttachmentStore) PutFile(
	ctx context.Context,
	path string,
) (AgentAttachment, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return AgentAttachment{}, errors.New("attachment source is unavailable")
	}
	if info.Size() > maxWechatAgentSendAttachment {
		return AgentAttachment{}, errors.New("attachment source is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentAttachment{}, err
	}
	return s.PutBytes(ctx, filepath.Base(path), mime.TypeByExtension(filepath.Ext(path)), data)
}

func (s *FileWechatAgentAttachmentStore) Describe(
	ctx context.Context,
	attachmentID string,
) (AgentAttachment, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentAttachment{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[attachmentID]
	if !ok {
		return AgentAttachment{}, &AgentError{
			Code: AgentErrorCodeNotFound, Message: "attachment was not found",
		}
	}
	return record.Attachment, nil
}

func (s *FileWechatAgentAttachmentStore) ReadChunk(
	ctx context.Context,
	request AgentAttachmentChunkRequest,
) (AgentAttachmentChunk, error) {
	if request.Length <= 0 || request.Length > AgentMaxAttachmentChunkBytes || request.Offset < 0 {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeValidation, Message: "attachment chunk range is invalid",
		}
	}
	s.mu.RLock()
	record, ok := s.records[request.AttachmentID]
	s.mu.RUnlock()
	if !ok {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeNotFound, Message: "attachment was not found",
		}
	}
	file, err := os.Open(filepath.Join(s.directory, record.FileName))
	if err != nil {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "attachment is unavailable",
		}
	}
	defer file.Close()
	data := make([]byte, request.Length)
	n, readErr := file.ReadAt(data, request.Offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "attachment is unavailable",
		}
	}
	data = data[:n]
	return AgentAttachmentChunk{
		AttachmentID: request.AttachmentID,
		Offset:       request.Offset,
		Data:         data,
		EOF:          request.Offset+int64(n) >= record.Attachment.Size,
	}, nil
}

func (s *FileWechatAgentAttachmentStore) ReadAll(
	ctx context.Context,
	attachmentID string,
	maxBytes int64,
) ([]byte, error) {
	attachment, err := s.Describe(ctx, attachmentID)
	if err != nil {
		return nil, err
	}
	if attachment.Size > maxBytes {
		return nil, &AgentError{Code: AgentErrorCodeValidation, Message: "attachment is too large"}
	}
	s.mu.RLock()
	record := s.records[attachmentID]
	s.mu.RUnlock()
	return os.ReadFile(filepath.Join(s.directory, record.FileName))
}

func (s *FileWechatAgentAttachmentStore) CaptureInbound(
	ctx context.Context,
	message *WechatMessage,
) ([]AgentAttachment, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return nil, err
	}
	if message == nil {
		return nil, nil
	}
	attachments := make([]AgentAttachment, 0)
	seen := make(map[string]struct{})
	for _, segment := range message.Message {
		if segment == nil || segment.Data == nil {
			continue
		}
		if len(segment.Data.Media) == 0 {
			continue
		}
		attachment, err := s.PutBytes(ctx, "", "", segment.Data.Media)
		if err != nil {
			return nil, err
		}
		if attachment.ID != "" {
			if _, exists := seen[attachment.ID]; !exists {
				seen[attachment.ID] = struct{}{}
				attachments = append(attachments, attachment)
			}
		}
	}
	return attachments, nil
}

func (s *FileWechatAgentAttachmentStore) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *FileWechatAgentAttachmentStore) persistLocked(
	records map[string]fileWechatAgentAttachmentRecord,
) error {
	return writeWechatAgentJSONAtomic(s.indexPath, fileWechatAgentAttachmentState{
		Version: 1,
		Records: records,
	})
}

func cloneWechatAgentAttachmentRecords(
	records map[string]fileWechatAgentAttachmentRecord,
) map[string]fileWechatAgentAttachmentRecord {
	cloned := make(map[string]fileWechatAgentAttachmentRecord, len(records))
	for key, record := range records {
		cloned[key] = record
	}
	return cloned
}

func validateWechatAgentPublicAttachment(attachment AgentAttachment) error {
	if err := ValidateAgentAttachment(attachment); err != nil {
		return err
	}
	if attachment.Name != "" {
		if len(attachment.Name) > 255 ||
			strings.TrimSpace(attachment.Name) != attachment.Name ||
			filepath.Base(attachment.Name) != attachment.Name ||
			strings.ContainsAny(attachment.Name, `/\:`) ||
			strings.ContainsAny(attachment.Name, "\r\n\x00") {
			return &AgentError{
				Code: AgentErrorCodeValidation, Message: "attachment name is invalid",
			}
		}
	}
	if attachment.MediaType != "" {
		if len(attachment.MediaType) > 255 ||
			strings.TrimSpace(attachment.MediaType) != attachment.MediaType ||
			strings.Contains(attachment.MediaType, "://") ||
			strings.ContainsAny(attachment.MediaType, "\r\n\x00") {
			return &AgentError{
				Code: AgentErrorCodeValidation, Message: "attachment media type is invalid",
			}
		}
		if _, _, err := mime.ParseMediaType(attachment.MediaType); err != nil {
			return &AgentError{
				Code: AgentErrorCodeValidation, Message: "attachment media type is invalid",
			}
		}
	}
	return nil
}
