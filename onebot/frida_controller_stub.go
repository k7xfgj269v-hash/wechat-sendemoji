//go:build noFrida

package main

import (
	"context"
	"strings"
	"time"
)

// disabledFridaController 是 noFrida 构建下 hookController 的实现。
//
// hr-workbench 进程按架构只应经 Unix socket 访问 wechat-agent，自身不该链接
// frida-core。本类型让该进程在不引入 frida 包的前提下满足 FridaRuntime 契约，
// 所有本地 Frida 操作一律返回 ErrFridaUnavailable。
//
// 注意：这不影响 hr-workbench 对外暴露的 Frida 接口。那四个操作
// (frida.status / initialize / rescan / detach) 由 registerAgentFridaRoutes
// 经 AgentClient 转发给 wechat-agent，与本类型无关。
type disabledFridaController struct {
	status FridaStatus
}

func newDisabledFridaController(cfg *Config) *disabledFridaController {
	fridaType := "local"
	gadgetAddr := "127.0.0.1:27042"
	wechatPID := 0
	if cfg != nil {
		if trimmed := strings.TrimSpace(cfg.FridaType); trimmed != "" {
			fridaType = trimmed
		}
		if trimmed := strings.TrimSpace(cfg.FridaGadgetAddr); trimmed != "" {
			gadgetAddr = trimmed
		}
		wechatPID = cfg.WechatPid
	}
	return &disabledFridaController{
		status: FridaStatus{
			State:      "unavailable",
			Type:       fridaType,
			GadgetAddr: gadgetAddr,
			WechatPID:  wechatPID,
			LastError:  ErrFridaUnavailable.Error(),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func (c *disabledFridaController) Status() FridaStatus {
	return c.status
}

func (c *disabledFridaController) Initialize(context.Context, FridaInitializeRequest) (FridaStatus, error) {
	return c.status, ErrFridaUnavailable
}

func (c *disabledFridaController) Rescan(context.Context) (FridaStatus, error) {
	return c.status, ErrFridaUnavailable
}

func (c *disabledFridaController) Detach(context.Context) (FridaStatus, error) {
	return c.status, ErrFridaUnavailable
}

func (c *disabledFridaController) AcquireScript() (FridaScriptCaller, func(), error) {
	return nil, nil, ErrFridaUnavailable
}

// newHookController 在 noFrida 构建下返回禁用实现。
func newHookController(cfg *Config) FridaRuntime {
	return newDisabledFridaController(cfg)
}
