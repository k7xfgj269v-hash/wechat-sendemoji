package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AutomationProviderConfigStore struct {
	mu              sync.RWMutex
	dataDir         string
	configPath      string
	transactionPath string
	secrets         AutomationSecretStore
	config          AutomationProviderConfig
	apiKey          string
	persisted       bool
	testClient      *http.Client
	latestTest      *AutomationProviderConnectionTest
}

const automationProviderTransactionVersion = 1

type automationProviderTransaction struct {
	Version              int                      `json:"version"`
	PreviousPersisted    bool                     `json:"previous_persisted"`
	PreviousConfig       AutomationProviderConfig `json:"previous_config"`
	PreviousConfigSHA256 string                   `json:"previous_config_sha256"`
	NextConfig           AutomationProviderConfig `json:"next_config"`
	NextConfigSHA256     string                   `json:"next_config_sha256"`
	PreviousSecretSet    bool                     `json:"previous_secret_set"`
	PreviousSecretSHA256 string                   `json:"previous_secret_sha256"`
	NextSecretSet        bool                     `json:"next_secret_set"`
	NextSecretSHA256     string                   `json:"next_secret_sha256"`
}

func OpenAutomationProviderConfigStore(dataDir string) (*AutomationProviderConfigStore, error) {
	return openAutomationProviderConfigStore(dataDir, nil)
}

func openAutomationProviderConfigStore(
	dataDir string,
	runner AutomationCommandRunner,
) (*AutomationProviderConfigStore, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, NewAutomationValidationError("data_dir", "is required")
	}
	dataDir = filepath.Clean(dataDir)
	if err := ensureAutomationDirectory(dataDir); err != nil {
		return nil, err
	}
	store := &AutomationProviderConfigStore{
		dataDir:         dataDir,
		configPath:      filepath.Join(dataDir, "provider.json"),
		transactionPath: filepath.Join(dataDir, ".provider.transaction.json"),
		secrets:         NewAutomationSecretStore(dataDir, runner),
	}
	if err := store.recoverPendingTransaction(); err != nil {
		return nil, err
	}
	exists, err := automationProviderRegularFile(store.configPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		config := defaultAutomationProviderConfig()
		protocol := strings.TrimSpace(os.Getenv("ONEBOT_AI_PROTOCOL"))
		endpoint := strings.TrimSpace(os.Getenv("ONEBOT_AI_ENDPOINT"))
		model := strings.TrimSpace(os.Getenv("ONEBOT_AI_MODEL"))
		apiKey := strings.TrimSpace(os.Getenv("ONEBOT_AI_API_KEY"))
		if protocol != "" {
			config.Protocol = AutomationProviderProtocol(protocol)
		}
		if endpoint != "" || model != "" || apiKey != "" {
			if endpoint == "" || model == "" || apiKey == "" {
				return nil, errors.New("ONEBOT_AI_ENDPOINT, ONEBOT_AI_MODEL, and ONEBOT_AI_API_KEY must be configured together")
			}
			config.Enabled = true
			config.Endpoint = endpoint
			config.Model = model
		}
		if protocol != "" || endpoint != "" || model != "" || apiKey != "" {
			var normalizeErr error
			config, apiKey, normalizeErr = normalizeAutomationProviderConfig(config, apiKey, true)
			if normalizeErr != nil {
				return nil, normalizeErr
			}
		}
		store.config = config
		store.apiKey = apiKey
		return store, nil
	}
	if err := os.Chmod(store.configPath, 0o600); err != nil {
		return nil, WrapAutomationPersistence("set provider config permissions", err)
	}
	data, err := os.ReadFile(store.configPath)
	if err != nil {
		return nil, WrapAutomationPersistence("read provider config", err)
	}
	var config AutomationProviderConfig
	if err := decodeAutomationProviderConfig(data, &config); err != nil {
		return nil, err
	}
	apiKey, _, err := store.secrets.Get(context.Background(), automationProviderAPIKeySecret)
	if err != nil {
		return nil, err
	}
	config, apiKey, err = normalizeAutomationProviderConfig(config, apiKey, false)
	if err != nil {
		return nil, err
	}
	store.config = config
	store.apiKey = apiKey
	store.persisted = true
	return store, nil
}

