SHELL := /usr/bin/env bash

WORKTREES_DIR ?= worktrees
BASE ?= HEAD
TARGET ?= ramdhan/main
OPEN ?= 1
FORCE ?= 0
CLEAN_LEFTOVERS ?= 0
DELETE_BRANCH ?= 0

.PHONY: wt-new wt-close wt-list wt-help

wt-help:
	@echo "Worktree helpers"
	@echo ""
	@echo "Create new worktree:"
	@echo "  make wt-new BRANCH=examples/voice_agents/basic_agent_webui BASE=ramdhan/main"
	@echo ""
	@echo "Close/rebase/merge worktree:"
	@echo "  make wt-close BRANCH=examples/voice_agents/basic_agent_webui TARGET=ramdhan/main"
	@echo ""
	@echo "Options:"
	@echo "  OPEN=0              Do not open VS Code after creating worktree"
	@echo "  FORCE=1             Force remove dirty worktree on close"
	@echo "  CLEAN_LEFTOVERS=1   rm -rf leftover worktree directory after git worktree remove"
	@echo "  DELETE_BRANCH=1     Delete source branch after successful merge"

wt-list:
	git worktree list

wt-new:
	@if [ -z "$(BRANCH)" ]; then echo "BRANCH is required"; exit 1; fi
	@set -euo pipefail; \
	branch="$(BRANCH)"; \
	base="$(BASE)"; \
	path="$(WORKTREES_DIR)/$$branch"; \
	echo "==> Creating worktree $$path from $$base"; \
	mkdir -p "$$(dirname "$$path")"; \
	if git show-ref --verify --quiet "refs/heads/$$branch"; then \
		git worktree add "$$path" "$$branch"; \
	else \
		git worktree add -b "$$branch" "$$path" "$$base"; \
	fi; \
	echo "==> Preparing local temp and shared reference directories"; \
	cd "$$path"; \
	if [ -e .tmp ] && [ ! -L .tmp ]; then echo "Refusing to replace non-symlink .tmp"; exit 1; fi; \
	if [ -e refs ] && [ ! -L refs ]; then echo "Refusing to replace non-symlink refs"; exit 1; fi; \
	rm -f .tmp refs; \
	mkdir -p .tmp .tmp/gotmp .tmp/gocache; \
	ln -s "$$(realpath --relative-to="$$PWD" "$$(git -C ../.. rev-parse --show-toplevel)/refs")" refs; \
	exclude_file="$$(git rev-parse --git-path info/exclude)"; \
	mkdir -p "$$(dirname "$$exclude_file")"; \
	printf ".tmp\nrefs\n" >> "$$exclude_file"; \
	echo "==> Worktree ready: $$PWD"; \
	git status --short; \
	if [ "$(OPEN)" = "1" ]; then \
		echo "==> Opening VS Code"; \
		code -n . >/dev/null 2>&1 & \
	fi

wt-close:
	@if [ -z "$(BRANCH)" ]; then echo "BRANCH is required"; exit 1; fi
	@set -euo pipefail; \
	branch="$(BRANCH)"; \
	target="$(TARGET)"; \
	path="$(WORKTREES_DIR)/$$branch"; \
	root="$$(git rev-parse --show-toplevel)"; \
	if [ ! -d "$$path" ]; then echo "Worktree path not found: $$path"; exit 1; fi; \
	echo "==> Checking worktree status"; \
	if [ "$(FORCE)" != "1" ] && [ -n "$$(git -C "$$path" status --porcelain)" ]; then \
		echo "Worktree has uncommitted changes. Commit/stash them, or rerun with FORCE=1."; \
		git -C "$$path" status --short; \
		exit 1; \
	fi; \
	echo "==> Fetching latest refs"; \
	git fetch --all --prune; \
	echo "==> Rebasing $$branch onto $$target"; \
	git -C "$$path" checkout "$$branch"; \
	git -C "$$path" rebase "$$target"; \
	echo "==> Fast-forward merging $$branch into $$target"; \
	current="$$(git -C "$$root" branch --show-current)"; \
	if [ "$$current" != "$$target" ]; then \
		git -C "$$root" checkout "$$target"; \
	fi; \
	git -C "$$root" merge --ff-only "$$branch"; \
	echo "==> Removing worktree"; \
	if [ "$(FORCE)" = "1" ]; then \
		git worktree remove --force "$$path"; \
	else \
		git worktree remove "$$path"; \
	fi; \
	if [ "$(CLEAN_LEFTOVERS)" = "1" ] && [ -e "$$path" ]; then \
		echo "==> Removing leftover path $$path"; \
		rm -rf "$$path"; \
	fi; \
	if [ "$(DELETE_BRANCH)" = "1" ]; then \
		echo "==> Deleting branch $$branch"; \
		git branch -d "$$branch"; \
	fi; \
	echo "==> Done"
