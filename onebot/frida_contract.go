package main

import (
	"context"
	"errors"
	"time"
)

const (
	fridaReconnectDelay    = 5 * time.Second
	fridaReconnectMaxDelay = 30 * time.Second
	fridaDetachTimeout     = 5 * time.Second
)

var ErrFridaUnavailable = errors.New("Frida 控制通道尚未附加")

type FridaInitializeRequest struct {
	Type       string `json:"type"`
	GadgetAddr string `json:"gadget_addr,omitempty"`
	WechatPID  int    `json:"wechat_pid,omitempty"`
}

type FridaStatus struct {
	State         FridaLifecycleState `json:"state"`
	Type          string              `json:"type"`
	GadgetAddr    string              `json:"gadget_addr,omitempty"`
	WechatPID     int                 `json:"wechat_pid,omitempty"`
	Attached      bool                `json:"attached"`
	ScriptLoaded  bool                `json:"script_loaded"`
	AutoReconnect bool                `json:"auto_reconnect"`
	ErrorClass    FridaErrorClass     `json:"error_class,omitempty"`
	LastError     string              `json:"last_error,omitempty"`
	UpdatedAt     string              `json:"updated_at"`
}

type FridaLifecycle interface {
	Status() FridaStatus
	Initialize(context.Context, FridaInitializeRequest) (FridaStatus, error)
	Rescan(context.Context) (FridaStatus, error)
	Detach(context.Context) (FridaStatus, error)
}

// FridaScriptCaller 抽象 *frida.Script 上被本项目实际使用的两个方法，
// 使 worker.go 无需直接依赖 frida 包即可调用脚本导出。
type FridaScriptCaller interface {
	ExportsCall(fn string, args ...any) any
	ExportsCallWithContext(ctx context.Context, fn string, args ...any) any
}

// FridaRuntime 是 hookController 的完整契约：生命周期控制 + 脚本获取。
// hr-workbench 进程按架构只应经 Unix socket 访问 agent，因此在 noFrida
// 构建下由一个恒返回 ErrFridaUnavailable 的实现满足本契约。
type FridaRuntime interface {
	FridaLifecycle
	AcquireScript() (FridaScriptCaller, func(), error)
}
