package main

import (
	"strings"
	"testing"
)

func TestWebOpsOnboardingFleetContract(t *testing.T) {
	script := crmPipelineViewReadAsset(t, "web/ops_onboarding_fleet.js")
	style := crmPipelineViewReadAsset(t, "web/ops_onboarding_fleet.css")

	crmPipelineViewAssertContains(t, script, []string{
		"window.OneBotWorkbench",
		`id: "onboarding-fleet"`,
		`label: "入职与车队"`,
		`"/api/ops/onboarding_cases"`,
		`"/fleet"`,
		`"/fleet/transition"`,
		`expected_version: state.detail.version`,
		"recoverStale",
		"api.escapeHTML(",
		"onboarding_cases",
	})
	crmPipelineViewAssertContains(t, style, []string{
		".opsOnboardingFleet-root",
		".opsOnboardingFleet-workspace",
		"overflow-x: auto",
		"max-width: 100%",
		"@media (max-width: 430px)",
	})
	for _, asset := range []string{script, style} {
		body := strings.ToLower(asset)
		for _, forbidden := range []string{"http://", "https://", "@import", "gradient("} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("onboarding asset contains forbidden marker %q", forbidden)
			}
		}
	}
}
