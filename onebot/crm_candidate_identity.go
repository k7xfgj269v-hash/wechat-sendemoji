package main

import (
	"strings"
	"time"
	"unicode"
)

type crmCandidateIdentity struct {
	wechatID string
	phone    string
	email    string
}

func crmCandidateIdentityFromDraft(draft CandidateDraft) crmCandidateIdentity {
	return crmCandidateIdentity{
		wechatID: strings.TrimSpace(draft.WeChatID),
		phone:    crmCandidateNormalizePhoneAlias(draft.Phone),
		email:    crmCandidateNormalizeEmailAlias(draft.Email),
	}
}

func crmCandidateBindPrivateEventIdentity(event MessageEvent, draft CandidateDraft) (CandidateDraft, error) {
	wechatID, err := crmCandidatePrivateEventWeChatID(event)
	if err != nil {
		return CandidateDraft{}, err
	}
	if supplied := strings.TrimSpace(draft.WeChatID); supplied != "" && supplied != wechatID {
		return CandidateDraft{}, crmCandidateIdentityConflict("private message WeChat identity disagrees with the candidate draft")
	}
	draft.WeChatID = wechatID
	return draft, nil
}

func crmCandidatePrivateEventWeChatID(event MessageEvent) (string, error) {
	peerID := strings.TrimSpace(event.PeerID)
	authorID := strings.TrimSpace(event.AuthorID)
	if event.ChatType != ChatTypePrivate ||
		strings.TrimSpace(event.GroupID) != "" ||
		event.Disposition == MessageDispositionSourceGroupJob ||
		event.Disposition == MessageDispositionGroupNoise ||
		crmCandidateLooksLikeGroupID(peerID) ||
		crmCandidateLooksLikeGroupID(authorID) {
		return "", crmCandidateIdentityConflict("group messages cannot resolve candidate identity")
	}
	if peerID != "" {
		return peerID, nil
	}
	if authorID != "" {
		return authorID, nil
	}
	return "", crmCandidateIdentityConflict("private message has no candidate WeChat identity")
}

func crmCandidateResolveIdentity(candidates []Candidate, identity crmCandidateIdentity) (int, error) {
	matched := make(map[int]struct{}, 1)
	keys := []struct {
		value string
		match func(Candidate) bool
	}{
		{
			value: identity.wechatID,
			match: func(candidate Candidate) bool {
				return strings.TrimSpace(candidate.WeChatID) == identity.wechatID
			},
		},
		{
			value: identity.phone,
			match: func(candidate Candidate) bool {
				return crmCandidateNormalizePhoneAlias(candidate.Phone) == identity.phone
			},
		},
		{
			value: identity.email,
			match: func(candidate Candidate) bool {
				return crmCandidateNormalizeEmailAlias(candidate.Email) == identity.email
			},
		},
	}

	for _, key := range keys {
		if key.value == "" {
			continue
		}
		keyMatches := make([]int, 0, 1)
		for index := range candidates {
			if key.match(candidates[index]) {
				keyMatches = append(keyMatches, index)
			}
		}
		if len(keyMatches) > 1 {
			return -1, crmCandidateIdentityConflict("an identity alias matches multiple candidates")
		}
		if len(keyMatches) == 1 {
			matched[keyMatches[0]] = struct{}{}
		}
	}

	if len(matched) > 1 {
		return -1, crmCandidateIdentityConflict("identity aliases match different candidates")
	}
	if len(matched) == 0 {
		return -1, nil
	}

	var candidateIndex int
	for index := range matched {
		candidateIndex = index
	}
	if err := crmCandidateValidateIdentityAgreement(candidates[candidateIndex], identity); err != nil {
		return -1, err
	}
	return candidateIndex, nil
}

func crmCandidateValidateIdentityAgreement(candidate Candidate, identity crmCandidateIdentity) error {
	if identity.wechatID != "" &&
		strings.TrimSpace(candidate.WeChatID) != "" &&
		strings.TrimSpace(candidate.WeChatID) != identity.wechatID {
		return crmCandidateIdentityConflict("WeChat identity disagrees with the matched candidate")
	}
	if identity.phone != "" &&
		strings.TrimSpace(candidate.Phone) != "" &&
		crmCandidateNormalizePhoneAlias(candidate.Phone) != identity.phone {
		return crmCandidateIdentityConflict("phone identity disagrees with the matched candidate")
	}
	if identity.email != "" &&
		strings.TrimSpace(candidate.Email) != "" &&
		crmCandidateNormalizeEmailAlias(candidate.Email) != identity.email {
		return crmCandidateIdentityConflict("email identity disagrees with the matched candidate")
	}
	return nil
}

func crmCandidateEnrichIdentity(candidate Candidate, draft CandidateDraft, now time.Time) (Candidate, bool) {
	changed := false
	if candidate.WeChatID == "" && draft.WeChatID != "" {
		candidate.WeChatID = draft.WeChatID
		changed = true
	}
	if candidate.Phone == "" && draft.Phone != "" {
		candidate.Phone = draft.Phone
		changed = true
	}
	if candidate.Email == "" && draft.Email != "" {
		candidate.Email = draft.Email
		changed = true
	}
	if changed {
		candidate.Version++
		candidate.UpdatedAt = now
	}
	return candidate, changed
}

func crmCandidateNormalizePhoneAlias(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	var digits strings.Builder
	international := false
	for index, character := range value {
		switch {
		case character >= '0' && character <= '9':
			digits.WriteRune(character)
		case character == '+' && index == 0:
			international = true
		case unicode.IsSpace(character) || strings.ContainsRune("-().", character):
		default:
			return strings.ToLower(value)
		}
	}
	normalized := digits.String()
	if normalized == "" {
		return strings.ToLower(value)
	}
	if !international && strings.HasPrefix(normalized, "00") {
		international = true
		normalized = strings.TrimPrefix(normalized, "00")
	}
	if international {
		return "+" + normalized
	}
	return normalized
}

func crmCandidateNormalizeEmailAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func crmCandidateLooksLikeGroupID(value string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(value)), "@chatroom")
}

func crmCandidateIdentityConflict(message string) error {
	return NewCRMConflictError("candidate identity conflict: " + message)
}