func (s *AutomationProviderConfigStore) Public() AutomationProviderConfigPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publicLocked(s.config, s.apiKey)
}

func (s *AutomationProviderConfigStore) publicLocked(
	config AutomationProviderConfig,
	apiKey string,
) AutomationProviderConfigPublic {
	return automationProviderConfigPublic(
		cloneAutomationProviderConfig(config),
		automationProviderEffectiveAPIKey(apiKey),
		s.latestTest,
	)
}

func (s *AutomationProviderConfigStore) ResolveAutomationAudience(id string) (AutomationAudience, bool) {
	if s == nil {
		return AutomationAudience{}, false
	}
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	index := automationAudienceIndex(s.config.Audiences, id)
	if index < 0 {
		return AutomationAudience{}, false
	}
	audience := s.config.Audiences[index]
	audience.AllowedIntents = append([]string{}, audience.AllowedIntents...)
	return audience, true
}

func (s *AutomationProviderConfigStore) Update(update AutomationProviderConfigUpdate) (AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if update.ExpectedVersion != s.config.Version {
		return AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	if update.APIKey != nil && update.ClearAPIKey {
		return AutomationProviderConfigPublic{}, NewAutomationValidationError("api_key", "cannot be set and cleared together")
	}
	apiKey := s.apiKey
	if update.APIKey != nil {
		apiKey = *update.APIKey
	}
	if update.ClearAPIKey {
		apiKey = ""
	}
	apiKey = strings.TrimSpace(apiKey)
	var err error
	protocol := s.config.Protocol
	if strings.TrimSpace(string(update.Protocol)) != "" {
		protocol, err = normalizeAutomationProviderProtocol(update.Protocol)
		if err != nil {
			return AutomationProviderConfigPublic{}, err
		}
	}
	promptTemplate := strings.TrimSpace(update.PromptTemplate)
	if promptTemplate == "" || len(promptTemplate) > 64<<10 {
		return AutomationProviderConfigPublic{}, NewAutomationValidationError(
			"prompt_template",
			"is required and must not exceed 65536 bytes",
		)
	}
	config := cloneAutomationProviderConfig(s.config)
	config.Enabled = update.Enabled
	config.Protocol = protocol
	config.Endpoint = update.Endpoint
	config.Model = update.Model
	config.ChatCompletionsPromptCacheKey = update.ChatCompletionsPromptCacheKey
	if update.GateModel != nil {
		config.GateModel = *update.GateModel
	}
	config.TimeoutSeconds = update.TimeoutSeconds
	if update.ContextWindowTokens != nil {
		config.ContextWindowTokens = *update.ContextWindowTokens
	}
	if update.AutoCompactPercent != nil {
		config.AutoCompactPercent = *update.AutoCompactPercent
	}
	if update.ContextReserveTokens != nil {
		config.ContextReserveTokens = *update.ContextReserveTokens
	}
	if update.RecentTurnTokens != nil {
		config.RecentTurnTokens = *update.RecentTurnTokens
	}
	gatePromptTemplate := strings.TrimSpace(update.GatePromptTemplate)
	if gatePromptTemplate == "" {
		gatePromptTemplate = s.config.GatePromptTemplate
	}
	config.GatePromptTemplate = gatePromptTemplate
	promptChanged := promptTemplate != s.config.PromptTemplate
	changed := promptChanged ||
		config.Enabled != s.config.Enabled ||
		config.Protocol != s.config.Protocol ||
		strings.TrimSpace(config.Endpoint) != s.config.Endpoint ||
		strings.TrimSpace(config.Model) != s.config.Model ||
		strings.TrimSpace(config.GateModel) != s.config.GateModel ||
		config.ChatCompletionsPromptCacheKey != s.config.ChatCompletionsPromptCacheKey ||
		config.TimeoutSeconds != s.config.TimeoutSeconds ||
		config.ContextWindowTokens != s.config.ContextWindowTokens ||
		config.AutoCompactPercent != s.config.AutoCompactPercent ||
		config.ContextReserveTokens != s.config.ContextReserveTokens ||
		config.RecentTurnTokens != s.config.RecentTurnTokens ||
		config.GatePromptTemplate != s.config.GatePromptTemplate ||
		apiKey != s.apiKey
	if !changed {
		return s.publicLocked(s.config, s.apiKey), nil
	}
	now := time.Now().UTC()
	if promptChanged {
		if _, err := appendAutomationPromptRevision(&config, promptTemplate, update.CreatedBy, update.Reason, now); err != nil {
			return AutomationProviderConfigPublic{}, err
		}
	}
	config.Version++
	config.UpdatedAt = now
	config, apiKey, err = normalizeAutomationProviderConfig(config, apiKey, false)
	if err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	if err := s.persistLocked(config, apiKey); err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.apiKey = apiKey
	s.persisted = true
	return s.publicLocked(config, apiKey), nil
}

func (s *AutomationProviderConfigStore) Test(ctx context.Context, request AutomationProviderTestRequest) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	s.mu.RLock()
	config := cloneAutomationProviderConfig(s.config)
	apiKey := s.apiKey
	testClient := s.testClient
	s.mu.RUnlock()
	if strings.TrimSpace(request.Endpoint) != "" {
		config.Endpoint = request.Endpoint
	}
	if strings.TrimSpace(string(request.Protocol)) != "" {
		config.Protocol = request.Protocol
	}
	if strings.TrimSpace(request.Model) != "" {
		config.Model = request.Model
	}
	if request.GateModel != nil {
		config.GateModel = *request.GateModel
	}
	if request.TimeoutSeconds != 0 {
		config.TimeoutSeconds = request.TimeoutSeconds
	}
	if request.ContextWindowTokens != nil {
		config.ContextWindowTokens = *request.ContextWindowTokens
	}
	if request.AutoCompactPercent != nil {
		config.AutoCompactPercent = *request.AutoCompactPercent
	}
	if request.ContextReserveTokens != nil {
		config.ContextReserveTokens = *request.ContextReserveTokens
	}
	if request.RecentTurnTokens != nil {
		config.RecentTurnTokens = *request.RecentTurnTokens
	}
	if strings.TrimSpace(request.PromptTemplate) != "" {
		promptTemplate := strings.TrimSpace(request.PromptTemplate)
		config.PromptTemplate = promptTemplate
		for i := range config.PromptRevisions {
			if config.PromptRevisions[i].ID == config.ActivePromptRevisionID {
				config.PromptRevisions[i].Template = promptTemplate
				config.PromptRevisions[i].SHA256 = automationPromptSHA256(promptTemplate)
				break
			}
		}
	}
	if strings.TrimSpace(request.GatePromptTemplate) != "" {
		config.GatePromptTemplate = request.GatePromptTemplate
	}
	if request.APIKey != nil && request.ClearAPIKey {
		return NewAutomationValidationError("api_key", "cannot be set and cleared together")
	}
	if request.APIKey != nil {
		apiKey = *request.APIKey
	}
	if request.ClearAPIKey {
		apiKey = ""
	}
	apiKey = automationProviderEffectiveAPIKey(apiKey)
	config.Enabled = true
	attemptedConfig := config
	attemptedAPIKey := apiKey
	config, apiKey, err := normalizeAutomationProviderConfig(config, apiKey, true)
	if err != nil {
		s.recordConnectionTest(attemptedConfig, attemptedAPIKey, err)
		return err
	}
	provider, err := newConfiguredOpenAIAutomationProviderWithClient(config, apiKey, testClient)
	if err != nil {
		s.recordConnectionTest(config, apiKey, err)
		return err
	}
	testCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	_, err = provider.Generate(testCtx, AutomationProviderInput{
		SchemaVersion: AutomationProviderInputVersion,
		RunID:         "provider-connection-test",
		EngineVersion: AutomationEngineVersion,
		AllowedTools:  []AutomationToolName{},
	})
	cancel()
	if err == nil {
		gateModel := strings.TrimSpace(config.GateModel)
		if gateModel == "" {
			gateModel = config.Model
		}
		gateProvider, gateErr := newOpenAICompatibleAutomationProviderWithProtocolAndPromptCacheKey(
			config.Protocol,
			config.Endpoint,
			gateModel,
			apiKey,
			config.GatePromptTemplate,
			testClient,
			config.ChatCompletionsPromptCacheKey,
		)
		if gateErr == nil {
			gateTimeout := time.Duration(config.TimeoutSeconds) * time.Second
			if gateTimeout > automationGateMaxTimeout {
				gateTimeout = automationGateMaxTimeout
			}
			gateCtx, gateCancel := context.WithTimeout(
				ctx,
				gateTimeout,
			)
			_, gateErr = gateProvider.GenerateGateDecision(gateCtx, AutomationGateInput{
				SchemaVersion: AutomationGateInputVersion,
				RunID:         "provider-gate-connection-test",
				GateMode:      RecruitingSourceGateModeJobRelevance,
			})
			gateCancel()
		}
		if gateErr != nil {
			err = fmt.Errorf("gate model test failed: %w", gateErr)
		}
	}
	s.recordConnectionTest(config, apiKey, err)
	return err
}

