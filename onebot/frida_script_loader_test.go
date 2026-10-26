//go:build !noFrida

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"
)

func TestLoadFridaScriptWithContextRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	script, err := loadFridaScriptWithContext(ctx, nil)
	if script != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFridaScriptWithContext() = (%#v, %v), want canceled", script, err)
	}
}

func TestDecodeWechatHookConfigAcceptsHistoricalSchemas(t *testing.T) {
	paths, err := filepath.Glob("../wechat_version/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no historical WeChat configurations found")
	}

	seenCounts := make(map[int]bool)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			config, decodeErr := decodeWechatHookConfig(data)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if len(config) < 13 || len(config) > 24 {
				t.Fatalf("field count = %d, want 13..24", len(config))
			}
			seenCounts[len(config)] = true
			for key, value := range config {
				if key == "" || value == "" {
					t.Fatalf("empty historical field %q=%q", key, value)
				}
			}
		})
	}
	if !seenCounts[13] || !seenCounts[24] {
		t.Fatalf("historical field counts = %v, want both 13 and 24", seenCounts)
	}
}

func TestRenderFridaScriptTemplateAcceptsVersionTolerantScalarKeys(t *testing.T) {
	codeTemplate := []byte(`const target = ptr("{{.requiredAddr}}");`)
	for _, fieldCount := range []int{13, 24} {
		t.Run(fmt.Sprintf("%d_fields", fieldCount), func(t *testing.T) {
			config := map[string]string{"requiredAddr": "0x1"}
			for i := 1; i < fieldCount; i++ {
				config[fmt.Sprintf("metadata_%02d", i)] = fmt.Sprintf("value-%02d", i)
			}
			jsonData, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := renderFridaScriptTemplate(codeTemplate, jsonData)
			if err != nil {
				t.Fatal(err)
			}
			if rendered != `const target = ptr("0x1");` {
				t.Fatalf("rendered script = %q", rendered)
			}
		})
	}
}

