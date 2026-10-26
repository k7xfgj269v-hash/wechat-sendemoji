package main

import (
	"errors"
	"strings"
	"time"
)

var errRecruitingConfigNoChange = errors.New("recruiting config unchanged")

const RecruitingSourceGateModeJobRelevance = "job_relevance"

type RecruitingProjectUpdate struct {
	OrganizationName        *string `json:"organization_name,omitempty"`
	RequireAuthoritativeJob *bool   `json:"require_authoritative_job,omitempty"`
}

type RecruitingSourceGroupUpsert struct {
	ID          string `json:"id,omitempty"`
	GroupID     string `json:"group_id"`
	DisplayName string `json:"display_name,omitempty"`
	Enabled     bool   `json:"enabled"`
	GateEnabled bool   `json:"gate_enabled"`
	GateMode    string `json:"gate_mode,omitempty"`
}

type recruitingSourceGroupNotFoundError struct {
	id string
}

func (e *recruitingSourceGroupNotFoundError) Error() string {
	return "recruiting source group not found"
}

func (s *CRMStore) RecruitingProject() RecruitingProjectConfig {
	if s == nil {
		return RecruitingProjectConfig{}
	}
	state := s.Snapshot()
	return cloneRecruitingProjectConfig(state.RecruitingProject)
}

func (s *CRMStore) UpdateRecruitingProject(
	expectedVersion uint64,
	update RecruitingProjectUpdate,
) (RecruitingProjectConfig, error) {
	if s == nil {
		return RecruitingProjectConfig{}, NewCRMValidationError("store", "is required")
	}
	if err := validateCRMVersion("expected_version", expectedVersion); err != nil {
		return RecruitingProjectConfig{}, err
	}
	normalized, err := normalizeRecruitingProjectUpdate(update)
	if err != nil {
		return RecruitingProjectConfig{}, err
	}

	var current RecruitingProjectConfig
	err = s.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "operator",
		Operation: "recruiting.project.update",
	}, func(state *CRMState, _ *IDAllocator) error {
		if state.RecruitingProject.Version != expectedVersion {
			return NewCRMConflictError("recruiting project version conflict")
		}
		current = cloneRecruitingProjectConfig(state.RecruitingProject)
		changed := false
		if normalized.OrganizationName != nil &&
			state.RecruitingProject.OrganizationName != *normalized.OrganizationName {
			state.RecruitingProject.OrganizationName = *normalized.OrganizationName
			changed = true
		}
		if normalized.RequireAuthoritativeJob != nil &&
			state.RecruitingProject.RequireAuthoritativeJob != *normalized.RequireAuthoritativeJob {
			state.RecruitingProject.RequireAuthoritativeJob = *normalized.RequireAuthoritativeJob
			changed = true
		}
		if !changed {
			return errRecruitingConfigNoChange
		}
		if state.RecruitingProject.Version == ^uint64(0) {
			return NewCRMConflictError("recruiting project version exhausted")
		}
		state.RecruitingProject.Version++
		return nil
	})
	if errors.Is(err, errRecruitingConfigNoChange) {
		return current, nil
	}
	if err != nil {
		return RecruitingProjectConfig{}, err
	}
	return s.RecruitingProject(), nil
}

func (s *CRMStore) UpsertRecruitingSourceGroup(
	expectedProjectVersion uint64,
	input RecruitingSourceGroupUpsert,
) (RecruitingProjectConfig, error) {
	if s == nil {
		return RecruitingProjectConfig{}, NewCRMValidationError("store", "is required")
	}
	if err := validateCRMVersion("expected_project_version", expectedProjectVersion); err != nil {
		return RecruitingProjectConfig{}, err
	}
	normalized, err := normalizeRecruitingSourceGroupUpsert(input)
	if err != nil {
		return RecruitingProjectConfig{}, err
	}

	operation := "recruiting.source.create"
	if normalized.ID != "" {
		operation = "recruiting.source.update"
	}
	var current RecruitingProjectConfig
	err = s.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "operator",
		Operation:  operation,
		EntityType: "recruiting_source_group",
	}, func(state *CRMState, allocator *IDAllocator) error {
		project := &state.RecruitingProject
		if project.Version != expectedProjectVersion {
			return NewCRMConflictError("recruiting project version conflict")
		}
		current = cloneRecruitingProjectConfig(*project)

		if normalized.ID == "" {
			if recruitingSourceGroupIndexByGroupID(project.SourceGroups, normalized.GroupID) >= 0 {
				return NewCRMConflictError("recruiting source group already exists")
			}
			sourceID, nextErr := allocator.Next(IDKindSourceGroup)
			if nextErr != nil {
				return nextErr
			}
			now := time.Now().UTC()
			project.SourceGroups = append(project.SourceGroups, RecruitingSourceGroup{
				ID:          sourceID,
				GroupID:     normalized.GroupID,
				DisplayName: normalized.DisplayName,
				Enabled:     normalized.Enabled,
				GateEnabled: normalized.GateEnabled,
				GateMode:    normalized.GateMode,
				Version:     1,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		} else {
			index := recruitingSourceGroupIndexByID(project.SourceGroups, normalized.ID)
			if index < 0 {
				return &recruitingSourceGroupNotFoundError{id: normalized.ID}
			}
			duplicateIndex := recruitingSourceGroupIndexByGroupID(project.SourceGroups, normalized.GroupID)
			if duplicateIndex >= 0 && duplicateIndex != index {
				return NewCRMConflictError("recruiting source group already exists")
			}
			source := &project.SourceGroups[index]
			if source.GroupID == normalized.GroupID &&
				source.DisplayName == normalized.DisplayName &&
				source.Enabled == normalized.Enabled &&
				source.GateEnabled == normalized.GateEnabled &&
				source.GateMode == normalized.GateMode {
				return errRecruitingConfigNoChange
			}
			if source.Version == ^uint64(0) {
				return NewCRMConflictError("recruiting source group version exhausted")
			}
			source.GroupID = normalized.GroupID
			source.DisplayName = normalized.DisplayName
			source.Enabled = normalized.Enabled
			source.GateEnabled = normalized.GateEnabled
			source.GateMode = normalized.GateMode
			source.Version++
			source.UpdatedAt = time.Now().UTC()
		}
		if project.Version == ^uint64(0) {
			return NewCRMConflictError("recruiting project version exhausted")
		}
		project.Version++
		return nil
	})
	if errors.Is(err, errRecruitingConfigNoChange) {
		return current, nil
	}
	if err != nil {
		return RecruitingProjectConfig{}, err
	}
	return s.RecruitingProject(), nil
}

