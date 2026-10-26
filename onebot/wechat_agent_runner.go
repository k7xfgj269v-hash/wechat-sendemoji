package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func RunWechatAgent(ctx context.Context, cfg *Config) (runErr error) {
	if ctx == nil || cfg == nil {
		return errors.New("wechat agent context and config are required")
	}
	agentCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runDirectory, socketPath, err := resolveWechatAgentPaths(cfg)
	if err != nil {
		return err
	}
	if err := ensureWechatAgentRunDirectory(runDirectory); err != nil {
		return err
	}
	cfg.RunDir = runDirectory
	cfg.AgentSocket = socketPath
	lock, err := AcquireProcessLock(filepath.Join(runDirectory, "wechat-agent.lock"))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Close(); runErr == nil {
			runErr = err
		}
	}()
	if err := PrepareUnixSocket(socketPath, uint32(os.Geteuid())); err != nil {
		return err
	}
	eventStore, err := OpenWechatAgentEventStore(
		filepath.Join(runDirectory, defaultWechatAgentEventName),
		DefaultWechatAgentEventRetention,
	)
	if err != nil {
		return err
	}
	attachments, err := OpenFileWechatAgentAttachmentStore(
		filepath.Join(runDirectory, defaultWechatAgentAttachmentDir),
	)
	if err != nil {
		_ = eventStore.Close()
		return err
	}
	contacts := NewContactCacheWechatAgentAdapter(globalContactCache)
	runtime, err := NewWechatAgentRuntime(WechatAgentRuntimeDependencies{
		Contacts:           contacts,
		Frida:              hookController,
		Attachments:        attachments,
		Delivery:           NewQueueWechatAgentDelivery(msgChan),
		Events:             eventStore,
		DeliveryLedgerPath: filepath.Join(runDirectory, defaultWechatAgentDeliveryName),
	})
	if err != nil {
		_ = attachments.Close()
		_ = eventStore.Close()
		return err
	}
	restoreForward := installWechatAgentForwardRuntime(runtime)
	defer restoreForward()
	defer runtime.Close()
	defer func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), fridaDetachTimeout)
		defer cancel()
		if _, err := hookController.Detach(detachCtx); err != nil {
			Error("Frida 资源释放失败", "err", err)
		}
	}()
	go func() {
		if _, err := contacts.RefreshContacts(agentCtx); err != nil {
			Error("联系人缓存初始加载失败", "err", err)
		}
	}()
	go func() {
		if _, err := hookController.Initialize(agentCtx, FridaInitializeRequest{
			Type:       cfg.FridaType,
			GadgetAddr: cfg.FridaGadgetAddr,
			WechatPID:  cfg.WechatPid,
		}); err != nil {
			Error("Frida 初始附加失败", "err", err)
		}
	}()
	go cleanExpiredDownloads()

	listener, err := listenWechatAgentUnix(socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunSendWorker(agentCtx, msgChan)
	}()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewWechatAgentServer(runtime).Serve(agentCtx, listener)
	}()

	var serverErr, workerErr error
	select {
	case serverErr = <-serverDone:
		cancel()
		workerErr = <-workerDone
	case workerErr = <-workerDone:
		cancel()
		serverErr = <-serverDone
	}
	if ctx.Err() != nil {
		return nil
	}
	if serverErr != nil && !errors.Is(serverErr, context.Canceled) {
		return serverErr
	}
	if workerErr != nil && !errors.Is(workerErr, context.Canceled) {
		return workerErr
	}
	return nil
}
