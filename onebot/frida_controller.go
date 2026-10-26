//go:build !noFrida

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frida/frida-go/frida"
)

type fridaResources struct {
	manager    *frida.DeviceManager
	device     frida.DeviceInt
	session    *frida.Session
	script     *frida.Script
	remoteAddr string
}

type fridaContextScriptLoader func(context.Context, *frida.Session) (*frida.Script, error)

type fridaAttachRunner func(
	context.Context,
	FridaInitializeRequest,
	bool,
) (FridaStatus, error)

type FridaController struct {
	config       *Config
	scriptLoader fridaContextScriptLoader
	keyRefresher fridaDBKeyRefreshFunc
	contacts     fridaContactRefreshFunc

	operationMu sync.Mutex
	operations  fridaLifecycleCoordinator
	resourcesMu sync.RWMutex
	resources   fridaResources

	statusMu          sync.RWMutex
	status            FridaStatus
	desired           FridaInitializeRequest
	sessionGeneration uint64

	reconnectMu     sync.Mutex
	reconnectCancel context.CancelFunc
	reconnectID     uint64

	keyRefreshMu     sync.Mutex
	keyRefreshCancel context.CancelFunc
	keyRefreshDone   chan struct{}
	keyRefreshID     uint64
}

func NewFridaController(cfg *Config, scriptLoader func(*frida.Session) (*frida.Script, error)) *FridaController {
	var contextualLoader fridaContextScriptLoader
	if scriptLoader != nil {
		contextualLoader = func(_ context.Context, session *frida.Session) (*frida.Script, error) {
			return scriptLoader(session)
		}
	}
	return newFridaController(cfg, contextualLoader)
}

func newFridaController(cfg *Config, scriptLoader fridaContextScriptLoader) *FridaController {
	request := FridaInitializeRequest{
		Type:       "local",
		GadgetAddr: "127.0.0.1:27042",
	}
	if cfg != nil {
		request.Type = strings.TrimSpace(cfg.FridaType)
		request.GadgetAddr = strings.TrimSpace(cfg.FridaGadgetAddr)
		request.WechatPID = cfg.WechatPid
	}
	if request.Type == "" {
		request.Type = "local"
	}
	if request.GadgetAddr == "" {
		request.GadgetAddr = "127.0.0.1:27042"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return &FridaController{
		config:       cfg,
		scriptLoader: scriptLoader,
		keyRefresher: refreshWXRSKeysForPID,
		contacts: func(ctx context.Context, shouldPublish func() bool) error {
			return globalContactCache.RefreshContextIf(ctx, shouldPublish)
		},
		desired: request,
		status: FridaStatus{
			State:      FridaStateDetached,
			Type:       request.Type,
			GadgetAddr: request.GadgetAddr,
			WechatPID:  request.WechatPID,
			UpdatedAt:  now,
		},
	}
}

func (c *FridaController) Status() FridaStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *FridaController) Initialize(ctx context.Context, request FridaInitializeRequest) (FridaStatus, error) {
	normalized, err := normalizeFridaInitializeRequest(request)
	if err != nil {
		return c.Status(), err
	}
	return c.runAttach(ctx, fridaOperationInitialize, normalized, false, false)
}

func (c *FridaController) Rescan(ctx context.Context) (FridaStatus, error) {
	return c.rescanWith(ctx, c.attachResources)
}

func (c *FridaController) rescanWith(
	ctx context.Context,
	attach fridaAttachRunner,
) (FridaStatus, error) {
	return c.runLifecycleOperation(
		ctx,
		fridaOperationRescan,
		FridaInitializeRequest{},
		func(operationContext context.Context) (FridaStatus, error) {
			c.statusMu.RLock()
			request := c.desired
			c.statusMu.RUnlock()
			normalized, err := normalizeFridaInitializeRequest(request)
			if err != nil {
				return c.Status(), err
			}
			if normalized.Type == "local" {
				normalized.WechatPID = 0
			}
			c.suppressAutoReconnect()
			c.stopReconnect()
			return attach(operationContext, normalized, false)
		},
	)
}

