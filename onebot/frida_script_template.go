package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

type fridaScriptTemplate struct {
	template     *template.Template
	requiredKeys []string
}

type decodedFridaMessage struct {
	Type        string
	Payload     json.RawMessage
	Description string
	Stack       string
}

func decodeWechatHookConfig(jsonData []byte) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析微信 Hook 配置失败: %w", err)
	}
	if raw == nil {
		return nil, errors.New("微信 Hook 配置根节点必须是对象")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, errors.New("微信 Hook 配置只能包含一个 JSON 对象")
	}

	config := make(map[string]string, len(raw))
	for key, value := range raw {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("微信 Hook 配置字段 %q 不能为 null", key)
		}
		var scalar string
		if err := json.Unmarshal(value, &scalar); err != nil {
			return nil, fmt.Errorf("微信 Hook 配置字段 %q 必须是字符串", key)
		}
		config[key] = scalar
	}
	return config, nil
}

func parseFridaScriptTemplate(codeTemplate []byte) (*fridaScriptTemplate, error) {
	tmpl, err := template.New("fridaScript").
		Option("missingkey=error").
		Parse(string(codeTemplate))
	if err != nil {
		return nil, fmt.Errorf("解析 Frida 脚本模板失败: %w", err)
	}

	requiredSet := make(map[string]struct{})
	for _, parsedTemplate := range tmpl.Templates() {
		if parsedTemplate.Tree == nil || parsedTemplate.Tree.Root == nil {
			continue
		}
		collectFridaTemplateKeys(parsedTemplate.Tree.Root, requiredSet)
	}
	requiredKeys := make([]string, 0, len(requiredSet))
	for key := range requiredSet {
		requiredKeys = append(requiredKeys, key)
	}
	sort.Strings(requiredKeys)
	return &fridaScriptTemplate{
		template:     tmpl,
		requiredKeys: requiredKeys,
	}, nil
}

func renderFridaScriptTemplate(codeTemplate, jsonData []byte) (string, error) {
	config, err := decodeWechatHookConfig(jsonData)
	if err != nil {
		return "", err
	}
	parsed, err := parseFridaScriptTemplate(codeTemplate)
	if err != nil {
		return "", err
	}
	if err := validateWechatHookConfig(config, parsed.requiredKeys); err != nil {
		return "", err
	}

	var rendered bytes.Buffer
	if err := parsed.template.Execute(&rendered, config); err != nil {
		return "", fmt.Errorf("渲染 Frida 脚本失败: %w", err)
	}
	return rendered.String(), nil
}

func validateWechatHookConfig(config map[string]string, requiredKeys []string) error {
	required := make(map[string]struct{}, len(requiredKeys))
	for _, key := range requiredKeys {
		required[key] = struct{}{}
		value, ok := config[key]
		if !ok {
			return fmt.Errorf("微信 Hook 配置缺少模板字段 %q", key)
		}
		if err := validateFridaOffset(key, value); err != nil {
			return err
		}
	}
	for key, value := range config {
		if _, ok := required[key]; ok {
			continue
		}
		if isFridaOffsetLike(key, value) {
			if err := validateFridaOffset(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFridaOffset(key, value string) error {
	if len(value) < 3 || !strings.EqualFold(value[:2], "0x") {
		return fmt.Errorf("微信 Hook 配置字段 %q 必须是非零十六进制偏移", key)
	}
	digits := value[2:]
	for _, digit := range digits {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return fmt.Errorf("微信 Hook 配置字段 %q 必须是非零十六进制偏移", key)
		}
	}
	offset, err := strconv.ParseUint(digits, 16, 63)
	if err != nil {
		return fmt.Errorf("微信 Hook 配置字段 %q 超出 int64 范围", key)
	}
	if offset == 0 {
		return fmt.Errorf("微信 Hook 配置字段 %q 不能为零偏移", key)
	}
	return nil
}

func isFridaOffsetLike(key, value string) bool {
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		return true
	}
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	for _, suffix := range []string{"addr", "address", "offset"} {
		if strings.HasSuffix(lowerKey, suffix) {
			return true
		}
	}
	return false
}

func decodeFridaMessage(rawMessage string) (decodedFridaMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawMessage), &raw); err != nil {
		return decodedFridaMessage{}, fmt.Errorf("解析 Frida 消息失败: %w", err)
	}
	if raw == nil {
		return decodedFridaMessage{}, errors.New("Frida 消息根节点必须是对象")
	}

	var message decodedFridaMessage
	if value, ok := raw["type"]; !ok {
		return decodedFridaMessage{}, errors.New("Frida 消息缺少 type")
	} else if err := json.Unmarshal(value, &message.Type); err != nil || message.Type == "" {
		return decodedFridaMessage{}, errors.New("Frida 消息 type 必须是非空字符串")
	}
	message.Payload = raw["payload"]
	if value, ok := raw["description"]; ok {
		_ = json.Unmarshal(value, &message.Description)
	}
	if value, ok := raw["stack"]; ok {
		_ = json.Unmarshal(value, &message.Stack)
	}
	return message, nil
}