func (s *AutomationProviderConfigStore) Provider() (AutomationProvider, error) {
	s.mu.RLock()
	config := cloneAutomationProviderConfig(s.config)
	apiKey := s.apiKey
	s.mu.RUnlock()
	if !config.Enabled {
		return noopAutomationProvider{}, nil
	}
	return newConfiguredOpenAIAutomationProvider(config, automationProviderEffectiveAPIKey(apiKey))
}

func (s *AutomationProviderConfigStore) ActivePromptRevisionID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.config.ActivePromptRevisionID)
}

func (s *AutomationProviderConfigStore) ChatContextPolicy() AutomationChatContextPolicy {
	if s == nil {
		return AutomationChatContextPolicy{
			ContextWindowTokens:  AutomationProviderDefaultContextWindowTokens,
			AutoCompactPercent:   AutomationProviderDefaultAutoCompactPercent,
			ContextReserveTokens: AutomationProviderDefaultContextReserveTokens,
			RecentTurnTokens:     AutomationProviderDefaultRecentTurnTokens,
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return AutomationChatContextPolicy{
		ContextWindowTokens:  s.config.ContextWindowTokens,
		AutoCompactPercent:   s.config.AutoCompactPercent,
		ContextReserveTokens: s.config.ContextReserveTokens,
		RecentTurnTokens:     s.config.RecentTurnTokens,
	}
}

func (s *AutomationProviderConfigStore) ProviderForPromptRevision(
	revisionID string,
) (AutomationProvider, error) {
	if s == nil {
		return nil, errors.New("automation provider config store is unavailable")
	}
	revisionID = strings.TrimSpace(revisionID)
	s.mu.RLock()
	config := cloneAutomationProviderConfig(s.config)
	apiKey := s.apiKey
	s.mu.RUnlock()
	if revisionID != "" {
		revision, ok := automationPromptRevisionByID(config.PromptRevisions, revisionID)
		if !ok {
			return nil, errors.New("automation prompt revision not found")
		}
		config.PromptTemplate = revision.Template
		config.ActivePromptRevisionID = revision.ID
	}
	if !config.Enabled {
		return noopAutomationProvider{}, nil
	}
	return newConfiguredOpenAIAutomationProvider(config, automationProviderEffectiveAPIKey(apiKey))
}

func (s *AutomationProviderConfigStore) CreateAudience(request AutomationAudienceCreate) (AutomationAudience, AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.ExpectedVersion != s.config.Version {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	if len(s.config.Audiences) >= AutomationProviderMaxAudiences {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, NewAutomationValidationError("audiences", "contains too many entries")
	}
	audience := AutomationAudience{
		ID:                 nextAutomationAudienceID(s.config.Audiences),
		Label:              request.Label,
		ChatType:           request.ChatType,
		PeerID:             request.PeerID,
		AllowedIntents:     append([]string{}, request.AllowedIntents...),
		ApprovedContent:    request.ApprovedContent,
		ExpiresAt:          request.ExpiresAt,
		PerHourLimit:       request.PerHourLimit,
		PerRunRecipientCap: request.PerRunRecipientCap,
		Enabled:            request.Enabled,
		Version:            1,
	}
	audience, err := normalizeAutomationAudience(audience, true)
	if err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, NewAutomationValidationError("audience", err.Error())
	}
	if err := ensureAutomationAudiencePeerUnique(s.config.Audiences, audience, ""); err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, err
	}
	config := cloneAutomationProviderConfig(s.config)
	config.Version++
	config.UpdatedAt = time.Now().UTC()
	config.Audiences = append(config.Audiences, audience)
	if err := s.persistLocked(config, s.apiKey); err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.persisted = true
	return audience, s.publicLocked(config, s.apiKey), nil
}

func (s *AutomationProviderConfigStore) UpdateAudience(id string, request AutomationAudienceUpdate) (AutomationAudience, AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	index := automationAudienceIndex(s.config.Audiences, id)
	if index < 0 {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, errors.New("automation audience not found")
	}
	if request.ExpectedVersion != s.config.Audiences[index].Version {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	audience := AutomationAudience{
		ID:                 id,
		Label:              request.Label,
		ChatType:           request.ChatType,
		PeerID:             request.PeerID,
		AllowedIntents:     append([]string{}, request.AllowedIntents...),
		ApprovedContent:    request.ApprovedContent,
		ExpiresAt:          request.ExpiresAt,
		PerHourLimit:       request.PerHourLimit,
		PerRunRecipientCap: request.PerRunRecipientCap,
		Enabled:            request.Enabled,
		Version:            request.ExpectedVersion + 1,
	}
	audience, err := normalizeAutomationAudience(audience, true)
	if err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, NewAutomationValidationError("audience", err.Error())
	}
	if err := ensureAutomationAudiencePeerUnique(s.config.Audiences, audience, id); err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, err
	}
	config := cloneAutomationProviderConfig(s.config)
	config.Version++
	config.UpdatedAt = time.Now().UTC()
	config.Audiences[index] = audience
	if err := s.persistLocked(config, s.apiKey); err != nil {
		return AutomationAudience{}, AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.persisted = true
	return audience, s.publicLocked(config, s.apiKey), nil
}

func (s *AutomationProviderConfigStore) DeleteAudience(id string, request AutomationAudienceDelete) (AutomationProviderConfigPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	index := automationAudienceIndex(s.config.Audiences, id)
	if index < 0 {
		return AutomationProviderConfigPublic{}, errors.New("automation audience not found")
	}
	if request.ExpectedVersion != s.config.Audiences[index].Version {
		return AutomationProviderConfigPublic{}, ErrAutomationProviderConfigConflict
	}
	config := cloneAutomationProviderConfig(s.config)
	config.Version++
	config.UpdatedAt = time.Now().UTC()
	config.Audiences = append(config.Audiences[:index], config.Audiences[index+1:]...)
	if err := s.persistLocked(config, s.apiKey); err != nil {
		return AutomationProviderConfigPublic{}, err
	}
	s.config = config
	s.persisted = true
	return s.publicLocked(config, s.apiKey), nil
}

func (s *AutomationProviderConfigStore) persistLocked(config AutomationProviderConfig, apiKey string) error {
	data, err := automationProviderConfigData(config)
	if err != nil {
		return err
	}
	secretChanged := apiKey != s.apiKey || (!s.persisted && apiKey != "")
	transaction, err := newAutomationProviderTransaction(
		s.persisted,
		s.config,
		s.persisted && s.apiKey != "",
		s.apiKey,
		config,
		apiKey,
	)
	if err != nil {
		return err
	}
	if err := writeAutomationProviderTransaction(s.transactionPath, transaction); err != nil {
		return err
	}
	if err := writeAutomationProviderFileAtomic(s.configPath, data); err != nil {
		restoreErr := restoreAutomationProviderConfig(
			s.configPath,
			transaction.PreviousPersisted,
			transaction.PreviousConfig,
		)
		if restoreErr == nil {
			_ = removeAutomationProviderFile(s.transactionPath)
		}
		return errors.Join(err, restoreErr)
	}
	if secretChanged {
		if err := s.persistSecretLocked(apiKey); err != nil {
			restoreErr := restoreAutomationProviderConfig(
				s.configPath,
				transaction.PreviousPersisted,
				transaction.PreviousConfig,
			)
			if restoreErr == nil {
				_ = removeAutomationProviderFile(s.transactionPath)
			}
			return errors.Join(err, restoreErr)
		}
	}
	if err := removeAutomationProviderFile(s.transactionPath); err != nil {
		Warn("清理 provider 事务标记失败，将在下次启动时确认", "err", err)
	}
	return nil
}

func (s *AutomationProviderConfigStore) persistSecretLocked(apiKey string) error {
	ctx := context.Background()
	if apiKey == "" {
		return s.secrets.Delete(ctx, automationProviderAPIKeySecret)
	}
	return s.secrets.Put(ctx, automationProviderAPIKeySecret, apiKey)
}

func (s *AutomationProviderConfigStore) recoverPendingTransaction() error {
	exists, err := automationProviderRegularFile(s.transactionPath)
	if err != nil || !exists {
		return err
	}
	data, err := os.ReadFile(s.transactionPath)
	if err != nil {
		return WrapAutomationPersistence("read provider transaction", err)
	}
	var transaction automationProviderTransaction
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transaction); err != nil {
		return NewAutomationValidationError("provider.transaction", err.Error())
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return NewAutomationValidationError("provider.transaction", "must contain one JSON object")
	}
	if err := validateAutomationProviderTransaction(transaction); err != nil {
		return err
	}

	currentConfig, currentPersisted, err := readAutomationProviderConfigIfPresent(s.configPath)
	if err != nil {
		return err
	}
	currentSecret, currentSecretSet, err := s.secrets.Get(
		context.Background(),
		automationProviderAPIKeySecret,
	)
	if err != nil {
		return err
	}
	currentConfigSHA256 := automationProviderConfigSHA256(currentConfig)
	currentSecretSHA256 := automationProviderSecretSHA256(currentSecret)
	nextConfigMatches := currentPersisted &&
		currentConfigSHA256 == transaction.NextConfigSHA256
	nextSecretMatches := currentSecretSet == transaction.NextSecretSet &&
		currentSecretSHA256 == transaction.NextSecretSHA256
	if nextConfigMatches && nextSecretMatches {
		return removeAutomationProviderFile(s.transactionPath)
	}

	previousConfigMatches := currentPersisted == transaction.PreviousPersisted
	if transaction.PreviousPersisted {
		previousConfigMatches = previousConfigMatches &&
			currentConfigSHA256 == transaction.PreviousConfigSHA256
	}
	previousSecretMatches := currentSecretSet == transaction.PreviousSecretSet &&
		currentSecretSHA256 == transaction.PreviousSecretSHA256
	if !previousSecretMatches {
		return WrapAutomationPersistence(
			"recover provider transaction",
			errors.New("provider config and API key transaction is inconsistent"),
		)
	}
	if !previousConfigMatches {
		if err := restoreAutomationProviderConfig(
			s.configPath,
			transaction.PreviousPersisted,
			transaction.PreviousConfig,
		); err != nil {
			return err
		}
	}
	return removeAutomationProviderFile(s.transactionPath)
}

