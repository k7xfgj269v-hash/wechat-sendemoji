package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	automationProviderAPIKeySecret = "provider.api_key"
	automationProviderKeyMaxBytes  = 16 << 10
)

type AutomationSecretStore interface {
	Get(context.Context, string) (string, bool, error)
	Put(context.Context, string, string) error
	Delete(context.Context, string) error
}

type AutomationCommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type automationFileSecretStore struct {
	path string
}

func newAutomationFileSecretStore(dataDir string) *automationFileSecretStore {
	return &automationFileSecretStore{path: filepath.Join(filepath.Clean(dataDir), "provider.key")}
}

func (s *automationFileSecretStore) Get(ctx context.Context, key string) (string, bool, error) {
	if err := validateAutomationSecretRequest(ctx, key); err != nil {
		return "", false, err
	}
	exists, err := automationProviderRegularFile(s.path)
	if err != nil || !exists {
		return "", false, err
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return "", false, WrapAutomationPersistence("set provider key permissions", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return "", false, WrapAutomationPersistence("read provider key", err)
	}
	if len(data) > automationProviderKeyMaxBytes {
		return "", false, NewAutomationValidationError("provider.key", "is too large")
	}
	return strings.TrimSpace(string(data)), true, nil
}

func (s *automationFileSecretStore) Put(ctx context.Context, key, value string) error {
	if err := validateAutomationSecretRequest(ctx, key); err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if len(value) > automationProviderKeyMaxBytes {
		return NewAutomationValidationError("api_key", "is too long")
	}
	if value == "" {
		return s.Delete(ctx, key)
	}
	return writeAutomationProviderFileAtomic(s.path, []byte(value))
}

func (s *automationFileSecretStore) Delete(ctx context.Context, key string) error {
	if err := validateAutomationSecretRequest(ctx, key); err != nil {
		return err
	}
	if err := os.Remove(s.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return WrapAutomationPersistence("remove provider key", err)
	}
	if err := syncAutomationDirectory(filepath.Dir(s.path)); err != nil {
		return WrapAutomationPersistence("sync provider directory", err)
	}
	return nil
}

func validateAutomationSecretRequest(ctx context.Context, key string) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if strings.TrimSpace(key) != automationProviderAPIKeySecret {
		return NewAutomationValidationError("secret", "is not supported")
	}
	return nil
}
