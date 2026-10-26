package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type automationProviderTestKeychainRunner struct {
	value string
	found bool
}

func (r *automationProviderTestKeychainRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("missing keychain command")
	}
	switch args[0] {
	case "find-generic-password":
		if !r.found {
			return nil, automationProviderTestExitError(44)
		}
		return []byte(r.value + "\n"), nil
	case "add-generic-password":
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-w" {
				r.value = args[i+1]
				r.found = true
				return nil, nil
			}
		}
		return nil, errors.New("missing keychain value")
	case "delete-generic-password":
		r.value = ""
		r.found = false
		return nil, nil
	default:
		return nil, errors.New("unexpected keychain command")
	}
}

type automationProviderTestExitError int

func (e automationProviderTestExitError) Error() string {
	return "keychain fixture exit"
}

func (e automationProviderTestExitError) ExitCode() int {
	return int(e)
}

func TestAutomationProviderConfigPersistsSecretSeparatelyAndReopens(t *testing.T) {
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	dataDir := t.TempDir()
	keychain := &automationProviderTestKeychainRunner{}
	store, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	key := "secret-provider-key"
	gateModel := "test-gate-model"
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion:    0,
		Enabled:            true,
		Endpoint:           "http://127.0.0.1:18080/v1/chat/completions",
		Model:              "test-model",
		GateModel:          &gateModel,
		TimeoutSeconds:     12,
		PromptTemplate:     "Return strict JSON.",
		GatePromptTemplate: "Return a strict gate decision.",
		APIKey:             &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.Version != 1 ||
		!public.APIKeyConfigured ||
		!public.Enabled ||
		public.GateModel != "test-gate-model" ||
		public.GatePromptTemplate != "Return a strict gate decision." {
		t.Fatalf("public config = %#v", public)
	}
	configData, err := os.ReadFile(filepath.Join(dataDir, "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), key) || strings.Contains(string(configData), "api_key") {
		t.Fatalf("provider.json exposed secret: %s", configData)
	}
	if !strings.Contains(string(configData), `"gate_model": "test-gate-model"`) ||
		!strings.Contains(string(configData), `"gate_prompt_template": "Return a strict gate decision."`) {
		t.Fatalf("provider.json omitted gate config: %s", configData)
	}
	files := []string{"provider.json"}
	if runtime.GOOS == "darwin" {
		if !keychain.found || keychain.value != key {
			t.Fatalf("Keychain secret = %q, found = %v", keychain.value, keychain.found)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "provider.key")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("provider.key should not persist on Darwin: %v", err)
		}
	} else {
		keyData, err := os.ReadFile(filepath.Join(dataDir, "provider.key"))
		if err != nil {
			t.Fatal(err)
		}
		if string(keyData) != key {
			t.Fatalf("provider.key = %q", keyData)
		}
		files = append(files, "provider.key")
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(dataDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %04o", name, info.Mode().Perm())
		}
	}
	reopened, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	reopenedPublic := reopened.Public()
	if reopenedPublic.Version != 1 || !reopenedPublic.APIKeyConfigured ||
		reopenedPublic.Endpoint != public.Endpoint ||
		reopenedPublic.PromptTemplate != public.PromptTemplate ||
		reopenedPublic.GateModel != public.GateModel ||
		reopenedPublic.GatePromptTemplate != public.GatePromptTemplate {
		t.Fatalf("reopened config = %#v", reopenedPublic)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".provider.transaction.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider transaction marker remained after commit: %v", err)
	}
}