func newAutomationProviderTransaction(
	previousPersisted bool,
	previousConfig AutomationProviderConfig,
	previousSecretSet bool,
	previousSecret string,
	nextConfig AutomationProviderConfig,
	nextSecret string,
) (automationProviderTransaction, error) {
	if _, err := automationProviderConfigData(previousConfig); err != nil {
		return automationProviderTransaction{}, err
	}
	if _, err := automationProviderConfigData(nextConfig); err != nil {
		return automationProviderTransaction{}, err
	}
	previousSecret = strings.TrimSpace(previousSecret)
	nextSecret = strings.TrimSpace(nextSecret)
	return automationProviderTransaction{
		Version:              automationProviderTransactionVersion,
		PreviousPersisted:    previousPersisted,
		PreviousConfig:       cloneAutomationProviderConfig(previousConfig),
		PreviousConfigSHA256: automationProviderConfigSHA256(previousConfig),
		NextConfig:           cloneAutomationProviderConfig(nextConfig),
		NextConfigSHA256:     automationProviderConfigSHA256(nextConfig),
		PreviousSecretSet:    previousSecretSet,
		PreviousSecretSHA256: automationProviderSecretSHA256(previousSecret),
		NextSecretSet:        nextSecret != "",
		NextSecretSHA256:     automationProviderSecretSHA256(nextSecret),
	}, nil
}