func decodeFridaSendPayload(rawPayload json.RawMessage) (map[string]interface{}, error) {
	if len(rawPayload) == 0 {
		return nil, errors.New("Frida send 消息缺少 payload")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, fmt.Errorf("解析 Frida send payload 失败: %w", err)
	}
	if payload == nil {
		return nil, errors.New("Frida send payload 必须是对象")
	}
	if _, err := requiredFridaPayloadString(payload, "type"); err != nil {
		return nil, err
	}
	return payload, nil
}

func requiredFridaPayloadString(payload map[string]interface{}, key string) (string, error) {
	value, ok := payload[key]
	if !ok {
		return "", fmt.Errorf("Frida payload 缺少 %s", key)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("Frida payload 字段 %s 必须是非空字符串", key)
	}
	return text, nil
}

func optionalFridaPayloadString(payload map[string]interface{}, key string) (string, error) {
	value, ok := payload[key]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("Frida payload 字段 %s 必须是字符串", key)
	}
	return text, nil
}

func optionalFridaPayloadInt32(payload map[string]interface{}, key string) (int32, error) {
	value, ok := payload[key]
	if !ok {
		return 0, nil
	}
	number, ok := value.(float64)
	if !ok || math.Trunc(number) != number || number < math.MinInt32 || number > math.MaxInt32 {
		return 0, fmt.Errorf("Frida payload 字段 %s 必须是 int32", key)
	}
	return int32(number), nil
}

func fridaPayloadBytes(payload map[string]interface{}, key string) ([]byte, error) {
	value, ok := payload[key]
	if !ok {
		return nil, fmt.Errorf("Frida payload 缺少 %s", key)
	}
	values, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("Frida payload 字段 %s 必须是字节数组", key)
	}
	result := make([]byte, len(values))
	for i, value := range values {
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || number < 0 || number > 255 {
			return nil, fmt.Errorf("Frida payload 字段 %s[%d] 必须是字节", key, i)
		}
		result[i] = byte(number)
	}
	return result, nil
}

func collectFridaTemplateKeys(node parse.Node, keys map[string]struct{}) {
	switch value := node.(type) {
	case *parse.ListNode:
		for _, child := range value.Nodes {
			collectFridaTemplateKeys(child, keys)
		}
	case *parse.ActionNode:
		collectFridaPipeKeys(value.Pipe, keys)
	case *parse.IfNode:
		collectFridaBranchKeys(&value.BranchNode, keys)
	case *parse.RangeNode:
		collectFridaBranchKeys(&value.BranchNode, keys)
	case *parse.WithNode:
		collectFridaBranchKeys(&value.BranchNode, keys)
	case *parse.TemplateNode:
		collectFridaPipeKeys(value.Pipe, keys)
	}
}

func collectFridaBranchKeys(branch *parse.BranchNode, keys map[string]struct{}) {
	if branch == nil {
		return
	}
	collectFridaPipeKeys(branch.Pipe, keys)
	collectFridaTemplateKeys(branch.List, keys)
	collectFridaTemplateKeys(branch.ElseList, keys)
}

func collectFridaPipeKeys(pipe *parse.PipeNode, keys map[string]struct{}) {
	if pipe == nil {
		return
	}
	for _, command := range pipe.Cmds {
		for _, argument := range command.Args {
			collectFridaArgumentKeys(argument, keys)
		}
	}
}

func collectFridaArgumentKeys(node parse.Node, keys map[string]struct{}) {
	switch value := node.(type) {
	case *parse.FieldNode:
		if len(value.Ident) > 0 {
			keys[value.Ident[0]] = struct{}{}
		}
	case *parse.ChainNode:
		collectFridaArgumentKeys(value.Node, keys)
	case *parse.PipeNode:
		collectFridaPipeKeys(value, keys)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