func TestAutomationProviderConfigPreservesOmittedGateModelAndAllowsExplicitClear(t *testing.T) {
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	store, err := openAutomationProviderConfigStore(
		t.TempDir(),
		&automationProviderTestKeychainRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	key := "gate-model-preservation-key"
	gateModel := "dedicated-gate-model"
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: 0,
		Enabled:         true,
		Endpoint:        "http://127.0.0.1:18080/v1/chat/completions",
		Model:           "planner-model",
		GateModel:       &gateModel,
		TimeoutSeconds:  12,
		PromptTemplate:  "Return strict JSON.",
		APIKey:          &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	public, err = store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: public.Version,
		Enabled:         true,
		Endpoint:        public.Endpoint,
		Model:           "planner-model-v2",
		TimeoutSeconds:  public.TimeoutSeconds,
		PromptTemplate:  public.PromptTemplate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.GateModel != gateModel {
		t.Fatalf("omitted gate model = %q, want %q", public.GateModel, gateModel)
	}
	clear := ""
	public, err = store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: public.Version,
		Enabled:         true,
		Endpoint:        public.Endpoint,
		Model:           public.Model,
		GateModel:       &clear,
		TimeoutSeconds:  public.TimeoutSeconds,
		PromptTemplate:  public.PromptTemplate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.GateModel != "" {
		t.Fatalf("explicitly cleared gate model = %q", public.GateModel)
	}
}

func TestAutomationProviderConfigPersistsChatContextPolicy(t *testing.T) {
	t.Setenv("ONEBOT_AI_PROTOCOL", "")
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	dataDir := t.TempDir()
	store, err := openAutomationProviderConfigStore(
		dataDir,
		&automationProviderTestKeychainRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	initial := store.Public()
	if initial.ContextWindowTokens != AutomationProviderDefaultContextWindowTokens ||
		initial.AutoCompactPercent != AutomationProviderDefaultAutoCompactPercent ||
		initial.ContextReserveTokens != AutomationProviderDefaultContextReserveTokens ||
		initial.RecentTurnTokens != AutomationProviderDefaultRecentTurnTokens {
		t.Fatalf("default context policy = %+v", initial)
	}

	window := 131072
	percent := 75
	reserve := 16000
	recent := 24000
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion:      initial.Version,
		Enabled:              false,
		TimeoutSeconds:       initial.TimeoutSeconds,
		ContextWindowTokens:  &window,
		AutoCompactPercent:   &percent,
		ContextReserveTokens: &reserve,
		RecentTurnTokens:     &recent,
		PromptTemplate:       initial.PromptTemplate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.ContextWindowTokens != window ||
		public.AutoCompactPercent != percent ||
		public.ContextReserveTokens != reserve ||
		public.RecentTurnTokens != recent {
		t.Fatalf("updated context policy = %+v", public)
	}
	policy := store.ChatContextPolicy()
	if policy.ContextWindowTokens != window ||
		policy.AutoCompactPercent != percent ||
		policy.ContextReserveTokens != reserve ||
		policy.RecentTurnTokens != recent {
		t.Fatalf("runtime context policy = %+v", policy)
	}

	public, err = store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: public.Version,
		Enabled:         false,
		TimeoutSeconds:  public.TimeoutSeconds,
		PromptTemplate:  public.PromptTemplate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.ContextWindowTokens != window ||
		public.AutoCompactPercent != percent ||
		public.ContextReserveTokens != reserve ||
		public.RecentTurnTokens != recent {
		t.Fatalf("omitted context policy changed = %+v", public)
	}

	reopened, err := openAutomationProviderConfigStore(
		dataDir,
		&automationProviderTestKeychainRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	reopenedPublic := reopened.Public()
	if reopenedPublic.ContextWindowTokens != window ||
		reopenedPublic.AutoCompactPercent != percent ||
		reopenedPublic.ContextReserveTokens != reserve ||
		reopenedPublic.RecentTurnTokens != recent {
		t.Fatalf("reopened context policy = %+v", reopenedPublic)
	}
}

func TestAutomationProviderConfigRecoversInterruptedTransactions(t *testing.T) {
	for _, test := range []struct {
		name          string
		currentSecret string
		wantModel     string
		wantSecret    string
		wantError     bool
	}{
		{
			name:          "rollback config when secret is still previous",
			currentSecret: "old-provider-key",
			wantModel:     "old-model",
			wantSecret:    "old-provider-key",
		},
		{
			name:          "commit when config and secret are both next",
			currentSecret: "new-provider-key",
			wantModel:     "new-model",
			wantSecret:    "new-provider-key",
		},
		{
			name:          "block inconsistent secret",
			currentSecret: "unknown-provider-key",
			wantError:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ONEBOT_AI_ENDPOINT", "")
			t.Setenv("ONEBOT_AI_MODEL", "")
			t.Setenv("ONEBOT_AI_API_KEY", "")
			dataDir := t.TempDir()
			keychain := &automationProviderTestKeychainRunner{}
			store, err := openAutomationProviderConfigStore(dataDir, keychain)
			if err != nil {
				t.Fatal(err)
			}
			oldKey := "old-provider-key"
			if _, err := store.Update(AutomationProviderConfigUpdate{
				ExpectedVersion: 0,
				Enabled:         true,
				Endpoint:        "https://api.example.test/v1/chat/completions",
				Model:           "old-model",
				TimeoutSeconds:  30,
				PromptTemplate:  "Return strict JSON.",
				APIKey:          &oldKey,
			}); err != nil {
				t.Fatal(err)
			}
			store.mu.RLock()
			previous := cloneAutomationProviderConfig(store.config)
			store.mu.RUnlock()
			next := cloneAutomationProviderConfig(previous)
			next.Version++
			next.Model = "new-model"
			next.UpdatedAt = time.Now().UTC()
			newKey := "new-provider-key"
			transaction, err := newAutomationProviderTransaction(
				true,
				previous,
				true,
				oldKey,
				next,
				newKey,
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeAutomationProviderTransaction(store.transactionPath, transaction); err != nil {
				t.Fatal(err)
			}
			marker, err := os.ReadFile(store.transactionPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(marker), oldKey) || strings.Contains(string(marker), newKey) {
				t.Fatalf("provider transaction leaked a secret: %s", marker)
			}
			nextData, err := automationProviderConfigData(next)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeAutomationProviderFileAtomic(store.configPath, nextData); err != nil {
				t.Fatal(err)
			}
			if err := store.persistSecretLocked(test.currentSecret); err != nil {
				t.Fatal(err)
			}

			reopened, err := openAutomationProviderConfigStore(dataDir, keychain)
			if test.wantError {
				if err == nil {
					t.Fatal("open succeeded with an inconsistent provider transaction")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			public := reopened.Public()
			if public.Model != test.wantModel {
				t.Fatalf("recovered model = %q, want %q", public.Model, test.wantModel)
			}
			if keychain.value != test.wantSecret {
				t.Fatalf("recovered secret = %q, want %q", keychain.value, test.wantSecret)
			}
			if _, err := os.Stat(store.transactionPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("provider transaction marker remained after recovery: %v", err)
			}
		})
	}
}

func TestAutomationProviderConfigEnvironmentFallbackAndAudienceVersions(t *testing.T) {
	t.Setenv("ONEBOT_AI_ENDPOINT", "https://api.example.test/v1/chat/completions")
	t.Setenv("ONEBOT_AI_MODEL", "env-model")
	t.Setenv("ONEBOT_AI_API_KEY", "env-key")
	store, err := openAutomationProviderConfigStore(
		t.TempDir(),
		&automationProviderTestKeychainRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if public := store.Public(); public.Version != 0 || !public.Enabled || !public.APIKeyConfigured {
		t.Fatalf("fallback public = %#v", public)
	}
	audience, public, err := store.CreateAudience(AutomationAudienceCreate{
		ExpectedVersion: 0,
		Label:           "调度群",
		ChatType:        ChatTypeGroup,
		PeerID:          "synthetic-room@chatroom",
		AllowedIntents:  []string{"general", "scheduling"},
		Enabled:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audience.Version != 1 || public.Version != 1 || len(public.Audiences) != 1 {
		t.Fatalf("create result = %#v %#v", audience, public)
	}
	updated, public, err := store.UpdateAudience(audience.ID, AutomationAudienceUpdate{
		ExpectedVersion: audience.Version,
		Label:           "现场调度群",
		ChatType:        ChatTypeGroup,
		PeerID:          audience.PeerID,
		AllowedIntents:  []string{"general"},
		Enabled:         false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || public.Version != 2 {
		t.Fatalf("update result = %#v %#v", updated, public)
	}
	if _, err := store.DeleteAudience(audience.ID, AutomationAudienceDelete{ExpectedVersion: 1}); !errorsIsProviderConflict(err) {
		t.Fatalf("stale delete error = %v", err)
	}
	public, err = store.DeleteAudience(audience.ID, AutomationAudienceDelete{ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if public.Version != 3 || len(public.Audiences) != 0 {
		t.Fatalf("delete result = %#v", public)
	}
}

func TestAutomationProviderConfigProtocolDefaultPersistenceReadinessRedaction(t *testing.T) {
	t.Setenv("ONEBOT_AI_PROTOCOL", "")
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	dataDir := t.TempDir()
	keychain := &automationProviderTestKeychainRunner{}
	store, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	if public := store.Public(); public.Protocol != AutomationProviderChatCompletions ||
		public.Readiness.Protocol != AutomationProviderChatCompletions {
		t.Fatalf("default protocol public = %#v", public)
	}

	key := "redact-provider-key"
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: 0,
		Enabled:         true,
		Protocol:        AutomationProviderResponses,
		Endpoint:        "https://api.example.test/v1/responses",
		Model:           "responses-model",
		TimeoutSeconds:  9,
		PromptTemplate:  "Return strict JSON.",
		APIKey:          &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.Protocol != AutomationProviderResponses || public.Readiness.Protocol != AutomationProviderResponses ||
		public.Readiness.Status != AutomationProviderReadinessReady || !public.APIKeyConfigured {
		t.Fatalf("responses public = %#v", public)
	}
	configData, err := os.ReadFile(filepath.Join(dataDir, "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"protocol": "responses"`) || strings.Contains(string(configData), key) {
		t.Fatalf("provider.json = %s", configData)
	}
	reopened, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedPublic := reopened.Public(); reopenedPublic.Protocol != AutomationProviderResponses ||
		reopenedPublic.Readiness.Protocol != AutomationProviderResponses || !reopenedPublic.APIKeyConfigured {
		t.Fatalf("reopened public = %#v", reopenedPublic)
	}

	public, err = store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: 1,
		Enabled:         true,
		Endpoint:        "https://api.example.test/v1/responses",
		Model:           "responses-model-v2",
		TimeoutSeconds:  11,
		PromptTemplate:  "Return strict JSON.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.Version != 2 || public.Protocol != AutomationProviderResponses ||
		public.Readiness.Protocol != AutomationProviderResponses ||
		public.Readiness.Status != AutomationProviderReadinessReady {
		t.Fatalf("partial responses update public = %#v", public)
	}

	store.mu.RLock()
	config := cloneAutomationProviderConfig(store.config)
	apiKey := store.apiKey
	store.mu.RUnlock()
	store.recordConnectionTest(config, apiKey, errors.New("fixture failure with "+key))
	public = store.Public()
	if public.Readiness.Status != AutomationProviderReadinessTestFailed ||
		public.Readiness.LatestConnectionTest == nil ||
		public.Readiness.LatestConnectionTest.Protocol != AutomationProviderResponses ||
		!public.Readiness.LatestConnectionTest.MatchesCurrentConfig ||
		strings.Contains(public.Readiness.LatestConnectionTest.Error, key) ||
		!strings.Contains(public.Readiness.LatestConnectionTest.Error, "[redacted]") {
		t.Fatalf("redacted readiness = %#v", public.Readiness)
	}
	publicJSON, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), key) {
		t.Fatalf("public config exposed secret: %s", publicJSON)
	}
}

func TestAutomationProviderConfigLatestFailedTestSurvivesAudienceAndPromptMutations(t *testing.T) {
	t.Setenv("ONEBOT_AI_PROTOCOL", "")
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")

	store, err := openAutomationProviderConfigStore(t.TempDir(), &automationProviderTestKeychainRunner{})
	if err != nil {
		t.Fatal(err)
	}
	key := "latest-test-secret"
	basePrompt := "Return strict JSON for recruiting fixtures."
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: 0,
		Enabled:         true,
		Protocol:        AutomationProviderResponses,
		Endpoint:        "https://api.example.test/v1/responses",
		Model:           "responses-model",
		TimeoutSeconds:  9,
		PromptTemplate:  basePrompt,
		APIKey:          &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalRevisionID := public.ActivePromptRevisionID

	recordFailedTestForPrompt := func(prompt string) {
		t.Helper()
		store.mu.RLock()
		config := cloneAutomationProviderConfig(store.config)
		apiKey := store.apiKey
		store.mu.RUnlock()
		config.PromptTemplate = prompt
		store.recordConnectionTest(config, apiKey, errors.New("fixture failure with "+key))
	}
	assertMatchingFailed := func(label string, public AutomationProviderConfigPublic) {
		t.Helper()
		latest := public.Readiness.LatestConnectionTest
		if public.Readiness.Status != AutomationProviderReadinessTestFailed ||
			latest == nil ||
			latest.Status != AutomationProviderConnectionTestFailed ||
			latest.Protocol != AutomationProviderResponses ||
			!latest.MatchesCurrentConfig ||
			strings.Contains(latest.Error, key) ||
			!strings.Contains(latest.Error, "[redacted]") {
			t.Fatalf("%s readiness = %#v", label, public.Readiness)
		}
	}

	recordFailedTestForPrompt(basePrompt)
	assertMatchingFailed("initial", store.Public())

	audience, public, err := store.CreateAudience(AutomationAudienceCreate{
		ExpectedVersion: public.Version,
		Label:           "Recruiting source group",
		ChatType:        ChatTypeGroup,
		PeerID:          "synthetic-jobs@chatroom",
		AllowedIntents:  []string{"recruiting"},
		Enabled:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("audience create", public)

	audience, public, err = store.UpdateAudience(audience.ID, AutomationAudienceUpdate{
		ExpectedVersion: audience.Version,
		Label:           "Updated recruiting source group",
		ChatType:        ChatTypeGroup,
		PeerID:          audience.PeerID,
		AllowedIntents:  []string{"recruiting", "job_intake"},
		Enabled:         false,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("audience update", public)

	public, err = store.DeleteAudience(audience.ID, AutomationAudienceDelete{
		ExpectedVersion: audience.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("audience delete", public)

	_, public, err = store.SavePromptRevision(public.Version, basePrompt, "operator", "same prompt")
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("prompt save no-op", public)

	public, err = store.ActivatePromptRevision(public.Version, originalRevisionID, "operator", "already active")
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("prompt activate no-op", public)

	changedPrompt := basePrompt + " Use only fictional applicants."
	recordFailedTestForPrompt(changedPrompt)
	_, public, err = store.SavePromptRevision(public.Version, changedPrompt, "operator", "changed prompt")
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("prompt save changed", public)

	recordFailedTestForPrompt(basePrompt)
	public, err = store.ActivatePromptRevision(public.Version, originalRevisionID, "operator", "restore prompt")
	if err != nil {
		t.Fatal(err)
	}
	assertMatchingFailed("prompt activate changed", public)
}

func errorsIsProviderConflict(err error) bool {
	return err == ErrAutomationProviderConfigConflict
}

func TestAutomationProviderConfigEndpointProtocolCombinationValidation(t *testing.T) {
	base := func(protocol AutomationProviderProtocol, endpoint string) AutomationProviderConfig {
		config := defaultAutomationProviderConfig()
		config.Version = 1
		config.Protocol = protocol
		config.Endpoint = endpoint
		config.Model = "test-model"
		config.Enabled = false
		return config
	}
	normalize := func(config AutomationProviderConfig) error {
		_, _, err := normalizeAutomationProviderConfig(config, "", true)
		return err
	}
	tests := []struct {
		name    string
		config  AutomationProviderConfig
		wantErr string
	}{
		{
			name:    "chat_completions pointed at responses path",
			config:  base(AutomationProviderChatCompletions, "https://host.example/v1/responses"),
			wantErr: "chat_completions protocol requires",
		},
		{
			name:    "chat_completions pointed at responses path with trailing slash",
			config:  base(AutomationProviderChatCompletions, "https://host.example/v1/responses/"),
			wantErr: "chat_completions protocol requires",
		},
		{
			name:    "responses pointed at chat path",
			config:  base(AutomationProviderResponses, "https://host.example/v1/chat/completions"),
			wantErr: "responses protocol requires",
		},
		{
			name:    "chat_completions with bare base url",
			config:  base(AutomationProviderChatCompletions, "https://host.example/v1"),
			wantErr: "",
		},
		{
			name:    "chat_completions with matching chat path",
			config:  base(AutomationProviderChatCompletions, "https://host.example/v1/chat/completions"),
			wantErr: "",
		},
		{
			name:    "responses with matching responses path",
			config:  base(AutomationProviderResponses, "https://host.example/v1/responses"),
			wantErr: "",
		},
		{
			name:    "responses with bare base url",
			config:  base(AutomationProviderResponses, "https://host.example/v1"),
			wantErr: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := normalize(test.config)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("normalizeAutomationProviderConfig error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrAutomationValidation) || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("normalizeAutomationProviderConfig error = %v, want validation error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestAutomationProviderConfigValidateEndpointProtocolBranches(t *testing.T) {
	tests := []struct {
		name     string
		protocol AutomationProviderProtocol
		endpoint string
		wantErr  string
	}{
		{
			name:     "chat_completions responses path",
			protocol: AutomationProviderChatCompletions,
			endpoint: "https://host.example/v1/responses",
			wantErr:  "chat_completions protocol requires",
		},
		{
			name:     "chat_completions responses path trailing slash",
			protocol: AutomationProviderChatCompletions,
			endpoint: "https://host.example/v1/responses/",
			wantErr:  "chat_completions protocol requires",
		},
		{
			name:     "responses chat path",
			protocol: AutomationProviderResponses,
			endpoint: "https://host.example/v1/chat/completions",
			wantErr:  "responses protocol requires",
		},
		{
			name:     "responses chat path trailing slash",
			protocol: AutomationProviderResponses,
			endpoint: "https://host.example/v1/chat/completions/",
			wantErr:  "responses protocol requires",
		},
		{
			name:     "chat_completions bare base url",
			protocol: AutomationProviderChatCompletions,
			endpoint: "https://host.example/v1",
			wantErr:  "",
		},
		{
			name:     "chat_completions matching chat path",
			protocol: AutomationProviderChatCompletions,
			endpoint: "https://host.example/v1/chat/completions",
			wantErr:  "",
		},
		{
			name:     "responses matching responses path",
			protocol: AutomationProviderResponses,
			endpoint: "https://host.example/v1/responses",
			wantErr:  "",
		},
		{
			name:     "responses bare base url",
			protocol: AutomationProviderResponses,
			endpoint: "https://host.example/v1",
			wantErr:  "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAutomationProviderEndpointProtocol(test.protocol, test.endpoint)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAutomationProviderEndpointProtocol error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrAutomationValidation) || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateAutomationProviderEndpointProtocol error = %v, want validation error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestAutomationProviderConfigPromptCacheKeySwitchDefaultAndUpdate(t *testing.T) {
	t.Setenv("ONEBOT_AI_PROTOCOL", "")
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	dataDir := t.TempDir()
	keychain := &automationProviderTestKeychainRunner{}
	store, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	if public := store.Public(); public.ChatCompletionsPromptCacheKey {
		t.Fatalf("default public switch = true, want false: %#v", public)
	}
	normalized, _, err := normalizeAutomationProviderConfig(defaultAutomationProviderConfig(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.ChatCompletionsPromptCacheKey {
		t.Fatalf("normalized zero-value switch = true, want false: %#v", normalized)
	}

	key := "prompt-cache-key-provider-key"
	public, err := store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion:               0,
		Enabled:                       true,
		Endpoint:                      "https://api.example.test/v1/chat/completions",
		Model:                         "test-model",
		ChatCompletionsPromptCacheKey: true,
		TimeoutSeconds:                12,
		PromptTemplate:                "Return strict JSON.",
		APIKey:                        &key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !public.ChatCompletionsPromptCacheKey {
		t.Fatalf("updated public switch = false, want true: %#v", public)
	}
	configData, err := os.ReadFile(filepath.Join(dataDir, "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"chat_completions_prompt_cache_key": true`) {
		t.Fatalf("provider.json omitted switch: %s", configData)
	}
	reopened, err := openAutomationProviderConfigStore(dataDir, keychain)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedPublic := reopened.Public(); !reopenedPublic.ChatCompletionsPromptCacheKey {
		t.Fatalf("reopened public switch = false, want true: %#v", reopenedPublic)
	}

	public, err = store.Update(AutomationProviderConfigUpdate{
		ExpectedVersion: public.Version,
		Enabled:         true,
		Endpoint:        public.Endpoint,
		Model:           public.Model,
		TimeoutSeconds:  public.TimeoutSeconds,
		PromptTemplate:  public.PromptTemplate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.ChatCompletionsPromptCacheKey {
		t.Fatalf("omitted switch retained true after full-replace update: %#v", public)
	}
	if public.Version != 2 {
		t.Fatalf("switch flip version = %d, want 2", public.Version)
	}
}