func validateAutomationProviderTransaction(transaction automationProviderTransaction) error {
	if transaction.Version != automationProviderTransactionVersion {
		return NewAutomationValidationError("provider.transaction.version", "is unsupported")
	}
	if transaction.PreviousConfigSHA256 != automationProviderConfigSHA256(transaction.PreviousConfig) ||
		transaction.NextConfigSHA256 != automationProviderConfigSHA256(transaction.NextConfig) {
		return NewAutomationValidationError("provider.transaction", "config digest mismatch")
	}
	for field, value := range map[string]string{
		"previous_secret_sha256": transaction.PreviousSecretSHA256,
		"next_secret_sha256":     transaction.NextSecretSHA256,
	} {
		if len(value) != sha256.Size*2 {
			return NewAutomationValidationError("provider.transaction."+field, "is invalid")
		}
	}
	return nil
}

func automationProviderConfigData(config AutomationProviderConfig) ([]byte, error) {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, WrapAutomationPersistence("encode provider config", err)
	}
	return append(data, '\n'), nil
}

func automationProviderConfigSHA256(config AutomationProviderConfig) string {
	data, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:])
}

func automationProviderSecretSHA256(secret string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return fmt.Sprintf("%x", digest[:])
}

func writeAutomationProviderTransaction(
	path string,
	transaction automationProviderTransaction,
) error {
	data, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return WrapAutomationPersistence("encode provider transaction", err)
	}
	return writeAutomationProviderFileAtomic(path, append(data, '\n'))
}

