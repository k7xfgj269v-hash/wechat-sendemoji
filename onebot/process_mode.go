package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type ProcessMode string

const (
	ProcessModeWechatAgent ProcessMode = "wechat-agent"
	ProcessModeHRWorkbench ProcessMode = "hr-workbench"
)

var buildProcessMode string

func ResolveProcessMode(flagValue, executableName, buildValue string) (ProcessMode, error) {
	explicitMode, err := parseProcessMode(flagValue)
	if err != nil {
		return "", err
	}
	taggedMode, err := parseProcessMode(buildValue)
	if err != nil {
		return "", fmt.Errorf("invalid build process mode: %w", err)
	}
	if taggedMode != "" {
		if explicitMode != "" && explicitMode != taggedMode {
			return "", fmt.Errorf("mode %q conflicts with tagged binary mode %q", explicitMode, taggedMode)
		}
		return taggedMode, nil
	}
	if explicitMode != "" {
		return explicitMode, nil
	}

	switch filepath.Base(strings.TrimSpace(executableName)) {
	case string(ProcessModeWechatAgent):
		return ProcessModeWechatAgent, nil
	case string(ProcessModeHRWorkbench), "onebot-workbench", "onebot":
		return ProcessModeHRWorkbench, nil
	default:
		return ProcessModeHRWorkbench, nil
	}
}

func parseProcessMode(value string) (ProcessMode, error) {
	mode := ProcessMode(strings.TrimSpace(value))
	switch mode {
	case "":
		return "", nil
	case ProcessModeWechatAgent, ProcessModeHRWorkbench:
		return mode, nil
	default:
		return "", errors.New("mode must be wechat-agent or hr-workbench")
	}
}

func validateExplicitProcessFlags(mode ProcessMode, explicit map[string]bool) error {
	var forbidden []string
	switch mode {
	case ProcessModeWechatAgent:
		forbidden = []string{"receive_host", "crm_data_path", "automation_data_dir"}
	case ProcessModeHRWorkbench:
		forbidden = []string{
			"type",
			"send_url",
			"gadget_addr",
			"token",
			"image_path",
			"wechat_conf",
			"conn_type",
			"send_interval",
			"wechat_pid",
			"self_wxid",
			"sink",
			"send_timeout",
		}
	default:
		return errors.New("process mode is invalid")
	}
	for _, name := range forbidden {
		if explicit[name] {
			return fmt.Errorf("flag -%s is not valid in %s mode", name, mode)
		}
	}
	return nil
}