func (s *CRMStore) RemoveRecruitingSourceGroup(
	groupID string,
	expectedProjectVersion uint64,
) (RecruitingProjectConfig, error) {
	if s == nil {
		return RecruitingProjectConfig{}, NewCRMValidationError("store", "is required")
	}
	if err := validateCRMVersion("expected_project_version", expectedProjectVersion); err != nil {
		return RecruitingProjectConfig{}, err
	}
	normalizedGroupID, err := normalizeRecruitingGroupID(groupID)
	if err != nil {
		return RecruitingProjectConfig{}, err
	}

	err = s.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "operator",
		Operation:  "recruiting.source.remove",
		EntityType: "recruiting_source_group",
	}, func(state *CRMState, _ *IDAllocator) error {
		project := &state.RecruitingProject
		if project.Version != expectedProjectVersion {
			return NewCRMConflictError("recruiting project version conflict")
		}
		index := recruitingSourceGroupIndexByGroupID(project.SourceGroups, normalizedGroupID)
		if index < 0 {
			return &recruitingSourceGroupNotFoundError{id: normalizedGroupID}
		}
		project.SourceGroups = append(
			project.SourceGroups[:index],
			project.SourceGroups[index+1:]...,
		)
		if project.Version == ^uint64(0) {
			return NewCRMConflictError("recruiting project version exhausted")
		}
		project.Version++
		return nil
	})
	if err != nil {
		return RecruitingProjectConfig{}, err
	}
	return s.RecruitingProject(), nil
}

func normalizeRecruitingProjectUpdate(
	update RecruitingProjectUpdate,
) (RecruitingProjectUpdate, error) {
	if update.OrganizationName == nil && update.RequireAuthoritativeJob == nil {
		return RecruitingProjectUpdate{}, NewCRMValidationError("update", "must contain a field")
	}
	if update.OrganizationName != nil {
		normalized, err := normalizeCRMString(
			"organization_name",
			*update.OrganizationName,
			160,
			true,
			false,
		)
		if err != nil {
			return RecruitingProjectUpdate{}, err
		}
		update.OrganizationName = &normalized
	}
	return update, nil
}

func normalizeRecruitingSourceGroupUpsert(
	input RecruitingSourceGroupUpsert,
) (RecruitingSourceGroupUpsert, error) {
	var err error
	if input.ID, err = normalizeCRMString("id", input.ID, 80, false, false); err != nil {
		return RecruitingSourceGroupUpsert{}, err
	}
	if input.GroupID, err = normalizeRecruitingGroupID(input.GroupID); err != nil {
		return RecruitingSourceGroupUpsert{}, err
	}
	if input.DisplayName, err = normalizeCRMString(
		"display_name",
		input.DisplayName,
		160,
		false,
		false,
	); err != nil {
		return RecruitingSourceGroupUpsert{}, err
	}
	if input.GateMode, err = normalizeCRMString(
		"gate_mode",
		input.GateMode,
		80,
		false,
		false,
	); err != nil {
		return RecruitingSourceGroupUpsert{}, err
	}
	if input.GateEnabled && input.GateMode == "" {
		input.GateMode = RecruitingSourceGateModeJobRelevance
	}
	if input.GateMode != "" && input.GateMode != RecruitingSourceGateModeJobRelevance {
		return RecruitingSourceGroupUpsert{}, NewCRMValidationError(
			"gate_mode",
			"must be job_relevance",
		)
	}
	return input, nil
}

func normalizeRecruitingGroupID(value string) (string, error) {
	normalized, err := normalizeCRMString("group_id", value, 256, true, false)
	if err != nil {
		return "", err
	}
	const suffix = "@chatroom"
	if !strings.HasSuffix(normalized, suffix) {
		return "", NewCRMValidationError("group_id", "must be a WeChat group identifier")
	}
	prefix := strings.TrimSuffix(normalized, suffix)
	if prefix == "" {
		return "", NewCRMValidationError("group_id", "must be a WeChat group identifier")
	}
	for _, char := range prefix {
		if char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '_' || char == '-' || char == '.' {
			continue
		}
		return "", NewCRMValidationError("group_id", "must be a WeChat group identifier")
	}
	return normalized, nil
}

func recruitingSourceGroupIndexByID(groups []RecruitingSourceGroup, id string) int {
	for index := range groups {
		if groups[index].ID == id {
			return index
		}
	}
	return -1
}

func recruitingSourceGroupIndexByGroupID(groups []RecruitingSourceGroup, groupID string) int {
	for index := range groups {
		if groups[index].GroupID == groupID {
			return index
		}
	}
	return -1
}

func cloneRecruitingProjectConfig(project RecruitingProjectConfig) RecruitingProjectConfig {
	project.SourceGroups = append([]RecruitingSourceGroup{}, project.SourceGroups...)
	return project
}
