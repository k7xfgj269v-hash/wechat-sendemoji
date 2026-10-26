//go:build darwin

package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

const (
	automationKeychainPath    = "/usr/bin/security"
	automationKeychainService = "com.ludwig.wechat-chatter"
	automationKeychainAccount = "provider.api_key"
)

type automationExecCommandRunner struct{}

func (automationExecCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type automationDarwinSecretStore struct {
	runner AutomationCommandRunner
	file   *automationFileSecretStore
}

func NewAutomationSecretStore(dataDir string, runner AutomationCommandRunner) AutomationSecretStore {
	if runner == nil {
		runner = automationExecCommandRunner{}
	}
	return &automationDarwinSecretStore{
		runner: runner,
		file:   newAutomationFileSecretStore(dataDir),
	}
}

func (s *automationDarwinSecretStore) Get(ctx context.Context, key string) (string, bool, error) {
	if err := validateAutomationSecretRequest(ctx, key); err != nil {
		return "", false, err
	}
	value, found, keychainErr := s.getKeychain(ctx)
	if keychainErr == nil && found {
		return value, true, nil
	}
	fileValue, fileFound, fileErr := s.file.Get(ctx, key)
	if fileErr != nil {
		return "", false, fileErr
	}
	if !fileFound {
		if keychainErr != nil {
			return "", false, keychainErr
		}
		return "", false, nil
	}
	if keychainErr != nil {
		return fileValue, true, nil
	}
	if err := s.putKeychainVerified(ctx, fileValue); err != nil {
		return fileValue, true, nil
	}
	if err := s.file.Delete(ctx, key); err != nil {
		return fileValue, true, nil
	}
	return fileValue, true, nil
}

func (s *automationDarwinSecretStore) Put(ctx context.Context, key, value string) error {
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
	if err := s.putKeychainVerified(ctx, value); err != nil {
		return err
	}
	return s.file.Delete(ctx, key)
}

func (s *automationDarwinSecretStore) Delete(ctx context.Context, key string) error {
	if err := validateAutomationSecretRequest(ctx, key); err != nil {
		return err
	}
	_, err := s.runner.Run(
		ctx,
		automationKeychainPath,
		"delete-generic-password",
		"-s", automationKeychainService,
		"-a", automationKeychainAccount,
	)
	if err != nil && !automationKeychainItemNotFound(err) {
		return WrapAutomationPersistence("delete provider key from Keychain", err)
	}
	return s.file.Delete(ctx, key)
}

func (s *automationDarwinSecretStore) putKeychainVerified(ctx context.Context, value string) error {
	_, err := s.runner.Run(
		ctx,
		automationKeychainPath,
		"add-generic-password",
		"-U",
		"-s", automationKeychainService,
		"-a", automationKeychainAccount,
		"-w", value,
	)
	if err != nil {
		return WrapAutomationPersistence("write provider key to Keychain", err)
	}
	readback, found, err := s.getKeychain(ctx)
	if err != nil {
		return err
	}
	if !found || readback != value {
		return WrapAutomationPersistence("verify provider key in Keychain", errors.New("Keychain readback mismatch"))
	}
	return nil
}

func (s *automationDarwinSecretStore) getKeychain(ctx context.Context) (string, bool, error) {
	output, err := s.runner.Run(
		ctx,
		automationKeychainPath,
		"find-generic-password",
		"-s", automationKeychainService,
		"-a", automationKeychainAccount,
		"-w",
	)
	if err != nil {
		if automationKeychainItemNotFound(err) {
			return "", false, nil
		}
		return "", false, WrapAutomationPersistence("read provider key from Keychain", err)
	}
	value := strings.TrimRight(string(output), "\r\n")
	if len(value) > automationProviderKeyMaxBytes {
		return "", false, NewAutomationValidationError("api_key", "is too long")
	}
	return value, true, nil
}

func automationKeychainItemNotFound(err error) bool {
	type exitCoder interface {
		ExitCode() int
	}
	var code exitCoder
	return errors.As(err, &code) && code.ExitCode() == 44
}
