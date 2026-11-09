# shizo — build and install.
# Override the install root for verification in a scratchpad:
#   make install PREFIX=/tmp/bg-test

PREFIX ?= $(HOME)/.shizo

ENGINE := $(PREFIX)/engine
GLOBAL := $(PREFIX)/global

SRC := src
DIST := dist
BIN := $(DIST)/shizo

BINARY_NAME := shizo
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Stamp VERSION into the binary (main.version), readable via `shizo --version`.
# -s -w drop debug symbols (smaller file, harder to debug). Combined with
# -trimpath (in the release recipe) for reproducible, path-clean binaries.
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Platforms to cross-compile for. windows-amd64 is included to match the
# reference template, but note shizo is Unix-only in practice (shizo.sh
# is bash and it relies on the nono sandbox) — drop it if you don't need it.
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

SSG_SRC := cmd/ssg

.PHONY: build release test install clean site

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
	cp "$(BIN)" "$(ENGINE)/shizo"
	cp "$(SRC)/shizo.sh" "$(ENGINE)/shizo.sh"
	chmod 0755 "$(ENGINE)/shizo" "$(ENGINE)/shizo.sh"
	cp conf/pi/shizo.ts "$(ENGINE)/shizo-pi.ts"
	chmod 0644 "$(ENGINE)/shizo-pi.ts"
	if [ ! -f "$(GLOBAL)/rules" ]; then \
		cp conf/global-rules.starter "$(GLOBAL)/rules"; \
		echo "installed starter rules -> $(GLOBAL)/rules"; \
		SHIZO_DIR="$(PREFIX)" "$(ENGINE)/shizo" attest --global; \
	else \
		echo "kept existing rules  -> $(GLOBAL)/rules"; \
		echo "(re-attest with: shizo attest --global, if you edited them)"; \
	fi
	@if [ -d "$(HOME)/.bash-gate" ]; then \
		echo "NOTE: old install dir ~/.bash-gate found — rename it to ~/.shizo"; \
	fi
	@if [ -d "$(HOME)/.claude-bash-rules" ]; then \
		echo "NOTE: old install dir ~/.claude-bash-rules found — rename it to ~/.shizo"; \
	fi
	@echo "engine installed     -> $(ENGINE)"
	@echo "register the hook (command: $(ENGINE)/shizo.sh) in ~/.claude/settings.json"

site:
	cd "$(SSG_SRC)" && go run . -root ../.. build

site-serve:
	cd "$(SSG_SRC)" && go run . -root ../.. serve

clean:
	if [ -d "$(DIST)" ]; then rm -r "$(DIST)"; fi
	if [ -d "public" ]; then rm -r "public"; fi
