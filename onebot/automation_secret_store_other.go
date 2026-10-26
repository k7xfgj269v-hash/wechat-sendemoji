//go:build !darwin

package main

func NewAutomationSecretStore(dataDir string, _ AutomationCommandRunner) AutomationSecretStore {
	return newAutomationFileSecretStore(dataDir)
}
