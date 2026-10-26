package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hrWorkbenchShutdownTimeout = 3 * time.Second

type HRWorkbenchProcessDependencies struct {
	Agent                AgentClient
	Listen               func(string, string) (net.Listener, error)
	OpenCRM              func(string) (*CRMStore, error)
	InitializeAutomation func(*Config, *CRMStore) (*AutomationRuntime, error)
	OpenCursor           func(string) (*HRWorkbenchAgentCursorStore, error)
}

func RunHRWorkbench(ctx context.Context, cfg *Config) (runErr error) {
	if ctx == nil || cfg == nil {
		return errors.New("hr workbench context and config are required")
	}
	runDirectory, socketPath, err := resolveHRWorkbenchPaths(cfg)
	if err != nil {
		return err
	}
	if err := ensureHRWorkbenchRunDirectory(runDirectory); err != nil {
		return err
	}
	cfg.RunDir = runDirectory
	cfg.AgentSocket = socketPath
	lock, err := AcquireProcessLock(filepath.Join(runDirectory, "hr-workbench.lock"))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Close(); runErr == nil {
			runErr = err
		}
	}()
	return RunHRWorkbenchWithDependencies(ctx, cfg, HRWorkbenchProcessDependencies{
		Agent: NewUnixAgentClient(socketPath),
	})
}

func RunHRWorkbenchWithDependencies(
	ctx context.Context,
	cfg *Config,
	dependencies HRWorkbenchProcessDependencies,
) error {
	if ctx == nil || cfg == nil || dependencies.Agent == nil {
		return errors.New("hr workbench dependencies are incomplete")
	}
	if strings.TrimSpace(cfg.ReceiveHost) == "" {
		cfg.ReceiveHost = defaultReceiveHost
	}
	if err := validateHRWorkbenchListenAddress(cfg.ReceiveHost); err != nil {
		_ = dependencies.Agent.Close()
		return err
	}
	if dependencies.Listen == nil {
		dependencies.Listen = net.Listen
	}
	if dependencies.OpenCRM == nil {
		dependencies.OpenCRM = OpenCRMStore
	}
	if dependencies.InitializeAutomation == nil {
		dependencies.InitializeAutomation = initializeAutomationRuntime
	}
	if dependencies.OpenCursor == nil {
		dependencies.OpenCursor = OpenHRWorkbenchAgentCursorStore
	}

	runDirectory, _, err := resolveHRWorkbenchPaths(cfg)
	if err != nil {
		_ = dependencies.Agent.Close()
		return err
	}
	if err := ensureHRWorkbenchRunDirectory(runDirectory); err != nil {
		_ = dependencies.Agent.Close()
		return err
	}
	cursorPath := filepath.Join(runDirectory, defaultHRWorkbenchAgentCursorFileName)

	dataPath := strings.TrimSpace(cfg.CRMDataPath)
	if dataPath == "" {
		dataPath, err = DefaultCRMDataPath()
		if err != nil {
			_ = dependencies.Agent.Close()
			return err
		}
	}
	crm, err := dependencies.OpenCRM(dataPath)
	if err != nil {
		_ = dependencies.Agent.Close()
		return err
	}
	cfg.CRMDataPath = dataPath
	crmMessageSetStore(crm)
	rules := NewForwardRuleStore(crm)
	forwardRuleStore = rules

	restoreAgent := installHRWorkbenchAgentClient(dependencies.Agent)
	automation, err := dependencies.InitializeAutomation(cfg, crm)
	if err != nil {
		restoreAgent()
		crmMessageSetStore(nil)
		_ = crm.Close()
		_ = dependencies.Agent.Close()
		return err
	}
	cursor, err := dependencies.OpenCursor(cursorPath)
	if err != nil {
		restoreAgent()
		if automation != nil {
			_ = automation.Close()
		}
		crmMessageSetStore(nil)
		_ = crm.Close()
		_ = dependencies.Agent.Close()
		return err
	}

	listener, err := dependencies.Listen("tcp", cfg.ReceiveHost)
	if err != nil {
		restoreAgent()
		if automation != nil {
			_ = automation.Close()
		}
		crmMessageSetStore(nil)
		_ = crm.Close()
		_ = dependencies.Agent.Close()
		return err
	}

	processCtx, cancel := context.WithCancel(ctx)
	subscription := NewHRWorkbenchInboundSubscription(dependencies.Agent, crm, cursor)
	mux := http.NewServeMux()
	registerHRWorkbenchHTTPRoutes(mux, crm, rules, automation, dependencies.Agent, subscription)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	subscriptionDone := make(chan error, 1)
	go func() {
		subscriptionDone <- subscription.Run(processCtx)
	}()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve(listener)
	}()

	var runErr error
	subscriptionFinished := false
	serverFinished := false
	select {
	case <-ctx.Done():
	case err := <-subscriptionDone:
		subscriptionFinished = true
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = err
		}
	case err := <-serverDone:
		serverFinished = true
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), hrWorkbenchShutdownTimeout)
	_ = server.Shutdown(shutdownCtx)
	shutdownCancel()
	cancel()
	if !serverFinished {
		if err := <-serverDone; err != nil && !errors.Is(err, http.ErrServerClosed) && runErr == nil {
			runErr = err
		}
	}
	if !subscriptionFinished {
		if err := <-subscriptionDone; err != nil && !errors.Is(err, context.Canceled) && runErr == nil {
			runErr = err
		}
	}
	restoreAgent()
	if automation != nil {
		if err := automation.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}
	crmMessageSetStore(nil)
	if err := crm.Close(); err != nil && runErr == nil {
		runErr = err
	}
	if err := dependencies.Agent.Close(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func registerHRWorkbenchHTTPRoutes(
	mux *http.ServeMux,
	store *CRMStore,
	rules *ForwardRuleStore,
	automation *AutomationRuntime,
	agent AgentClient,
	subscription *HRWorkbenchInboundSubscription,
) {
	registerHTTPRouteModules(httpRouteRegistrationContext{
		mux:          mux,
		store:        store,
		rules:        rules,
		automation:   automation,
		backend:      httpRouteBackendAgent,
		agent:        agent,
		subscription: subscription,
	})
}

func hrWorkbenchAgentEventStatusHandler(
	subscription *HRWorkbenchInboundSubscription,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !automationLoopbackRequest(r) {
			writeAPIError(w, http.StatusForbidden, "仅允许本机访问")
			return
		}
		if subscription == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "消息订阅不可用")
			return
		}
		writeJSON(w, http.StatusOK, subscription.State())
	}
}

func resolveHRWorkbenchPaths(cfg *Config) (string, string, error) {
	defaultRunDirectory, defaultSocketPath, err := defaultWechatAgentPaths()
	if err != nil {
		return "", "", err
	}
	runDirectory := strings.TrimSpace(cfg.RunDir)
	if runDirectory == "" {
		runDirectory = defaultRunDirectory
	}
	if !filepath.IsAbs(runDirectory) || filepath.Clean(runDirectory) != runDirectory {
		return "", "", errors.New("hr workbench run directory is unsafe")
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

func ensureHRWorkbenchRunDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("hr workbench run directory is unsafe")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("hr workbench run directory is unsafe")
	}
	if uid, ok := agentFileOwner(info); !ok || uid != uint32(os.Geteuid()) {
		return errors.New("hr workbench run directory is unsafe")
	}
	return os.Chmod(path, 0700)
}

func validateHRWorkbenchListenAddress(address string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return errors.New("hr workbench listen address is invalid")
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("hr workbench must listen on loopback")
	}
	return nil
}