func (c *FridaController) Detach(ctx context.Context) (FridaStatus, error) {
	return c.runLifecycleOperation(
		ctx,
		fridaOperationDetach,
		FridaInitializeRequest{},
		func(operationContext context.Context) (FridaStatus, error) {
			c.suppressAutoReconnect()
			c.stopReconnect()
			return c.detach(operationContext)
		},
	)
}

func (c *FridaController) detach(ctx context.Context) (FridaStatus, error) {
	c.statusMu.Lock()
	c.sessionGeneration++
	c.status.State = FridaStateDetaching
	c.status.Attached = false
	c.status.ScriptLoaded = false
	c.status.AutoReconnect = false
	setFridaStatusError(&c.status, nil)
	c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	c.statusMu.Unlock()
	if err := c.cancelDBKeyRefresh(ctx); err != nil {
		Warn("等待微信数据库密钥刷新退出失败", "err", err)
	}

	resources := c.takeResources()
	err := cleanFridaResources(ctx, resources, true)
	if err != nil {
		err = classifyFridaRuntimeError(
			err,
			FridaErrorClassDetachFailure,
			"分离 Frida 会话失败",
		)
	}

	c.statusMu.Lock()
	if err != nil {
		c.status.State = FridaStateError
		setFridaStatusError(&c.status, err)
	} else {
		c.status.State = FridaStateDetached
		setFridaStatusError(&c.status, nil)
	}
	c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	status := c.status
	c.statusMu.Unlock()
	return status, err
}

func (c *FridaController) AcquireScript() (FridaScriptCaller, func(), error) {
	c.resourcesMu.RLock()
	if c.resources.script == nil {
		c.resourcesMu.RUnlock()
		return nil, nil, ErrFridaUnavailable
	}
	return c.resources.script, c.resourcesMu.RUnlock, nil
}

func (c *FridaController) runLifecycleOperation(
	ctx context.Context,
	kind fridaLifecycleOperationKind,
	request FridaInitializeRequest,
	run func(context.Context) (FridaStatus, error),
) (FridaStatus, error) {
	operation, execute, err := c.operations.begin(ctx, kind, request)
	if err != nil {
		return c.Status(), err
	}
	if !execute {
		return operation.status, operation.err
	}

	c.operationMu.Lock()
	status := c.Status()
	runErr := classifyFridaLifecycleError(
		operation.context.Err(),
		FridaErrorClassCancellation,
		"Frida 生命周期操作已取消",
	)
	if runErr == nil {
		status, runErr = run(operation.context)
	}
	c.operationMu.Unlock()
	c.operations.finish(operation, status, runErr)
	return status, runErr
}

func (c *FridaController) attach(
	ctx context.Context,
	request FridaInitializeRequest,
	reconnect bool,
) (FridaStatus, error) {
	kind := fridaOperationInitialize
	if reconnect {
		kind = fridaOperationReconnect
	}
	return c.runAttach(ctx, kind, request, reconnect, reconnect)
}

func (c *FridaController) runAttach(
	ctx context.Context,
	kind fridaLifecycleOperationKind,
	request FridaInitializeRequest,
	reconnect bool,
	force bool,
) (FridaStatus, error) {
	return c.runAttachWith(
		ctx,
		kind,
		request,
		reconnect,
		force,
		c.hasEquivalentAttachment,
		c.attachResources,
	)
}

func (c *FridaController) runAttachWith(
	ctx context.Context,
	kind fridaLifecycleOperationKind,
	request FridaInitializeRequest,
	reconnect bool,
	force bool,
	equivalent func(FridaInitializeRequest) bool,
	attach fridaAttachRunner,
) (FridaStatus, error) {
	return c.runLifecycleOperation(ctx, kind, request, func(operationContext context.Context) (FridaStatus, error) {
		if !force && equivalent != nil && equivalent(request) {
			return c.Status(), nil
		}
		if !reconnect {
			c.suppressAutoReconnect()
			c.stopReconnect()
		}
		return attach(operationContext, request, reconnect)
	})
}