func readAutomationProviderConfigIfPresent(
	path string,
) (AutomationProviderConfig, bool, error) {
	exists, err := automationProviderRegularFile(path)
	if err != nil || !exists {
		return AutomationProviderConfig{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AutomationProviderConfig{}, false, WrapAutomationPersistence("read provider config", err)
	}
	var config AutomationProviderConfig
	if err := decodeAutomationProviderConfig(data, &config); err != nil {
		return AutomationProviderConfig{}, false, err
	}
	return config, true, nil
}

func restoreAutomationProviderConfig(
	path string,
	persisted bool,
	config AutomationProviderConfig,
) error {
	if !persisted {
		return removeAutomationProviderFile(path)
	}
	data, err := automationProviderConfigData(config)
	if err != nil {
		return err
	}
	return writeAutomationProviderFileAtomic(path, data)
}

func removeAutomationProviderFile(path string) error {
	exists, err := automationProviderRegularFile(path)
	if err != nil || !exists {
		return err
	}
	if err := os.Remove(path); err != nil {
		return WrapAutomationPersistence("remove provider file", err)
	}
	if err := syncAutomationDirectory(filepath.Dir(path)); err != nil {
		return WrapAutomationPersistence("sync provider directory", err)
	}
	return nil
}

// automationProviderEffectiveAPIKey falls back to the ONEBOT_AI_API_KEY
// environment variable when no key is stored, so an operator can clear the
// stored key and rely on the auto-rotating environment key instead of
// re-applying a snapshot every time the upstream token changes.
func automationProviderEffectiveAPIKey(stored string) string {
	if key := strings.TrimSpace(stored); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("ONEBOT_AI_API_KEY"))
}

