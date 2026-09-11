# bash-gate — build and install.
# Override the install root for verification in a scratchpad:
#   make install PREFIX=/tmp/cbr-test

PREFIX ?= $(HOME)/.claude-bash-rules

ENGINE := $(PREFIX)/engine
GLOBAL := $(PREFIX)/global

SRC := src
DIST := dist
BIN := $(DIST)/bash-gate

BINARY_NAME := bash-gate
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Stamp VERSION into the binary (main.version), readable via `bash-gate --version`.
# -s -w drop debug symbols (smaller file, harder to debug). Combined with
# -trimpath (in the release recipe) for reproducible, path-clean binaries.
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Platforms to cross-compile for. windows-amd64 is included to match the
# reference template, but note bash-gate is Unix-only in practice (gate.sh
# is bash and it relies on the nono sandbox) — drop it if you don't need it.
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

.PHONY: build release test install clean

build:
	mkdir -p "$(DIST)"
	cd "$(SRC)" && go build -ldflags="-X main.version=$(VERSION)" -o "../$(BIN)" .

# Cross-compile stripped, path-clean binaries for every platform in PLATFORMS.
release: clean
	@echo "Cross-compiling $(BINARY_NAME) $(VERSION)..."
	@$(foreach p,$(PLATFORMS), \
		os=$(word 1,$(subst -, ,$(p))); arch=$(word 2,$(subst -, ,$(p))); \
		out="$(DIST)/$(p)/$(BINARY_NAME)"; \
		[ "$$os" = windows ] && out="$$out.exe"; \
		mkdir -p "$(DIST)/$(p)"; \
		echo "  -> $$out"; \
		( cd "$(SRC)" && GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags="$(LDFLAGS)" -o "../$$out" . ) || exit 1; \
	)

test:
	cd "$(SRC)" && go test ./...

# Installs the binary + wrapper (read-only), and the starter global rules
# ONLY if none exist yet — never clobbers an operator's tuned rules.
install: build
	mkdir -p "$(ENGINE)" "$(GLOBAL)" "$(PREFIX)/logs" "$(PREFIX)/attestations"
	cp "$(BIN)" "$(ENGINE)/bash-gate"
	cp "$(SRC)/gate.sh" "$(ENGINE)/gate.sh"
	chmod 0755 "$(ENGINE)/bash-gate" "$(ENGINE)/gate.sh"
	if [ ! -f "$(GLOBAL)/rules" ]; then \
		cp conf/global-rules.starter "$(GLOBAL)/rules"; \
		echo "installed starter rules -> $(GLOBAL)/rules"; \
		CLAUDE_BASH_RULES_DIR="$(PREFIX)" "$(ENGINE)/bash-gate" attest --global; \
	else \
		echo "kept existing rules  -> $(GLOBAL)/rules"; \
		echo "(re-attest with: bash-gate attest --global, if you edited them)"; \
	fi
	@echo "engine installed     -> $(ENGINE)"
	@echo "register the hook (command: $(ENGINE)/gate.sh) in ~/.claude/settings.json"

clean:
	if [ -d "$(DIST)" ]; then rm -r "$(DIST)"; fi
