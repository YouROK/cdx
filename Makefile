BIN := bin/cdx
CACHE_DIR ?= $(CURDIR)/.cache

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
SHAREDIR ?= $(PREFIX)/share/cdx
PLUGINS_DIR ?= $(SHAREDIR)/plugins
UNIT_DIR ?= $(if $(filter /usr/local,$(PREFIX)),/etc/systemd/system,$(PREFIX)/lib/systemd/system)
DESTDIR ?=

SUDO_EXPLICIT := $(if $(filter command line environment,$(origin SUDO)),1,)
ifeq ($(shell id -u 2>/dev/null),0)
SUDO :=
SUDO_ROOT := 1
else
SUDO ?= sudo
endif

export GOMODCACHE := $(CACHE_DIR)/gomod
export GOCACHE := $(CACHE_DIR)/gobuild
export GOFLAGS := -mod=mod
export GOSUMDB := off

.DEFAULT_GOAL := all

.PHONY: all build bin run test test-live vet fmt clean install uninstall guard-root

all: clean build

guard-root:
	@if [ "$$(id -u)" = "0" ] && [ -z "$(ALLOW_ROOT_BUILD)" ]; then \
		echo "refusing to build as root: it would leave root-owned files in bin and .cache"; \
		echo "build as your user, then run 'sudo make install'"; \
		echo "to override: make build ALLOW_ROOT_BUILD=1"; \
		exit 1; \
	fi

build: bin

bin: guard-root
	@mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/cdx

run: build
	./$(BIN)

test: guard-root
	go test ./...

test-live: guard-root
	@test -n "$(CDX_LIVE_API_KEY)" || { echo "CDX_LIVE_API_KEY is not set"; exit 1; }
	go test -tags live -v -timeout 180s ./test/live/

vet: guard-root
	go vet ./...
	go vet -tags live ./test/live/

fmt:
	gofmt -w cmd internal test

clean:
	rm -rf bin dist

install:
	@test -x "$(BIN)" || { \
		echo "$(BIN) is missing: run 'make' first, without sudo"; \
		exit 1; \
	}
	@if [ "$$(id -u)" != "0" ] && [ -z "$(SUDO)" ] && [ -z "$(SUDO_EXPLICIT)" ]; then \
		echo "no privilege escalation available: run with sudo or set SUDO="; \
		exit 1; \
	fi
	$(SUDO) install -Dm755 $(BIN) $(DESTDIR)$(BINDIR)/cdx
	$(SUDO) install -d -m755 $(DESTDIR)$(PLUGINS_DIR)
	@if [ -d plugins ] && [ -n "$$(ls -A plugins 2>/dev/null)" ]; then \
		$(SUDO) cp -r plugins/* $(DESTDIR)$(PLUGINS_DIR)/; \
		echo "installed plugins to $(DESTDIR)$(PLUGINS_DIR)"; \
	fi
	@echo "installed $(DESTDIR)$(BINDIR)/cdx"

uninstall:
	$(SUDO) rm -f $(DESTDIR)$(BINDIR)/cdx
	$(SUDO) rm -rf $(DESTDIR)$(SHAREDIR)
	@echo "removed $(DESTDIR)$(BINDIR)/cdx and $(DESTDIR)$(SHAREDIR)"
	@echo "user data is kept in ~/.local/share/cdx and ~/.config/cdx"