func (c *FridaController) attachResources(
	ctx context.Context,
	request FridaInitializeRequest,
	reconnect bool,
) (FridaStatus, error) {
	c.statusMu.Lock()
	if reconnect && !c.status.AutoReconnect {
		status := c.status
		c.statusMu.Unlock()
		return status, newFridaLifecycleError(
			FridaErrorClassCancellation,
			"Frida 自动重连已取消",
			context.Canceled,
		)
	}
	c.sessionGeneration++
	generation := c.sessionGeneration
	c.desired = request
	c.status.State = FridaStateAttaching
	if reconnect {
		c.status.State = FridaStateReconnecting
	}
	c.status.Type = request.Type
	c.status.GadgetAddr = request.GadgetAddr
	c.status.WechatPID = request.WechatPID
	c.status.Attached = false
	c.status.ScriptLoaded = false
	c.status.AutoReconnect = true
	setFridaStatusError(&c.status, nil)
	c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	c.statusMu.Unlock()
	if err := c.cancelDBKeyRefresh(ctx); err != nil {
		Warn("等待旧微信数据库密钥刷新退出失败", "err", err)
	}
	c.applyConfig(request)

	oldResources := c.takeResources()
	if err := cleanFridaResources(ctx, oldResources, true); err != nil {
		Warn("清理旧 Frida 资源失败", "err", err)
	}

	resources, pid, err := c.createSessionResources(ctx, request)
	if err != nil {
		_ = cleanFridaResources(context.Background(), resources, true)
		c.statusMu.Lock()
		c.status.State = FridaStateError
		if reconnect {
			c.status.State = FridaStateReconnecting
		} else {
			c.status.AutoReconnect = false
		}
		c.status.WechatPID = pid
		setFridaStatusError(&c.status, err)
		c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status := c.status
		c.statusMu.Unlock()
		return status, err
	}

	resources.script, err = c.loadPublishedScript(ctx, resources)
	if err != nil {
		failedResources := c.takeResources()
		if failedResources.script == nil {
			failedResources.script = resources.script
		}
		_ = cleanFridaResources(context.Background(), failedResources, true)
		err = classifyFridaRuntimeError(
			err,
			FridaErrorClassScriptLoadFailure,
			"加载 Frida 脚本失败",
		)
		c.statusMu.Lock()
		c.status.State = FridaStateError
		if reconnect {
			c.status.State = FridaStateReconnecting
		} else {
			c.status.AutoReconnect = false
		}
		c.status.WechatPID = pid
		setFridaStatusError(&c.status, err)
		c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status := c.status
		c.statusMu.Unlock()
		return status, err
	}

	c.resourcesMu.Lock()
	c.resources.script = resources.script
	c.resourcesMu.Unlock()
	resources.session.On("detached", func(reason frida.SessionDetachReason) {
		go c.handleUnexpectedDetach(generation, reason)
	})
	if resources.session.IsDetached() {
		_ = cleanFridaResources(context.Background(), c.takeResources(), false)
		err = newFridaLifecycleError(
			FridaErrorClassUnexpectedDetach,
			"Frida 会话在脚本加载后立即断开",
			nil,
		)
		c.statusMu.Lock()
		c.status.State = FridaStateError
		c.status.WechatPID = pid
		c.status.AutoReconnect = reconnect
		setFridaStatusError(&c.status, err)
		c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status := c.status
		c.statusMu.Unlock()
		return status, err
	}

	c.statusMu.Lock()
	c.status.State = FridaStateAttached
	c.status.WechatPID = pid
	c.status.Attached = true
	c.status.ScriptLoaded = true
	setFridaStatusError(&c.status, nil)
	c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	status := c.status
	c.statusMu.Unlock()
	c.applyConfig(FridaInitializeRequest{
		Type:       request.Type,
		GadgetAddr: request.GadgetAddr,
		WechatPID:  pid,
	})
	Info("Frida 控制通道已附加", "type", request.Type, "PID", pid, "gadget_addr", request.GadgetAddr)
	if shouldRefreshWXRSKeysForAttachment(request) {
		c.scheduleDBKeyRefresh(generation, pid)
	}
	return status, nil
}

