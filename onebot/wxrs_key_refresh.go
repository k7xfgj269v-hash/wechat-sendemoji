//go:build !noFrida

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	wechatUseKeyCaptureTimeout = 300 * time.Second
	wechatUseVersionTimeout    = 5 * time.Second
	wechatUseExpectedVersion   = "wechat-use 1.17.1"
	dbKeyRefreshGracePeriod    = 10 * time.Second
	dbKeyContactRetryDelay     = 100 * time.Millisecond
	dbKeyContactRefreshTimeout = 2*contactCacheRefreshTimeout + time.Second
)

type fridaDBKeyRefreshFunc func(context.Context, int) error
type fridaContactRefreshFunc func(context.Context, func() bool) error

func refreshWXRSKeysForPID(ctx context.Context, pid int) error {
	if pid <= 0 {
		return errors.New("微信 PID 无效")
	}
	executable, err := resolveWechatUseExecutable()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, executable, wechatUseKeyRefreshArguments(pid)...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("wechat-use key 刷新失败: %w", err)
	}
	return nil
}

func wechatUseKeyRefreshArguments(pid int) []string {
	return []string{
		"init",
		"--force",
		"--attach-only",
		"--pid", strconv.Itoa(pid),
		"--json",
		"--skip-smoke",
		"--timeout", strconv.Itoa(int(wechatUseKeyCaptureTimeout / time.Second)),
	}
}

func resolveWechatUseExecutable() (string, error) {
	candidates := make([]string, 0, 2)
	if configured := strings.TrimSpace(os.Getenv("WECHAT_USE_BIN")); configured != "" {
		candidates = append(candidates, configured)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(
			candidates,
			filepath.Join(
				home,
				"Documents",
				"WeChatProjects",
				"wechat-use-v1.17.1",
				"wechat",
			),
		)
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			absolute = candidate
		}
		if validateWechatUseExecutable(absolute) == nil {
			return absolute, nil
		}
	}
	return "", errors.New("未找到兼容的 wechat-use 1.17.1 可执行文件")
}

func validateWechatUseExecutable(executable string) error {
	ctx, cancel := context.WithTimeout(context.Background(), wechatUseVersionTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").Output()
	if err != nil {
		return fmt.Errorf("读取 wechat-use 版本失败: %w", err)
	}
	if strings.TrimSpace(string(output)) != wechatUseExpectedVersion {
		return errors.New("wechat-use 版本不匹配")
	}
	return nil
}

func shouldRefreshWXRSKeysForAttachment(request FridaInitializeRequest) bool {
	switch request.Type {
	case "local":
		return true
	case "gadget":
		host, _, err := net.SplitHostPort(strings.TrimSpace(request.GadgetAddr))
		if err != nil {
			return false
		}
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

func (c *FridaController) scheduleDBKeyRefresh(generation uint64, pid int) {
	if c == nil || c.keyRefresher == nil || pid <= 0 {
		return
	}

	c.keyRefreshMu.Lock()
	previousDone := c.keyRefreshDone
	if c.keyRefreshCancel != nil {
		c.keyRefreshCancel()
	}
	c.keyRefreshID++
	refreshID := c.keyRefreshID
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.keyRefreshCancel = cancel
	c.keyRefreshDone = done
	c.keyRefreshMu.Unlock()

	go c.runDBKeyRefresh(ctx, refreshID, generation, pid, previousDone, done)
}

func (c *FridaController) runDBKeyRefresh(
	ctx context.Context,
	refreshID uint64,
	generation uint64,
	pid int,
	previousDone <-chan struct{},
	done chan struct{},
) {
	defer c.finishDBKeyRefresh(refreshID, done)

	if previousDone != nil {
		<-previousDone
	}
	if ctx.Err() != nil {
		return
	}

	keyCtx, cancel := context.WithTimeout(
		ctx,
		wechatUseKeyCaptureTimeout+dbKeyRefreshGracePeriod,
	)
	err := c.keyRefresher(keyCtx, pid)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			Warn("随 Frida 注入刷新微信数据库密钥失败", "PID", pid, "err", err)
		}
		return
	}
	isCurrent := func() bool {
		return c.isCurrentFridaGeneration(generation, pid)
	}
	if !isCurrent() || c.contacts == nil {
		return
	}

	refreshCtx, refreshCancel := context.WithTimeout(ctx, dbKeyContactRefreshTimeout)
	err = refreshContactsAfterDBKey(
		refreshCtx,
		func(contactCtx context.Context) error {
			return c.contacts(contactCtx, isCurrent)
		},
	)
	refreshCancel()
	if err != nil {
		if ctx.Err() == nil {
			Warn("微信数据库密钥已更新，但联系人刷新失败", "PID", pid, "err", err)
		}
		return
	}
	Info("微信数据库密钥和联系人已随 Frida 注入刷新", "PID", pid)
}

func refreshContactsAfterDBKey(
	ctx context.Context,
	refresh func(context.Context) error,
) error {
	for {
		err := refresh(ctx)
		if !errors.Is(err, errContactCacheRefreshInProgress) {
			return err
		}

		timer := time.NewTimer(dbKeyContactRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return fmt.Errorf("等待联系人缓存空闲: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *FridaController) isCurrentFridaGeneration(generation uint64, pid int) bool {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.sessionGeneration == generation &&
		c.status.Attached &&
		c.status.ScriptLoaded &&
		c.status.WechatPID == pid
}

func (c *FridaController) cancelDBKeyRefresh(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.keyRefreshMu.Lock()
	done := c.keyRefreshDone
	if c.keyRefreshCancel != nil {
		c.keyRefreshCancel()
	}
	c.keyRefreshMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待微信数据库密钥刷新退出: %w", ctx.Err())
	}
}

func (c *FridaController) finishDBKeyRefresh(refreshID uint64, done chan struct{}) {
	close(done)
	c.keyRefreshMu.Lock()
	if c.keyRefreshID == refreshID {
		if c.keyRefreshCancel != nil {
			c.keyRefreshCancel()
		}
		c.keyRefreshCancel = nil
		c.keyRefreshDone = nil
	}
	c.keyRefreshMu.Unlock()
}
