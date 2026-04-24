WAILS     := $(shell command -v wails 2>/dev/null || echo $(HOME)/go/bin/wails)
DB        := $(CURDIR)/opensynapse.db
GUI_DIR   := $(CURDIR)/cmd/gui
GUI_BIN   := $(GUI_DIR)/build/bin/openSynapse-ui

# Arch/CachyOS ships webkit2gtk-4.1 instead of 4.0.
# Wails v2 supports both; select the right one automatically.
# Override with: make gui WEBKIT_TAG=
WEBKIT_TAG := $(shell pkg-config --exists webkit2gtk-4.0 2>/dev/null && echo "" || echo "-tags webkit2_41")


# ── CLI ───────────────────────────────────────────────────────────────────────

.PHONY: build
build:
	go build -o oSyn ./cmd/oSyn/

# ── Desktop GUI ───────────────────────────────────────────────────────────────

.PHONY: gui
gui: ## Run the GUI in development mode (hot reload)
	cd $(GUI_DIR) && DATABASE_PATH=$(DB) $(WAILS) dev $(WEBKIT_TAG)

.PHONY: gui-build
gui-build: ## Build a production GUI binary → cmd/gui/build/bin/openSynapse-ui
	cd $(GUI_DIR) && $(WAILS) build -clean $(WEBKIT_TAG)

.PHONY: gui-run
gui-run: gui-build ## Build then launch the production GUI binary
	DATABASE_PATH=$(DB) $(GUI_BIN)

# ── Wails helpers ─────────────────────────────────────────────────────────────

.PHONY: gui-bindings
gui-bindings: ## Regenerate TypeScript bindings after changing app.go
	cd $(GUI_DIR) && $(WAILS) generate module $(WEBKIT_TAG)

.PHONY: gui-deps
gui-deps: ## Install frontend npm packages
	cd $(GUI_DIR)/frontend && npm install

# ── Misc ──────────────────────────────────────────────────────────────────────

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'