func (c *FridaController) createSessionResources(
	ctx context.Context,
	request FridaInitializeRequest,
) (fridaResources, int, error) {
	resources := fridaResources{manager: frida.NewDeviceManager()}
	pid := request.WechatPID
	var err error

	if request.Type == "gadget" {
		resources.remoteAddr = request.GadgetAddr
		options := frida.NewRemoteDeviceOptions()
		defer options.Clean()
		resources.device, err = resources.manager.AddRemoteDevice(request.GadgetAddr, options)
		if err != nil {
			return resources, 0, classifyFridaRuntimeError(
				err,
				FridaErrorClassDeviceUnavailable,
				"连接 Frida Gadget 失败",
			)
		}
		process, findErr := resources.device.ProcessByName("Gadget", frida.ScopeMinimal)
		if findErr != nil {
			return resources, 0, classifyFridaRuntimeError(
				findErr,
				FridaErrorClassTargetNotFound,
				"未找到 Frida Gadget 进程",
			)
		}
		pid = process.PID()
		process.Clean()
		if pid <= 0 {
			return resources, 0, newFridaLifecycleError(
				FridaErrorClassTargetNotFound,
				"Frida Gadget 未返回有效的微信进程 PID",
				nil,
			)
		}
		resources.session, err = runFridaCancellableCall(
			ctx,
			func(cancellable *frida.Cancellable) (*frida.Session, error) {
				return resources.device.Attach(pid, nil, frida.WithCancel(cancellable))
			},
		)
		if err != nil {
			return resources, pid, classifyFridaRuntimeError(
				err,
				FridaErrorClassAttachFailure,
				"附加 Frida Gadget 进程失败",
			)
		}
	} else {
		resources.device, err = resources.manager.DeviceByType(frida.DeviceTypeLocal)
		if err != nil {
			return resources, 0, classifyFridaRuntimeError(
				err,
				FridaErrorClassDeviceUnavailable,
				"获取本地 Frida 设备失败",
			)
		}
		if pid <= 0 {
			pid, err = GetWeChatPID()
			if err != nil {
				return resources, 0, classifyFridaRuntimeError(
					err,
					FridaErrorClassTargetNotFound,
					"未找到微信进程",
				)
			}
		}
		resources.session, err = runFridaCancellableCall(
			ctx,
			func(cancellable *frida.Cancellable) (*frida.Session, error) {
				return resources.device.Attach(pid, nil, frida.WithCancel(cancellable))
			},
		)
		if err != nil {
			return resources, pid, classifyFridaRuntimeError(
				err,
				FridaErrorClassAttachFailure,
				"附加微信进程失败",
			)
		}
	}
	return resources, pid, nil
}

func (c *FridaController) publishResources(resources fridaResources) {
	c.resourcesMu.Lock()
	c.resources = resources
	c.resourcesMu.Unlock()
}

func (c *FridaController) loadPublishedScript(
	ctx context.Context,
	resources fridaResources,
) (*frida.Script, error) {
	c.publishResources(resources)
	if c.scriptLoader == nil {
		return nil, newFridaLifecycleError(
			FridaErrorClassScriptLoadFailure,
			"Frida 脚本加载器未配置",
			nil,
		)
	}

	cancelLoad := func() {
		if resources.session == nil || resources.session.IsDetached() {
			return
		}
		detachCtx, cancel := context.WithTimeout(context.Background(), fridaDetachTimeout)
		defer cancel()
		if err := detachFridaSession(detachCtx, resources.session); err != nil {
			Warn("取消 Frida 脚本加载时分离会话失败", "err", err)
		}
	}
	return runTrackedOperation(
		ctx,
		cancelLoad,
		func() (*frida.Script, error) {
			return c.scriptLoader(ctx, resources.session)
		},
	)
}

