package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	DataBundleSchemaVersion         = 1
	DataProviderStateSchemaVersion  = 1
	DataAuditPartitionSchemaVersion = 1
	DataBundleMaxBytes              = 64 << 20

	DataBundleManifestName   = "manifest.json"
	DataBundleCRMName        = "crm/snapshot.json"
	DataBundleAutomationName = "automation/snapshot.json"
	DataBundleProviderName   = "automation/provider-public.json"
)

var (
	ErrDataBundleInvalid  = errors.New("invalid data bundle")
	ErrDataBundleTooLarge = errors.New("data bundle exceeds 64 MiB")
)

type DataExportOptions struct {
	IncludeAudit bool `json:"include_audit"`
}

type DataBundleManifestEntry struct {
	Name          string `json:"name"`
	SchemaVersion int    `json:"schema_version"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

type DataBundleManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Entries       []DataBundleManifestEntry `json:"entries"`
}

type DataBundleValidation struct {
	Valid    bool               `json:"valid"`
	Size     int64              `json:"size"`
	Manifest DataBundleManifest `json:"manifest"`
}

type DataManagementStatus struct {
	Ready                   bool `json:"ready"`
	BundleSchemaVersion     int  `json:"bundle_schema_version"`
	CRMSchemaVersion        int  `json:"crm_schema_version"`
	AutomationSchemaVersion int  `json:"automation_schema_version"`
	ProviderSchemaVersion   int  `json:"provider_schema_version"`
	AuditSchemaVersion      int  `json:"audit_schema_version"`
	MaxValidationBytes      int  `json:"max_validation_bytes"`
}

type DataManagementService struct {
	crm        *CRMStore
	automation *AutomationStore
	provider   *AutomationProviderConfigStore
	audit      *AutomationAuditLedger
}

type dataProviderState struct {
	SchemaVersion int                            `json:"schema_version"`
	Provider      AutomationProviderConfigPublic `json:"provider"`
}

type dataBundleEntry struct {
	name          string
	schemaVersion int
	data          []byte
}

type dataBundleError struct {
	message string
	cause   error
}

func (e *dataBundleError) Error() string {
	return e.message
}

func (e *dataBundleError) Unwrap() error {
	return e.cause
}

func NewDataManagementService(
	crm *CRMStore,
	automation *AutomationStore,
	provider *AutomationProviderConfigStore,
	audit *AutomationAuditLedger,
) *DataManagementService {
	return &DataManagementService{
		crm:        crm,
		automation: automation,
		provider:   provider,
		audit:      audit,
	}
}

func (s *DataManagementService) Status() DataManagementStatus {
	return DataManagementStatus{
		Ready:                   s != nil && s.crm != nil && s.automation != nil && s.provider != nil,
		BundleSchemaVersion:     DataBundleSchemaVersion,
		CRMSchemaVersion:        CRMSchemaVersion,
		AutomationSchemaVersion: AutomationSnapshotSchemaVersion,
		ProviderSchemaVersion:   DataProviderStateSchemaVersion,
		AuditSchemaVersion:      DataAuditPartitionSchemaVersion,
		MaxValidationBytes:      DataBundleMaxBytes,
	}
}

func (s *DataManagementService) Export(
	ctx context.Context,
	options DataExportOptions,
	writer io.Writer,
) (DataBundleManifest, error) {
	if ctx == nil {
		return DataBundleManifest{}, errors.New("data export context is required")
	}
	if writer == nil {
		return DataBundleManifest{}, errors.New("data export writer is required")
	}
	if s == nil || s.crm == nil || s.automation == nil || s.provider == nil {
		return DataBundleManifest{}, errors.New("data management service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return DataBundleManifest{}, err
	}

	crmState := s.crm.Snapshot()
	automationState := s.automation.Snapshot()
	providerState := dataProviderState{
		SchemaVersion: DataProviderStateSchemaVersion,
		Provider:      s.provider.Public(),
	}

	entries := make([]dataBundleEntry, 0, 3)
	crmData, err := marshalDataBundleJSON(crmState)
	if err != nil {
		return DataBundleManifest{}, err
	}
	entries = append(entries, dataBundleEntry{
		name:          DataBundleCRMName,
		schemaVersion: crmState.SchemaVersion,
		data:          crmData,
	})

	automationData, err := marshalDataBundleJSON(automationState)
	if err != nil {
		return DataBundleManifest{}, err
	}
	entries = append(entries, dataBundleEntry{
		name:          DataBundleAutomationName,
		schemaVersion: automationState.SchemaVersion,
		data:          automationData,
	})

	providerData, err := marshalDataBundleJSON(providerState)
	if err != nil {
		return DataBundleManifest{}, err
	}
	entries = append(entries, dataBundleEntry{
		name:          DataBundleProviderName,
		schemaVersion: DataProviderStateSchemaVersion,
		data:          providerData,
	})

	if options.IncludeAudit && s.audit != nil {
		records, readErr := s.audit.ReadAll()
		if readErr != nil {
			return DataBundleManifest{}, readErr
		}
		auditEntries, partitionErr := dataAuditPartitionEntries(records)
		if partitionErr != nil {
			return DataBundleManifest{}, partitionErr
		}
		entries = append(entries, auditEntries...)
	}
	if err := ctx.Err(); err != nil {
		return DataBundleManifest{}, err
	}

	manifest := DataBundleManifest{
		SchemaVersion: DataBundleSchemaVersion,
		Entries:       make([]DataBundleManifestEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		sum := sha256.Sum256(entry.data)
		manifest.Entries = append(manifest.Entries, DataBundleManifestEntry{
			Name:          entry.name,
			SchemaVersion: entry.schemaVersion,
			Size:          int64(len(entry.data)),
			SHA256:        hex.EncodeToString(sum[:]),
		})
	}
	manifestData, err := marshalDataBundleJSON(manifest)
	if err != nil {
		return DataBundleManifest{}, err
	}

	archive := zip.NewWriter(writer)
	if err := writeDataBundleZIPEntry(archive, DataBundleManifestName, manifestData); err != nil {
		_ = archive.Close()
		return DataBundleManifest{}, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			return DataBundleManifest{}, err
		}
		if err := writeDataBundleZIPEntry(archive, entry.name, entry.data); err != nil {
			_ = archive.Close()
			return DataBundleManifest{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return DataBundleManifest{}, fmt.Errorf("close data bundle: %w", err)
	}
	return manifest, nil
}

func ValidateDataBundle(reader io.ReaderAt, size int64) (DataBundleValidation, error) {
	if reader == nil {
		return DataBundleValidation{}, newDataBundleError("data bundle reader is required", ErrDataBundleInvalid)
	}
	if size < 0 {
		return DataBundleValidation{}, newDataBundleError("data bundle size is invalid", ErrDataBundleInvalid)
	}
	if size > DataBundleMaxBytes {
		return DataBundleValidation{}, newDataBundleError(ErrDataBundleTooLarge.Error(), ErrDataBundleTooLarge)
	}

	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return DataBundleValidation{}, newDataBundleError("data bundle is not a valid ZIP archive", ErrDataBundleInvalid)
	}
	files := make(map[string]*zip.File, len(archive.File))
	var totalUncompressed uint64
	for _, file := range archive.File {
		name, nameErr := validateDataBundleEntryName(file.Name)
		if nameErr != nil {
			return DataBundleValidation{}, nameErr
		}
		if file.FileInfo().IsDir() {
			return DataBundleValidation{}, newDataBundleError("data bundle contains a directory entry", ErrDataBundleInvalid)
		}
		if _, exists := files[name]; exists {
			return DataBundleValidation{}, newDataBundleError("data bundle contains duplicate entry names", ErrDataBundleInvalid)
		}
		if dataBundleSecretEntryName(name) {
			return DataBundleValidation{}, newDataBundleError("data bundle contains a secret-bearing entry", ErrDataBundleInvalid)
		}
		totalUncompressed += file.UncompressedSize64
		if totalUncompressed > DataBundleMaxBytes {
			return DataBundleValidation{}, newDataBundleError(ErrDataBundleTooLarge.Error(), ErrDataBundleTooLarge)
		}
		files[name] = file
	}

	manifestFile, ok := files[DataBundleManifestName]
	if !ok {
		return DataBundleValidation{}, newDataBundleError("data bundle manifest is missing", ErrDataBundleInvalid)
	}
	manifestData, err := readDataBundleZIPFile(manifestFile)
	if err != nil {
		return DataBundleValidation{}, err
	}
	if err := rejectAutomationDuplicateJSONKeys("manifest", manifestData); err != nil {
		return DataBundleValidation{}, newDataBundleError("data bundle manifest contains invalid JSON", ErrDataBundleInvalid)
	}
	var manifest DataBundleManifest
	if err := decodeDataBundleJSON(manifestData, &manifest); err != nil {
		return DataBundleValidation{}, newDataBundleError("data bundle manifest contains invalid JSON", ErrDataBundleInvalid)
	}
	if manifest.SchemaVersion < 1 || manifest.SchemaVersion > DataBundleSchemaVersion {
		return DataBundleValidation{}, newDataBundleError("data bundle uses an unsupported future schema", ErrDataBundleInvalid)
	}

	manifestEntries := make(map[string]DataBundleManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		name, nameErr := validateDataBundleEntryName(entry.Name)
		if nameErr != nil || name == DataBundleManifestName {
			return DataBundleValidation{}, newDataBundleError("data bundle manifest contains an invalid entry name", ErrDataBundleInvalid)
		}
		if _, exists := manifestEntries[name]; exists {
			return DataBundleValidation{}, newDataBundleError("data bundle manifest contains duplicate entries", ErrDataBundleInvalid)
		}
		if entry.Size < 0 || len(entry.SHA256) != sha256.Size*2 {
			return DataBundleValidation{}, newDataBundleError("data bundle manifest contains invalid entry metadata", ErrDataBundleInvalid)
		}
		if _, decodeErr := hex.DecodeString(entry.SHA256); decodeErr != nil {
			return DataBundleValidation{}, newDataBundleError("data bundle manifest contains an invalid checksum", ErrDataBundleInvalid)
		}
		if err := validateDataBundleEntrySchema(name, entry.SchemaVersion); err != nil {
			return DataBundleValidation{}, err
		}
		manifestEntries[name] = entry
	}
	if len(files) != len(manifestEntries)+1 {
		return DataBundleValidation{}, newDataBundleError("data bundle contains unmanifested or missing entries", ErrDataBundleInvalid)
	}
	for name := range files {
		if name == DataBundleManifestName {
			continue
		}
		if _, exists := manifestEntries[name]; !exists {
			return DataBundleValidation{}, newDataBundleError("data bundle contains an unmanifested entry", ErrDataBundleInvalid)
		}
	}
	for name, metadata := range manifestEntries {
		file, exists := files[name]
		if !exists {
			return DataBundleValidation{}, newDataBundleError("data bundle is missing a manifested entry", ErrDataBundleInvalid)
		}
		data, readErr := readDataBundleZIPFile(file)
		if readErr != nil {
			return DataBundleValidation{}, readErr
		}
		if int64(len(data)) != metadata.Size {
			return DataBundleValidation{}, newDataBundleError("data bundle entry size does not match the manifest", ErrDataBundleInvalid)
		}
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), metadata.SHA256) {
			return DataBundleValidation{}, newDataBundleError("data bundle entry checksum does not match the manifest", ErrDataBundleInvalid)
		}
		if err := validateDataBundleEntryData(name, metadata.SchemaVersion, data); err != nil {
			return DataBundleValidation{}, err
		}
	}
	return DataBundleValidation{
		Valid:    true,
		Size:     size,
		Manifest: manifest,
	}, nil
}

func marshalDataBundleJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode data bundle entry: %w", err)
	}
	return append(data, '\n'), nil
}

func writeDataBundleZIPEntry(archive *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create data bundle entry: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write data bundle entry: %w", err)
	}
	return nil
}

func dataAuditPartitionEntries(records []AutomationAuditRecord) ([]dataBundleEntry, error) {
	partitions := make(map[string][]AutomationAuditRecord)
	for _, record := range records {
		date := record.CreatedAt.UTC().Format("2006-01-02")
		partitions[date] = append(partitions[date], cloneAutomationAuditRecord(record))
	}
	dates := make([]string, 0, len(partitions))
	for date := range partitions {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	entries := make([]dataBundleEntry, 0, len(dates))
	for _, date := range dates {
		partition := partitions[date]
		sort.Slice(partition, func(i, j int) bool {
			if partition[i].CreatedAt.Equal(partition[j].CreatedAt) {
				return partition[i].ID < partition[j].ID
			}
			return partition[i].CreatedAt.Before(partition[j].CreatedAt)
		})
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		encoder.SetEscapeHTML(false)
		for _, record := range partition {
			if err := encoder.Encode(record); err != nil {
				return nil, fmt.Errorf("encode audit partition: %w", err)
			}
		}
		entries = append(entries, dataBundleEntry{
			name:          "audit/" + date + ".jsonl",
			schemaVersion: DataAuditPartitionSchemaVersion,
			data:          data.Bytes(),
		})
	}
	return entries, nil
}

func validateDataBundleEntryName(name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return "", newDataBundleError("data bundle contains an unsafe entry name", ErrDataBundleInvalid)
	}
	cleaned := path.Clean(name)
	if cleaned != name || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", newDataBundleError("data bundle contains an unsafe entry name", ErrDataBundleInvalid)
	}
	return cleaned, nil
}

func dataBundleSecretEntryName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{
		"provider.key",
		"api_key",
		"api-key",
		"token",
		"secret",
		"credential",
		"password",
		".pem",
		".p12",
		".sock",
		"socket",
		".lock",
		"recovery",
		"rollback",
		".tmp",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func validateDataBundleEntrySchema(name string, schemaVersion int) error {
	maxVersion := 0
	switch {
	case name == DataBundleCRMName:
		maxVersion = CRMSchemaVersion
	case name == DataBundleAutomationName:
		maxVersion = AutomationSnapshotSchemaVersion
	case name == DataBundleProviderName:
		maxVersion = DataProviderStateSchemaVersion
	case dataBundleAuditEntryName(name):
		maxVersion = DataAuditPartitionSchemaVersion
	default:
		return newDataBundleError("data bundle contains an unsupported entry", ErrDataBundleInvalid)
	}
	if schemaVersion < 1 || schemaVersion > maxVersion {
		return newDataBundleError("data bundle entry uses an unsupported future schema", ErrDataBundleInvalid)
	}
	return nil
}

func dataBundleAuditEntryName(name string) bool {
	if !strings.HasPrefix(name, "audit/") || !strings.HasSuffix(name, ".jsonl") {
		return false
	}
	dateText := strings.TrimSuffix(strings.TrimPrefix(name, "audit/"), ".jsonl")
	date, err := time.Parse("2006-01-02", dateText)
	return err == nil && date.Format("2006-01-02") == dateText
}

func readDataBundleZIPFile(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > DataBundleMaxBytes {
		return nil, newDataBundleError(ErrDataBundleTooLarge.Error(), ErrDataBundleTooLarge)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, newDataBundleError("data bundle entry cannot be opened", ErrDataBundleInvalid)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, DataBundleMaxBytes+1))
	if err != nil {
		return nil, newDataBundleError("data bundle entry cannot be read", ErrDataBundleInvalid)
	}
	if len(data) > DataBundleMaxBytes {
		return nil, newDataBundleError(ErrDataBundleTooLarge.Error(), ErrDataBundleTooLarge)
	}
	return data, nil
}

func validateDataBundleEntryData(name string, schemaVersion int, data []byte) error {
	if dataBundleAuditEntryName(name) {
		return validateDataBundleAuditPartition(data)
	}
	if err := rejectAutomationDuplicateJSONKeys(name, data); err != nil {
		return newDataBundleError("data bundle entry contains invalid JSON", ErrDataBundleInvalid)
	}
	if err := rejectDataBundleSecretJSON(data); err != nil {
		return err
	}
	switch name {
	case DataBundleCRMName, DataBundleAutomationName:
		var version struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &version); err != nil || version.SchemaVersion != schemaVersion {
			return newDataBundleError("data bundle snapshot schema does not match the manifest", ErrDataBundleInvalid)
		}
	case DataBundleProviderName:
		var state dataProviderState
		if err := decodeDataBundleJSON(data, &state); err != nil || state.SchemaVersion != schemaVersion {
			return newDataBundleError("data bundle provider state is invalid", ErrDataBundleInvalid)
		}
	default:
		return newDataBundleError("data bundle contains an unsupported entry", ErrDataBundleInvalid)
	}
	return nil
}

func validateDataBundleAuditPartition(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), DataBundleMaxBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			return newDataBundleError("data bundle audit partition contains an empty record", ErrDataBundleInvalid)
		}
		if err := rejectAutomationDuplicateJSONKeys("audit", line); err != nil {
			return newDataBundleError("data bundle audit partition contains invalid JSON", ErrDataBundleInvalid)
		}
		if err := rejectDataBundleSecretJSON(line); err != nil {
			return err
		}
		var record AutomationAuditRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return newDataBundleError("data bundle audit partition contains invalid JSON", ErrDataBundleInvalid)
		}
	}
	if err := scanner.Err(); err != nil {
		return newDataBundleError("data bundle audit partition is too large", ErrDataBundleTooLarge)
	}
	return nil
}

func rejectDataBundleSecretJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return newDataBundleError("data bundle entry contains invalid JSON", ErrDataBundleInvalid)
	}
	var walk func(any) bool
	walk = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
				if normalized != "api_key_configured" && dataBundleSecretJSONKey(normalized) {
					return true
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	if walk(value) {
		return newDataBundleError("data bundle contains secret-bearing data", ErrDataBundleInvalid)
	}
	return nil
}

func dataBundleSecretJSONKey(key string) bool {
	for _, marker := range []string{
		"api_key",
		"access_token",
		"auth_token",
		"bearer_token",
		"refresh_token",
		"password",
		"private_key",
		"client_secret",
		"credential",
		"socket_path",
		"lock_path",
		"recovery_file",
		"rollback_file",
	} {
		if key == marker || strings.HasSuffix(key, "_"+marker) {
			return true
		}
	}
	return false
}

func decodeDataBundleJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("JSON must contain one value")
	}
	return nil
}

func newDataBundleError(message string, cause error) error {
	return &dataBundleError{message: message, cause: cause}
}