func TestDecodeWechatHookConfigRejectsInvalidShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "array root", data: `[]`},
		{name: "string root", data: `"config"`},
		{name: "number root", data: `42`},
		{name: "null root", data: `null`},
		{name: "nested object", data: `{"offset":{"value":"0x1"}}`},
		{name: "nested array", data: `{"offset":["0x1"]}`},
		{name: "number value", data: `{"offset":1}`},
		{name: "boolean value", data: `{"offset":true}`},
		{name: "null value", data: `{"offset":null}`},
		{name: "trailing object", data: `{"offset":"0x1"} {"other":"0x2"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if config, err := decodeWechatHookConfig([]byte(tc.data)); err == nil {
				t.Fatalf("decodeWechatHookConfig() = %#v, want rejection", config)
			}
		})
	}
}

func TestParseFridaScriptTemplateFindsFrozenPlaceholders(t *testing.T) {
	codeTemplate, err := os.ReadFile("./script.js")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFridaScriptTemplate(codeTemplate)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"autoBufferWriteFunc",
		"blrX8Addr",
		"buf2RespAddr",
		"cndOnCompleteAddr",
		"downloadFileAddr",
		"downloadImagAddr",
		"downloadVideoAddr",
		"req2bufEnterAddr",
		"req2bufExitAddr",
		"sendFuncAddr",
		"startDownloadMedia",
		"uploadGetCallbackWrapperAddr",
		"uploadGetCallbackWrapperFuncAddr",
		"uploadImageAddr",
		"uploadOnCompleteAddr",
		"uploadOnCompleteFuncAddr",
	}
	if !reflect.DeepEqual(parsed.requiredKeys, want) {
		t.Fatalf("required placeholders = %#v, want %#v", parsed.requiredKeys, want)
	}
}

func TestRenderFridaScriptTemplateRejectsMissingRequiredPlaceholder(t *testing.T) {
	_, err := renderFridaScriptTemplate(
		[]byte(`const first = {{.firstAddr}}; const second = {{.secondAddr}};`),
		[]byte(`{"firstAddr":"0x1"}`),
	)
	if err == nil || !strings.Contains(err.Error(), `"secondAddr"`) {
		t.Fatalf("missing placeholder error = %v", err)
	}
}

func TestFridaScriptTemplateRetainsMissingKeyErrors(t *testing.T) {
	parsed, err := parseFridaScriptTemplate([]byte(`const target = {{.missingAddr}};`))
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	err = parsed.template.Execute(&rendered, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missingAddr") {
		t.Fatalf("template missing-key error = %v", err)
	}
}

func TestValidateFridaOffset(t *testing.T) {
	for _, value := range []string{"0x1", "0X2A", "0x7fffffffffffffff"} {
		t.Run("valid_"+value, func(t *testing.T) {
			if err := validateFridaOffset("offset", value); err != nil {
				t.Fatalf("validateFridaOffset(%q): %v", value, err)
			}
		})
	}

	for _, value := range []string{
		"",
		"1",
		"0x",
		"0x-1",
		"0xgg",
		"0x0",
		"0X000000",
		"0x8000000000000000",
		"0xffffffffffffffff",
		"0x10000000000000000",
	} {
		t.Run("invalid_"+value, func(t *testing.T) {
			if err := validateFridaOffset("offset", value); err == nil {
				t.Fatalf("validateFridaOffset(%q) succeeded", value)
			}
		})
	}
}

func TestValidateWechatHookConfigChecksAdditionalOffsetLikeKeys(t *testing.T) {
	if err := validateWechatHookConfig(
		map[string]string{
			"requiredAddr":  "0x1",
			"schemaVersion": "4.1.11",
			"sendMode":      "async",
			"uploadPolicy":  "direct",
			"mediaFormat":   "compact",
			"callbackType":  "json",
		},
		[]string{"requiredAddr"},
	); err != nil {
		t.Fatal(err)
	}
	if err := validateWechatHookConfig(
		map[string]string{
			"requiredAddr": "0x1",
			"futureOffset": "not-hex",
		},
		[]string{"requiredAddr"},
	); err == nil {
		t.Fatal("additional offset-like key with a non-hex value was accepted")
	}
	if err := validateWechatHookConfig(
		map[string]string{
			"requiredAddr": "0x1",
			"metadata":     "0x0",
		},
		[]string{"requiredAddr"},
	); err == nil {
		t.Fatal("additional offset-like value with a zero offset was accepted")
	}
}

func TestRenderFridaScriptTemplateIsByteIdenticalToFrozenRenderer(t *testing.T) {
	codeTemplate, err := os.ReadFile("./script.js")
	if err != nil {
		t.Fatal(err)
	}
	jsonData, err := os.ReadFile("../wechat_version/4_1_11_53_mac.json")
	if err != nil {
		t.Fatal(err)
	}

	var legacyConfig map[string]string
	if err := json.Unmarshal(jsonData, &legacyConfig); err != nil {
		t.Fatal(err)
	}
	legacyTemplate, err := template.New("fridaScript").Parse(string(codeTemplate))
	if err != nil {
		t.Fatal(err)
	}
	var legacy bytes.Buffer
	if err := legacyTemplate.Execute(&legacy, legacyConfig); err != nil {
		t.Fatal(err)
	}

	rendered, err := renderFridaScriptTemplate(codeTemplate, jsonData)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(rendered), legacy.Bytes()) {
		t.Fatalf(
			"rendered bytes differ: got length %d, want length %d",
			len(rendered),
			legacy.Len(),
		)
	}
}

func TestMalformedFridaMessagesArePanicSafe(t *testing.T) {
	messages := []string{
		"",
		`null`,
		`[]`,
		`{"type":null}`,
		`{"type":42}`,
		`{"type":""}`,
		`{"type":"send"}`,
		`{"type":"send","payload":null}`,
		`{"type":"send","payload":[]}`,
		`{"type":"send","payload":{"type":1}}`,
		`{"type":"send","payload":{"type":"buf2resp","data":[0,256]}}`,
		`{"type":"send","payload":{"type":"buf2resp","data":[0,"bad"]}}`,
		`{"type":"send","payload":{"type":"upload_image_finish","target_id":1}}`,
		`{"type":"send","payload":{"type":"upload_voice_finish","target_id":"wxid_test","voice_duration":1.5}}`,
		`{"type":"log","payload":{"nested":["still","safe"]}}`,
		`{"type":"error","description":{"nested":true},"stack":["bad"]}`,
	}

	for _, message := range messages {
		message := message
		t.Run(message, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("handleFridaMessage(%q) panicked: %v", message, recovered)
				}
			}()
			handleFridaMessage(message)
		})
	}
}