func newConfiguredOpenAIAutomationProvider(config AutomationProviderConfig, apiKey string) (*OpenAICompatibleAutomationProvider, error) {
	return newConfiguredOpenAIAutomationProviderWithClient(config, apiKey, nil)
}

func newConfiguredOpenAIAutomationProviderWithClient(
	config AutomationProviderConfig,
	apiKey string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	if client == nil {
		client = &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second}
	}
	return newOpenAICompatibleAutomationProviderWithProtocolAndPromptCacheKey(
		config.Protocol,
		config.Endpoint,
		config.Model,
		apiKey,
		config.PromptTemplate,
		client,
		config.ChatCompletionsPromptCacheKey,
	)
}

func (s *AutomationProviderConfigStore) recordConnectionTest(
	config AutomationProviderConfig,
	apiKey string,
	testErr error,
) {
	status := AutomationProviderConnectionTestPassed
	errorText := ""
	if testErr != nil {
		status = AutomationProviderConnectionTestFailed
		errorText = testErr.Error()
		if apiKey != "" {
			errorText = strings.ReplaceAll(errorText, apiKey, "[redacted]")
		}
		if len(errorText) > 500 {
			errorText = errorText[:500]
		}
	}
	protocol, err := normalizeAutomationProviderProtocol(config.Protocol)
	if err == nil {
		config.Protocol = protocol
	}
	s.mu.Lock()
	s.latestTest = &AutomationProviderConnectionTest{
		Status:                   status,
		Protocol:                 config.Protocol,
		TestedAt:                 time.Now().UTC(),
		Error:                    errorText,
		configurationFingerprint: automationProviderConfigurationFingerprint(config, apiKey),
	}
	s.mu.Unlock()
}

