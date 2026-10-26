package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func defaultWechatAgentPaths() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	runDirectory := filepath.Join(home, defaultWechatAgentDirectoryName)
	if override := strings.TrimSpace(os.Getenv("WECHAT_AGENT_RUN_DIR")); override != "" {
		runDirectory = override
	}
	socketPath := filepath.Join(runDirectory, defaultWechatAgentSocketName)
	if override := strings.TrimSpace(os.Getenv("WECHAT_AGENT_SOCKET")); override != "" {
		socketPath = override
	}
	if !filepath.IsAbs(runDirectory) || filepath.Clean(runDirectory) != runDirectory ||
		ValidateAgentSocketPath(socketPath) != nil {
		return "", "", ErrUnsafeAgentSocketPath
	}
	return runDirectory, socketPath, nil
}

func resolveWechatAgentPaths(cfg *Config) (string, string, error) {
	defaultRunDirectory, defaultSocketPath, err := defaultWechatAgentPaths()
	if err != nil {
		return "", "", err
	}
	runDirectory := strings.TrimSpace(cfg.RunDir)
	if runDirectory == "" {
		runDirectory = defaultRunDirectory
	}
	if !filepath.IsAbs(runDirectory) || filepath.Clean(runDirectory) != runDirectory {
		return "", "", ErrUnsafeAgentSocketPath
	}
	socketPath := strings.TrimSpace(cfg.AgentSocket)
	if socketPath == "" {
		if runDirectory == defaultRunDirectory {
			socketPath = defaultSocketPath
		} else {
			socketPath = filepath.Join(runDirectory, defaultWechatAgentSocketName)
		}
	}
	if err := ValidateAgentSocketPath(socketPath); err != nil {
		return "", "", err
	}
	return runDirectory, socketPath, nil
}

func ensureWechatAgentRunDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeAgentSocketPath
	}
	owner, ok := agentFileOwner(info)
	if !ok || owner != uint32(os.Getuid()) {
		return ErrUnsafeAgentSocketPath
	}
	return os.Chmod(path, 0700)
}

func listenWechatAgentUnix(socketPath string) (*net.UnixListener, error) {
	if err := ValidateAgentSocketParent(socketPath, uint32(os.Getuid())); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(socketPath); err == nil {
		return nil, errors.New("wechat agent socket already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	if err := ValidateAgentSocketFile(socketPath, uint32(os.Getuid())); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	return listener, nil
}
