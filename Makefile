# Prism: quick builds and local dev runs.
#
#   make dev        tray app with hot reload, panel pinned open (PRISM_PIN_PANEL=1)
#   make panel      panel alone in the browser, backed by the mock (no Rust build)
#   make build      release bundle via tauri build
#   make check      fmt + clippy + every test, the same set CI runs
#   make stop       kill a dev run that outlived its terminal
#
# The dev build shares ~/.config/dev.prism.gateway with the installed app, so with
# the installed Prism running you get the real "port in use" notice on 9086.

SHELL := /bin/bash
DESKTOP := apps/desktop
PNPM := pnpm --dir $(DESKTOP)

.PHONY: dev panel build check fmt clippy test test-core test-desktop test-panel install stop clean

NODE_MODULES := $(DESKTOP)/node_modules

dev: $(NODE_MODULES)
	cd $(DESKTOP) && PRISM_PIN_PANEL=1 pnpm tauri dev

panel: $(NODE_MODULES)
	$(PNPM) dev

build: $(NODE_MODULES)
	cd $(DESKTOP) && pnpm tauri build

check: fmt clippy test

fmt:
	cargo fmt --all -- --check

clippy:
	cargo clippy -p prism-core -p prism-desktop --all-targets -- -D warnings

test: test-core test-desktop test-panel

test-core:
	cargo test -p prism-core

test-desktop:
	cargo test -p prism-desktop --lib

test-panel: $(NODE_MODULES)
	$(PNPM) exec tsc --noEmit -p .
	$(PNPM) test
	$(PNPM) build

install: $(NODE_MODULES)

$(NODE_MODULES): $(DESKTOP)/package.json $(DESKTOP)/pnpm-lock.yaml
	$(PNPM) install --frozen-lockfile
	@touch $@

# Kills the dev tauri/vite processes by exact name, then whatever still holds the dev ports.
# The installed /usr/bin/prism-desktop is spared: only the workspace's debug binary is matched.
stop:
	-pkill -9 -x cargo-tauri
	-pgrep -x prism-desktop -a | grep -F "$(CURDIR)/target/debug/" | awk '{print $$1}' | xargs -r kill -9
	-ss -ltnp 2>/dev/null | grep -E ':(1420|1421) ' | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u | xargs -r kill -9

clean:
	cargo clean
	rm -rf $(DESKTOP)/dist
