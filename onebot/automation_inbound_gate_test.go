package main

import "testing"

func TestAutomationInboundChatDefaultsOff(t *testing.T) {
	// 未设置 -> 关闭（DSH 驱动为准，避免双循环）
	t.Setenv("ONEBOT_INBOUND_AUTOMATION", "")
	if automationInboundChatEnabled() {
		t.Fatal("inbound automation must default to disabled")
	}
}

func TestAutomationInboundChatExplicitOn(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on", " On "} {
		t.Setenv("ONEBOT_INBOUND_AUTOMATION", v)
		if !automationInboundChatEnabled() {
			t.Fatalf("value %q should enable inbound automation", v)
		}
	}
}

func TestAutomationInboundChatExplicitOff(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off", "anything-else"} {
		t.Setenv("ONEBOT_INBOUND_AUTOMATION", v)
		if automationInboundChatEnabled() {
			t.Fatalf("value %q should keep inbound automation disabled", v)
		}
	}
}

func TestAutomationEnqueueEventNoopWhenDisabled(t *testing.T) {
	t.Setenv("ONEBOT_INBOUND_AUTOMATION", "")
	automationRuntimeMu.Lock()
	prev := automationRuntime
	automationRuntime = nil
	automationRuntimeMu.Unlock()
	defer func() {
		automationRuntimeMu.Lock()
		automationRuntime = prev
		automationRuntimeMu.Unlock()
	}()
	// runtime 为 nil 且开关关闭：必须安静返回，不能 panic、不能阻塞
	automationEnqueueEvent(MessageEvent{ID: "e1"})
}
