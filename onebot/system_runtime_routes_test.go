package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type systemRuntimeHealthSourceStub struct {
	calls  atomic.Int32
	health func(context.Context) (AgentHealth, error)
}

var systemRuntimeTestStartedAt = time.Date(2026, time.July, 24, 8, 30, 45, 0, time.UTC)

const systemRuntimeTestPID = 2468

func (s *systemRuntimeHealthSourceStub) Health(ctx context.Context) (AgentHealth, error) {
	s.calls.Add(1)
	return s.health(ctx)
}

func TestSystemRuntimeHealthReady(t *testing.T) {
	source := &systemRuntimeHealthSourceStub{
		health: func(context.Context) (AgentHealth, error) {
			return AgentHealth{
				Status:          "ok",
				Ready:           true,
				PID:             42,
				ProtocolVersion: AgentProtocolVersion,
			}, nil
		},
	}
	response := systemRuntimeHealthRequest(
		registerSystemRuntimeHealthTestMux(source),
		http.MethodGet,
		"operator-secret",
		"127.0.0.1:12345",
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	var health SystemRuntimeHealth
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	assertSystemRuntimeMetadata(t, health, true, true)
	if health.Status != "ok" || !health.Ready || health.Agent == nil || !health.Agent.Ready ||
		health.Agent.ProtocolVersion != AgentProtocolVersion {
		t.Fatalf("health = %#v", health)
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("Health calls = %d, want 1", calls)
	}
}

func TestRegisterSystemRuntimeRoutesCapturesMetadata(t *testing.T) {
	source := &systemRuntimeHealthSourceStub{
		health: func(context.Context) (AgentHealth, error) {
			return AgentHealth{Status: "ok", Ready: true}, nil
		},
	}
	mux := http.NewServeMux()
	before := time.Now().UTC().Add(-time.Second)
	registerSystemRuntimeRoutes(mux, source, "operator-secret")
	after := time.Now().UTC().Add(time.Second)
	response := systemRuntimeHealthRequest(
		mux,
		http.MethodGet,
		"operator-secret",
		"127.0.0.1:12345",
	)

	var health SystemRuntimeHealth
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	startedAt, err := time.Parse(time.RFC3339, health.StartedAt)
	if err != nil {
		t.Fatalf("StartedAt = %q: %v", health.StartedAt, err)
	}
	if health.Process != systemRuntimeProcess || health.PID != os.Getpid() ||
		startedAt.Before(before) || startedAt.After(after) {
		t.Fatalf("health metadata = %#v", health)
	}
}

func TestSystemRuntimeHealthDegraded(t *testing.T) {
	t.Run("unready", func(t *testing.T) {
		source := &systemRuntimeHealthSourceStub{
			health: func(context.Context) (AgentHealth, error) {
				return AgentHealth{
					Status:          "degraded",
					Ready:           false,
					ProtocolVersion: AgentProtocolVersion,
				}, nil
			},
		}
		response := systemRuntimeHealthRequest(
			registerSystemRuntimeHealthTestMux(source),
			http.MethodGet,
			"operator-secret",
			"127.0.0.1:12345",
		)

		assertSystemRuntimeDegraded(t, response)
		var health SystemRuntimeHealth
		if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		assertSystemRuntimeMetadata(t, health, true, true)
		if health.Agent == nil || health.Agent.Ready ||
			health.Agent.ProtocolVersion != AgentProtocolVersion {
			t.Fatalf("agent health = %#v", health.Agent)
		}
		if calls := source.calls.Load(); calls != 1 {
			t.Fatalf("Health calls = %d, want 1", calls)
		}
	})

	t.Run("nil source", func(t *testing.T) {
		response := systemRuntimeHealthRequest(
			registerSystemRuntimeHealthTestMux(nil),
			http.MethodGet,
			"operator-secret",
			"127.0.0.1:12345",
		)
		assertSystemRuntimeDegraded(t, response)
		var health SystemRuntimeHealth
		if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		assertSystemRuntimeMetadata(t, health, false, false)
		if strings.Contains(response.Body.String(), `"agent"`) {
			t.Fatalf("nil source returned agent health: %s", response.Body.String())
		}
	})
}

func TestSystemRuntimeHealthRedactsErrors(t *testing.T) {
	const privateError = "dial /private/tmp/operator-secret.sock: credential rejected"
	source := &systemRuntimeHealthSourceStub{
		health: func(context.Context) (AgentHealth, error) {
			return AgentHealth{}, errors.New(privateError)
		},
	}
	response := systemRuntimeHealthRequest(
		registerSystemRuntimeHealthTestMux(source),
		http.MethodGet,
		"operator-secret",
		"127.0.0.1:12345",
	)

	assertSystemRuntimeDegraded(t, response)
	var health SystemRuntimeHealth
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	assertSystemRuntimeMetadata(t, health, false, false)
	if strings.Contains(response.Body.String(), privateError) ||
		strings.Contains(response.Body.String(), "operator-secret.sock") ||
		strings.Contains(response.Body.String(), `"agent"`) {
		t.Fatalf("response leaked source error: %s", response.Body.String())
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("Health calls = %d, want 1", calls)
	}
}

func TestSystemRuntimeHealthTimesOut(t *testing.T) {
	source := &systemRuntimeHealthSourceStub{
		health: func(ctx context.Context) (AgentHealth, error) {
			<-ctx.Done()
			return AgentHealth{}, ctx.Err()
		},
	}
	startedAt := time.Now()
	response := systemRuntimeHealthRequest(
		registerSystemRuntimeHealthTestMux(source),
		http.MethodGet,
		"operator-secret",
		"127.0.0.1:12345",
	)
	elapsed := time.Since(startedAt)

	assertSystemRuntimeDegraded(t, response)
	var health SystemRuntimeHealth
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	assertSystemRuntimeMetadata(t, health, false, false)
	if elapsed < systemRuntimeHealthTimeout-250*time.Millisecond ||
		elapsed > systemRuntimeHealthTimeout+time.Second {
		t.Fatalf("handler elapsed = %s, want approximately %s", elapsed, systemRuntimeHealthTimeout)
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("Health calls = %d, want 1", calls)
	}
}

func TestSystemRuntimeHealthAuth(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		remoteAddr string
		wantStatus int
		wantCalls  int32
	}{
		{
			name:       "missing bearer",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong bearer",
			token:      "wrong-secret",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "non-loopback",
			token:      "operator-secret",
			remoteAddr: "192.0.2.10:12345",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "authorized",
			token:      "operator-secret",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &systemRuntimeHealthSourceStub{
				health: func(context.Context) (AgentHealth, error) {
					return AgentHealth{Status: "ok", Ready: true}, nil
				},
			}
			response := systemRuntimeHealthRequest(
				registerSystemRuntimeHealthTestMux(source),
				http.MethodGet,
				test.token,
				test.remoteAddr,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if calls := source.calls.Load(); calls != test.wantCalls {
				t.Fatalf("Health calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestSystemRuntimeHealthMethod(t *testing.T) {
	source := &systemRuntimeHealthSourceStub{
		health: func(context.Context) (AgentHealth, error) {
			return AgentHealth{Status: "ok", Ready: true}, nil
		},
	}
	response := systemRuntimeHealthRequest(
		registerSystemRuntimeHealthTestMux(source),
		http.MethodPost,
		"operator-secret",
		"127.0.0.1:12345",
	)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if allow := response.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
	if calls := source.calls.Load(); calls != 0 {
		t.Fatalf("Health calls = %d, want 0", calls)
	}
}

func TestSystemRuntimeLaunchdPlists(t *testing.T) {
	tests := []struct {
		name         string
		label        string
		arguments    []string
		placeholders []string
		stdout       string
		stderr       string
	}{
		{
			name:  "com.ludwig.wechat-agent.plist",
			label: "com.ludwig.wechat-agent",
			arguments: []string{
				"@@BIN_DIR@@/wechat-agent",
				"-run_dir",
				"@@RUN_DIR@@",
				"-agent_socket",
				"@@RUN_DIR@@/wechat-agent.sock",
			},
			placeholders: []string{
				"@@BIN_DIR@@",
				"@@LOG_DIR@@",
				"@@RESOURCE_DIR@@",
				"@@RUN_DIR@@",
			},
			stdout: "@@LOG_DIR@@/wechat-agent.stdout.log",
			stderr: "@@LOG_DIR@@/wechat-agent.stderr.log",
		},
		{
			name:  "com.ludwig.hr-workbench.plist",
			label: "com.ludwig.hr-workbench",
			arguments: []string{
				"@@BIN_DIR@@/hr-workbench",
				"-run_dir",
				"@@RUN_DIR@@",
				"-agent_socket",
				"@@RUN_DIR@@/wechat-agent.sock",
				"-receive_host",
				"@@HR_LISTEN_ADDR@@",
			},
			placeholders: []string{
				"@@BIN_DIR@@",
				"@@HR_LISTEN_ADDR@@",
				"@@LOG_DIR@@",
				"@@RESOURCE_DIR@@",
				"@@RUN_DIR@@",
			},
			stdout: "@@LOG_DIR@@/hr-workbench.stdout.log",
			stderr: "@@LOG_DIR@@/hr-workbench.stderr.log",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join("..", "launchd", test.name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertLaunchdXMLWellFormed(t, data)
			if got := launchdProgramArguments(t, data); !reflect.DeepEqual(got, test.arguments) {
				t.Fatalf("ProgramArguments = %#v, want %#v", got, test.arguments)
			}

			compact := strings.Join(strings.Fields(string(data)), " ")
			for _, fragment := range []string{
				"<key>Label</key> <string>" + test.label + "</string>",
				"<key>RunAtLoad</key> <true/>",
				"<key>KeepAlive</key> <dict> <key>SuccessfulExit</key> <false/> </dict>",
				"<key>ThrottleInterval</key> <integer>10</integer>",
				"<key>ProcessType</key> <string>Background</string>",
				"<key>Umask</key> <integer>63</integer>",
				"<key>WorkingDirectory</key> <string>@@RESOURCE_DIR@@</string>",
				"<key>StandardOutPath</key> <string>" + test.stdout + "</string>",
				"<key>StandardErrorPath</key> <string>" + test.stderr + "</string>",
			} {
				if !strings.Contains(compact, fragment) {
					t.Errorf("missing launchd fragment %q", fragment)
				}
			}
			if test.stdout == test.stderr {
				t.Fatal("stdout and stderr paths must be independent")
			}

			placeholderPattern := regexp.MustCompile(`@@[A-Z_]+@@`)
			placeholderSet := make(map[string]struct{})
			for _, placeholder := range placeholderPattern.FindAllString(string(data), -1) {
				placeholderSet[placeholder] = struct{}{}
			}
			gotPlaceholders := make([]string, 0, len(placeholderSet))
			for placeholder := range placeholderSet {
				gotPlaceholders = append(gotPlaceholders, placeholder)
			}
			sortStrings(gotPlaceholders)
			sortStrings(test.placeholders)
			if !reflect.DeepEqual(gotPlaceholders, test.placeholders) {
				t.Fatalf("placeholders = %#v, want %#v", gotPlaceholders, test.placeholders)
			}

			for _, forbidden := range []string{
				"<key>UserName</key>",
				"/bin/sh",
				"/bin/zsh",
				"/bin/bash",
				"/usr/bin/env",
				"/Users/",
				"/private/tmp/",
				"/home/",
			} {
				if strings.Contains(string(data), forbidden) {
					t.Errorf("plist contains forbidden value %q", forbidden)
				}
			}
			if regexp.MustCompile(`(?i)(api[_-]?key|authorization|password|secret|token)`).Match(data) {
				t.Error("plist contains a credential-like value")
			}
		})
	}
}

func registerSystemRuntimeHealthTestMux(source SystemRuntimeHealthSource) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(
		"/api/system/health",
		automationOperatorAuth(
			"operator-secret",
			systemRuntimeHealthHandler(source, systemRuntimeTestStartedAt, systemRuntimeTestPID),
		),
	)
	return mux
}

func systemRuntimeHealthRequest(
	handler http.Handler,
	method string,
	token string,
	remoteAddr string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/system/health", nil)
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertSystemRuntimeDegraded(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	var health SystemRuntimeHealth
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "degraded" || health.Ready {
		t.Fatalf("health = %#v", health)
	}
}

func assertSystemRuntimeMetadata(
	t *testing.T,
	health SystemRuntimeHealth,
	agentConnected bool,
	hasAgent bool,
) {
	t.Helper()
	if health.Process != systemRuntimeProcess ||
		health.PID != systemRuntimeTestPID ||
		health.StartedAt != systemRuntimeTestStartedAt.Format(time.RFC3339) ||
		health.AgentConnected != agentConnected ||
		(health.Agent != nil) != hasAgent {
		t.Fatalf("health metadata = %#v", health)
	}
}

func assertLaunchdXMLWellFormed(t *testing.T, data []byte) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("invalid plist XML: %v", err)
		}
	}
}

func launchdProgramArguments(t *testing.T, data []byte) []string {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read plist XML: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			t.Fatal(err)
		}
		if key != "ProgramArguments" {
			continue
		}
		for {
			token, err = decoder.Token()
			if err != nil {
				t.Fatal(err)
			}
			arrayStart, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			if arrayStart.Name.Local != "array" {
				t.Fatalf("ProgramArguments value = <%s>, want <array>", arrayStart.Name.Local)
			}
			var arguments []string
			for {
				token, err = decoder.Token()
				if err != nil {
					t.Fatal(err)
				}
				switch value := token.(type) {
				case xml.StartElement:
					if value.Name.Local != "string" {
						t.Fatalf("ProgramArguments item = <%s>, want <string>", value.Name.Local)
					}
					var argument string
					if err := decoder.DecodeElement(&argument, &value); err != nil {
						t.Fatal(err)
					}
					arguments = append(arguments, argument)
				case xml.EndElement:
					if value.Name.Local == "array" {
						return arguments
					}
				}
			}
		}
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