func (c *FridaController) handleUnexpectedDetach(generation uint64, reason frida.SessionDetachReason) {
	c.operationMu.Lock()
	c.statusMu.Lock()
	if generation != c.sessionGeneration || !c.status.AutoReconnect {
		c.statusMu.Unlock()
		c.operationMu.Unlock()
		return
	}
	detachErr := newFridaLifecycleError(
		FridaErrorClassUnexpectedDetach,
		"Frida 会话意外断开",
		errors.New(reason.String()),
	)
	c.status.State = FridaStateReconnecting
	c.status.Attached = false
	c.status.ScriptLoaded = false
	setFridaStatusError(&c.status, detachErr)
	c.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	c.statusMu.Unlock()

	keyRefreshCtx, cancelKeyRefresh := context.WithTimeout(
		context.Background(),
		fridaDetachTimeout,
	)
	if err := c.cancelDBKeyRefresh(keyRefreshCtx); err != nil {
		Warn("等待断线后的微信数据库密钥刷新退出失败", "err", err)
	}
	cancelKeyRefresh()
	_ = cleanFridaResources(context.Background(), c.takeResources(), false)
	c.operationMu.Unlock()
	c.statusMu.RLock()
	autoReconnect := c.status.AutoReconnect
	c.statusMu.RUnlock()
	if autoReconnect {
		c.startReconnect()
	}
}

func (c *FridaController) startReconnect() {
	c.statusMu.RLock()
	autoReconnect := c.status.AutoReconnect
	c.statusMu.RUnlock()
	if !autoReconnect {
		return
	}

	c.reconnectMu.Lock()
	if c.reconnectCancel != nil {
		c.reconnectMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.reconnectID++
	reconnectID := c.reconnectID
	c.reconnectCancel = cancel
	c.reconnectMu.Unlock()

	go func() {
		defer func() {
			restart := false
			c.reconnectMu.Lock()
			if c.reconnectID == reconnectID {
				c.reconnectCancel = nil
				c.statusMu.RLock()
				restart = c.status.AutoReconnect && c.status.State == FridaStateReconnecting
				c.statusMu.RUnlock()
			}
			c.reconnectMu.Unlock()
			if restart {
				c.startReconnect()
			}
		}()

		retryDelay := fridaReconnectDelay
		for {
			if !waitFridaReconnect(ctx, retryDelay) {
				return
			}

			c.statusMu.RLock()
			request := c.desired
			autoReconnect := c.status.AutoReconnect
			c.statusMu.RUnlock()
			if !autoReconnect {
				return
			}
			_, err := c.attach(ctx, request, true)
			if err == nil {
				Info("Frida 自动重连成功")
				return
			}
			if isFridaReconnectCancellation(ctx, err) {
				return
			}
			retryDelay = nextFridaReconnectDelay(retryDelay)
			Debug("Frida 自动重连失败，继续等待", "err", err, "next_retry_in", retryDelay)
		}
	}()
}

func isFridaReconnectCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil ||
		FridaErrorClassOf(err) == FridaErrorClassCancellation ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, frida.ErrContextCancelled)
}

func waitFridaReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextFridaReconnectDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return fridaReconnectDelay
	}
	if delay >= fridaReconnectMaxDelay {
		return fridaReconnectMaxDelay
	}
	next := delay * 2
	if next > fridaReconnectMaxDelay {
		return fridaReconnectMaxDelay
	}
	return next
}

func (c *FridaController) stopReconnect() {
	c.reconnectMu.Lock()
	cancel := c.reconnectCancel
	c.reconnectMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type trackedOperationResult[T any] struct {
	value T
	err   error
}

func runTrackedOperation[T any](
	ctx context.Context,
	cancel func(),
	run func() (T, error),
) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resultChannel := make(chan trackedOperationResult[T], 1)
	go func() {
		value, err := run()
		resultChannel <- trackedOperationResult[T]{value: value, err: err}
	}()

	select {
	case result := <-resultChannel:
		return result.value, result.err
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		result := <-resultChannel
		return result.value, ctx.Err()
	}
}

func runFridaCancellableCall[T any](
	ctx context.Context,
	run func(*frida.Cancellable) (T, error),
) (T, error) {
	cancellable := frida.NewCancellable()
	defer cancellable.Unref()
	return runTrackedOperation(
		ctx,
		cancellable.Cancel,
		func() (T, error) {
			return run(cancellable)
		},
	)
}

func (c *FridaController) takeResources() fridaResources {
	c.resourcesMu.Lock()
	resources := c.resources
	c.resources = fridaResources{}
	c.resourcesMu.Unlock()
	return resources
}

