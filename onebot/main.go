package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const defaultReceiveHost = "127.0.0.1:58080"

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runMain() error {
	mode, err := initFlag()
	if err != nil {
		return err
	}
	initLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch mode {
	case ProcessModeWechatAgent:
		err = RunWechatAgent(ctx, config)
	case ProcessModeHRWorkbench:
		err = RunHRWorkbench(ctx, config)
	default:
		return errors.New("process mode is invalid")
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func initializeCRMRuntime(cfg *Config) (*CRMStore, error) {
	if cfg == nil {
		return nil, NewCRMValidationError("config", "is required")
	}
	if strings.TrimSpace(cfg.ReceiveHost) == "" {
		cfg.ReceiveHost = defaultReceiveHost
	}

	dataPath := strings.TrimSpace(cfg.CRMDataPath)
	if dataPath == "" {
		var err error
		dataPath, err = DefaultCRMDataPath()
		if err != nil {
			return nil, err
		}
	}

	store, err := OpenCRMStore(dataPath)
	if err != nil {
		return nil, err
	}
	cfg.CRMDataPath = dataPath
	crmMessageSetStore(store)
	forwardRuleStore = NewForwardRuleStore(store)
	return store, nil
}

func registerHTTPRoutes(
	mux *http.ServeMux,
	store *CRMStore,
	rules *ForwardRuleStore,
	automations ...*AutomationRuntime,
) {
	var automation *AutomationRuntime
	if len(automations) > 0 {
		automation = automations[0]
	}
	registerHTTPRouteModules(httpRouteRegistrationContext{
		mux:        mux,
		store:      store,
		rules:      rules,
		automation: automation,
		backend:    httpRouteBackendLegacy,
	})
}

func initFlag() (ProcessMode, error) {
	var modeValue string
	flag.StringVar(&modeValue, "mode", "", "进程模式: wechat-agent | hr-workbench")
	flag.StringVar(&config.AgentSocket, "agent_socket", "", "wechat-agent Unix socket 路径")
	flag.StringVar(&config.RunDir, "run_dir", "", "进程锁和 socket 的运行目录")
	flag.StringVar(&config.FridaType, "type", "local", "frida 类型: local | gadget")
	flag.StringVar(&config.SendURL, "send_url", "http://127.0.0.1:36060/onebot", "发送消息的 URL: http://127.0.0.1:36060/onebot")
	flag.StringVar(&config.ReceiveHost, "receive_host", defaultReceiveHost, "接收消息的地址: 127.0.0.1:58080")
	flag.StringVar(&config.CRMDataPath, "crm_data_path", "", "CRM 持久化快照路径，默认 $HOME/.wechat-chatter/crm.json")
	flag.StringVar(&config.AutomationDataDir, "automation_data_dir", "", "AI 自动化数据目录，默认 $HOME/.wechat-chatter/automation")
	flag.StringVar(&config.FridaGadgetAddr, "gadget_addr", "127.0.0.1:27042", "Gadget 地址: 127.0.0.1:27042 仅当 type 为 gadget 时有效")
	flag.StringVar(&config.OnebotToken, "token", "MuseBot", "OneBot Token: MuseBot")
	flag.StringVar(&config.ImagePath, "image_path", "", "图片路径: /Users/xxx/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/xxx/temp/xxx/2026-01/Img/")
	flag.StringVar(&config.WechatConf, "wechat_conf", "../wechat_version/4_1_11_53_mac.json", "微信配置文件路径: ../wechat_version/4_1_6_12_mac.json")
	flag.StringVar(&config.ConnType, "conn_type", "http", "连接类型: http | websocket")
	flag.IntVar(&config.SendInterval, "send_interval", 1000, "发送间隔: ms")
	flag.IntVar(&config.WechatPid, "wechat_pid", 0, "微信进程 PID，不设置则自动查找")
	flag.StringVar(&config.SelfWxid, "self_wxid", "", "你的 wxid（判断消息方向 in/out，默认从 image_path 推导）")
	flag.StringVar(&config.Sink, "sink", "", "转发下游: '' 走旧POST | file:/path | fifo:/path | webhook:URL")
	flag.IntVar(&config.SendTimeout, "send_timeout", 15, "Frida 导出调用超时: 秒")
	flag.StringVar(&logLevel, "log_level", "info", "log level")

	flag.Parse()
	mode, err := ResolveProcessMode(modeValue, os.Args[0], buildProcessMode)
	if err != nil {
		return "", err
	}
	explicit := make(map[string]bool)
	flag.Visit(func(current *flag.Flag) {
		explicit[current.Name] = true
	})
	if err := validateExplicitProcessFlags(mode, explicit); err != nil {
		return "", err
	}

	if myWechatId == "" && config.ImagePath != "" {
		if idx := strings.Index(config.ImagePath, "xwechat_files/"); idx != -1 {
			rest := config.ImagePath[idx+len("xwechat_files/"):]
			if end := strings.Index(rest, "/"); end != -1 {
				rest = rest[:end]
			}
			// 去掉末尾的 "_xxxx" 后缀，保留 wxid_xxx 部分
			if last := strings.LastIndex(rest, "_"); last > strings.Index(rest, "_") {
				myWechatId = rest[:last]
			}
		}
	}
	if config.SelfWxid == "" {
		config.SelfWxid = myWechatId
	}
	if config.SendTimeout <= 0 {
		config.SendTimeout = 15
	}

	fmt.Println("FridaType", config.FridaType)
	fmt.Println("SendURL", config.SendURL)
	fmt.Println("ReceiveHost", config.ReceiveHost)
	fmt.Println("FridaGadgetAddr", config.FridaGadgetAddr)
	fmt.Println("OnebotToken", config.OnebotToken)
	fmt.Println("ImagePath", config.ImagePath)
	fmt.Println("WechatConf", config.WechatConf)
	fmt.Println("ConnType", config.ConnType)
	fmt.Println("SendInterval", config.SendInterval)
	fmt.Println("WechatPid", config.WechatPid)
	fmt.Println("SelfWxid", config.SelfWxid)
	fmt.Println("Sink", config.Sink)
	fmt.Println("SendTimeout", config.SendTimeout)
	fmt.Println("LogLevel", logLevel)
	fmt.Println("ProcessMode", mode)
	fmt.Println("AgentSocket", config.AgentSocket)
	fmt.Println("RunDir", config.RunDir)
	return mode, nil
}