func decodeAutomationProviderConfig(data []byte, destination *AutomationProviderConfig) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewAutomationValidationError("provider.json", err.Error())
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return NewAutomationValidationError("provider.json", "must contain one JSON object")
	}
	return nil
}

func automationProviderRegularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, WrapAutomationPersistence("inspect provider file", err)
	case info.Mode()&os.ModeSymlink != 0:
		return false, WrapAutomationPersistence("inspect provider file", errors.New("provider file must not be a symbolic link"))
	case !info.Mode().IsRegular():
		return false, WrapAutomationPersistence("inspect provider file", errors.New("provider file must be regular"))
	default:
		return true, nil
	}
}

func writeAutomationProviderFileAtomic(path string, data []byte) error {
	if err := ensureAutomationParent(path); err != nil {
		return err
	}
	if _, err := automationProviderRegularFile(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return WrapAutomationPersistence("create provider temporary file", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return WrapAutomationPersistence("set provider temporary file permissions", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return WrapAutomationPersistence("write provider temporary file", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return WrapAutomationPersistence("sync provider temporary file", err)
	}
	if err := temp.Close(); err != nil {
		return WrapAutomationPersistence("close provider temporary file", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return WrapAutomationPersistence("replace provider file", err)
	}
	if err := syncAutomationDirectory(parent); err != nil {
		return WrapAutomationPersistence("sync provider directory", err)
	}
	return nil
}

func nextAutomationAudienceID(audiences []AutomationAudience) string {
	var highest uint64
	for _, audience := range audiences {
		if !strings.HasPrefix(audience.ID, "aud_") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimPrefix(audience.ID, "aud_"), 10, 64)
		if err == nil && value > highest {
			highest = value
		}
	}
	return fmt.Sprintf("aud_%08d", highest+1)
}

func automationAudienceIndex(audiences []AutomationAudience, id string) int {
	for i := range audiences {
		if audiences[i].ID == id {
			return i
		}
	}
	return -1
}

func ensureAutomationAudiencePeerUnique(audiences []AutomationAudience, audience AutomationAudience, excludeID string) error {
	for _, existing := range audiences {
		if existing.ID != excludeID && existing.ChatType == audience.ChatType && existing.PeerID == audience.PeerID {
			return NewAutomationValidationError("peer_id", "is already configured for this chat type")
		}
	}
	return nil
}
