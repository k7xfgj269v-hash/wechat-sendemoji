SHELL := /bin/bash

PROJECT_ROOT := $(abspath $(CURDIR)/..)
BUILD_DIR ?= $(CURDIR)/dist
INSTALL_ROOT ?= $(HOME)/.wechat-chatter
INSTALL_BIN_DIR ?= $(INSTALL_ROOT)/bin
RUN_DIR ?= $(INSTALL_ROOT)/run
LOG_DIR ?= $(INSTALL_ROOT)/log
RESOURCE_DIR ?= $(CURDIR)
LAUNCHD_SOURCE_DIR ?= $(PROJECT_ROOT)/launchd
LAUNCHD_RENDER_DIR ?= $(BUILD_DIR)/launchd
LAUNCHD_INSTALL_DIR ?= $(HOME)/Library/LaunchAgents
HR_LISTEN_ADDR ?= 127.0.0.1:58080

GO ?= go
GO_BUILD_FLAGS ?=
COMMON_LDFLAGS ?= -s -w

# hr-workbench 按架构只经 Unix socket 访问 wechat-agent，不应链接 frida-core。
# noFrida 标签会排除 frida_controller.go 与 frida_script_loader.go，改用
# frida_controller_stub.go 的禁用实现。wechat-agent 不带此标签，保留完整 Frida。
HR_BUILD_TAGS ?= noFrida

WECHAT_AGENT := $(BUILD_DIR)/wechat-agent
HR_WORKBENCH := $(BUILD_DIR)/hr-workbench
COMPAT_WORKBENCH := $(BUILD_DIR)/onebot-workbench
WECHAT_AGENT_PLIST := $(LAUNCHD_RENDER_DIR)/com.ludwig.wechat-agent.plist
HR_WORKBENCH_PLIST := $(LAUNCHD_RENDER_DIR)/com.ludwig.hr-workbench.plist

.PHONY: build build-wechat-agent build-hr-workbench build-compat-workbench \
	render-launchd lint-launchd install-launchd clean

build: build-wechat-agent build-hr-workbench build-compat-workbench

build-wechat-agent:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build $(GO_BUILD_FLAGS) \
		-ldflags "$(COMMON_LDFLAGS) -X main.buildProcessMode=wechat-agent" \
		-o "$(WECHAT_AGENT)" .

build-hr-workbench:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build $(GO_BUILD_FLAGS) -tags "$(HR_BUILD_TAGS)" \
		-ldflags "$(COMMON_LDFLAGS) -X main.buildProcessMode=hr-workbench" \
		-o "$(HR_WORKBENCH)" .

build-compat-workbench:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build $(GO_BUILD_FLAGS) -tags "$(HR_BUILD_TAGS)" \
		-ldflags "$(COMMON_LDFLAGS) -X main.buildProcessMode=hr-workbench" \
		-o "$(COMPAT_WORKBENCH)" .

render-launchd:
	@mkdir -p "$(LAUNCHD_RENDER_DIR)"
	@for source in "$(LAUNCHD_SOURCE_DIR)"/*.plist; do \
		target="$(LAUNCHD_RENDER_DIR)/$$(basename "$$source")"; \
		sed \
			-e 's|@@BIN_DIR@@|$(INSTALL_BIN_DIR)|g' \
			-e 's|@@RUN_DIR@@|$(RUN_DIR)|g' \
			-e 's|@@HR_LISTEN_ADDR@@|$(HR_LISTEN_ADDR)|g' \
			-e 's|@@RESOURCE_DIR@@|$(RESOURCE_DIR)|g' \
			-e 's|@@LOG_DIR@@|$(LOG_DIR)|g' \
			"$$source" > "$$target"; \
	done

lint-launchd: render-launchd
	plutil -lint "$(WECHAT_AGENT_PLIST)" "$(HR_WORKBENCH_PLIST)"

install-launchd: build lint-launchd
	@install -d -m 0700 "$(INSTALL_ROOT)" "$(RUN_DIR)" "$(LOG_DIR)"
	@install -d -m 0755 "$(INSTALL_BIN_DIR)" "$(LAUNCHD_INSTALL_DIR)"
	@install -m 0755 "$(WECHAT_AGENT)" "$(INSTALL_BIN_DIR)/wechat-agent"
	@install -m 0755 "$(HR_WORKBENCH)" "$(INSTALL_BIN_DIR)/hr-workbench"
	@install -m 0755 "$(COMPAT_WORKBENCH)" "$(INSTALL_BIN_DIR)/onebot-workbench"
	@install -m 0644 "$(WECHAT_AGENT_PLIST)" "$(LAUNCHD_INSTALL_DIR)/com.ludwig.wechat-agent.plist"
	@install -m 0644 "$(HR_WORKBENCH_PLIST)" "$(LAUNCHD_INSTALL_DIR)/com.ludwig.hr-workbench.plist"
	@echo "LaunchAgents installed but not loaded or started."

clean:
	rm -rf "$(BUILD_DIR)"