func (c *FridaController) hasEquivalentAttachment(request FridaInitializeRequest) bool {
	c.statusMu.RLock()
	equivalent := c.status.State == FridaStateAttached &&
		c.status.Attached &&
		c.status.ScriptLoaded &&
		c.desired == request
	c.statusMu.RUnlock()
	if !equivalent {
		return false
	}
	c.resourcesMu.RLock()
	hasScript := c.resources.script != nil &&
		c.resources.session != nil &&
		!c.resources.session.IsDetached()
	c.resourcesMu.RUnlock()
	return hasScript
}

func (c *FridaController) applyConfig(request FridaInitializeRequest) {
	if c.config == nil {
		return
	}
	c.config.FridaType = request.Type
	c.config.FridaGadgetAddr = request.GadgetAddr
	c.config.WechatPid = request.WechatPID
}

func (c *FridaController) suppressAutoReconnect() {
	c.statusMu.Lock()
	c.status.AutoReconnect = false
	c.statusMu.Unlock()
}

func cleanFridaResources(ctx context.Context, resources fridaResources, detach bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, fridaDetachTimeout)
	defer cancel()

	var firstErr error
	if detach {
		if resources.remoteAddr != "" {
			if err := shutdownGadgetTransport(cleanupCtx, resources.remoteAddr); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if resources.session != nil && !resources.session.IsDetached() {
			if err := detachFridaSession(cleanupCtx, resources.session); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if resources.script != nil {
		if resources.session == nil && !resources.script.IsDestroyed() {
			if err := resources.script.Unload(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		resources.script.Clean()
	}
	if resources.session != nil {
		resources.session.Clean()
	}
	if resources.device != nil {
		resources.device.Clean()
	}
	if resources.manager != nil {
		resources.manager.Clean()
	}
	return firstErr
}

func detachFridaSession(ctx context.Context, session *frida.Session) error {
	if session == nil || session.IsDetached() {
		return nil
	}
	// frida-go's DetachWithContext returns before its sync call exits on cancellation.
	_, err := runFridaCancellableCall(
		ctx,
		func(cancellable *frida.Cancellable) (struct{}, error) {
			return struct{}{}, session.Detach(frida.WithCancel(cancellable))
		},
	)
	return err
}

func shutdownGadgetTransport(ctx context.Context, address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	targetIPs := make([]net.IP, 0, len(resolved))
	for _, address := range resolved {
		targetIPs = append(targetIPs, address.IP)
	}

	maxFD := uint64(1024)
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		maxFD = limit.Cur
		if maxFD > 65536 {
			maxFD = 65536
		}
	}

	matches := 0
	var firstErr error
	for fd := 3; fd < int(maxFD); fd++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		peerIP, peerPort, ok := socketPeer(fd)
		if !ok || peerPort != port || !matchesAnyIP(peerIP, targetIPs) {
			continue
		}
		matches++
		if err := syscall.Shutdown(fd, syscall.SHUT_RDWR); err != nil && !errors.Is(err, syscall.ENOTCONN) && firstErr == nil {
			firstErr = err
		}
	}
	if matches == 0 {
		return fmt.Errorf("未找到连接到 %s 的 Gadget 传输", address)
	}
	return firstErr
}

func socketPeer(fd int) (net.IP, int, bool) {
	peer, err := syscall.Getpeername(fd)
	if err != nil {
		return nil, 0, false
	}
	switch value := peer.(type) {
	case *syscall.SockaddrInet4:
		return net.IP(value.Addr[:]), value.Port, true
	case *syscall.SockaddrInet6:
		return net.IP(value.Addr[:]), value.Port, true
	default:
		return nil, 0, false
	}
}

func matchesAnyIP(candidate net.IP, targets []net.IP) bool {
	for _, target := range targets {
		if candidate.Equal(target) {
			return true
		}
	}
	return false
}

func classifyFridaRuntimeError(
	err error,
	fallback FridaErrorClass,
	message string,
) error {
	if errors.Is(err, frida.ErrContextCancelled) {
		return newFridaLifecycleError(
			FridaErrorClassCancellation,
			"Frida 生命周期操作已取消",
			err,
		)
	}
	return classifyFridaLifecycleError(err, fallback, message)
}
