BIN := bin/cdx
CACHE_DIR ?= $(CURDIR)/.cache
NPM_CACHE := $(CACHE_DIR)/npm

# Каталоги установки. DESTDIR нужен упаковщикам, при обычной установке пуст.
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
# Юнит ставится рядом с бинарником, если PREFIX переопределён, иначе в /etc.
UNIT_DIR ?= $(if $(filter /usr/local,$(PREFIX)),/etc/systemd/system,$(PREFIX)/lib/systemd/system)
DESTDIR ?=

# Права нужны только для записи в системные каталоги.
# SUDO_EXPLICIT фиксирует, что значение задано извне: пустое значение
# означает осознанную установку без повышения прав.
SUDO_EXPLICIT := $(if $(filter command line environment,$(origin SUDO)),1,)
ifeq ($(shell id -u 2>/dev/null),0)
SUDO :=
SUDO_ROOT := 1
else
SUDO ?= sudo
endif

# Кеш модулей внутри проекта: системный может быть только для чтения.
export GOMODCACHE := $(CACHE_DIR)/gomod
export GOCACHE := $(CACHE_DIR)/gobuild
export GOFLAGS := -mod=mod
export GOSUMDB := off

.DEFAULT_GOAL := all

.PHONY: all build bin web dev run test test-live vet fmt clean install uninstall doctor guard-root

# Полная пересборка с нуля: очистка, интерфейс, бинарник.
all: clean build

# Сборка под root запрещена: она оставит root-владельцев в рабочих каталогах.
guard-root:
	@if [ "$$(id -u)" = "0" ] && [ -z "$(ALLOW_ROOT_BUILD)" ]; then \
		echo "refusing to build as root: it would leave root-owned files in bin, .cache and node_modules"; \
		echo "build as your user, then run 'sudo make install'"; \
		echo "to override: make build ALLOW_ROOT_BUILD=1"; \
		exit 1; \
	fi

# Сборка интерфейса и бинарника со встроенной статикой.
build: guard-root web
	go build -o $(BIN) ./cmd/cdx

# Быстрая пересборка на уже собранном интерфейсе.
bin: guard-root
	go build -o $(BIN) ./cmd/cdx

# Сборка интерфейса кладёт статику в пакет для go:embed.
web: guard-root
	cd web && npm install --no-audit --no-fund --cache $(NPM_CACHE) && npm run build

# Разработка интерфейса: Vite проксирует /api на живой демон.
dev:
	cd web && npm run dev

run: build
	./$(BIN)

test: guard-root
	go test ./...
	cd web && npm test

# Живые проверки на реальных моделях требуют ключ.
test-live:
	@test -n "$(CDX_LIVE_API_KEY)" || \
		{ echo "CDX_LIVE_API_KEY is not set"; exit 1; }
	go test -tags live -v -timeout 580s ./test/live/

vet: guard-root
	go vet ./...
	go vet -tags live ./test/live/

fmt:
	gofmt -w cmd internal test

# Бинарник и статика удаляются, кеш зависимостей и node_modules остаются.
clean:
	rm -rf bin internal/webassets/dist

# Установка в систему.
#
# Сборка намеренно не запускается: под sudo она создала бы root-владельцев
# в bin, .cache и node_modules. Сначала собирают обычным пользователем.
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
	@if [ -w "$(DESTDIR)$(UNIT_DIR)" ] || [ "$$(id -u)" = "0" ]; then \
		$(SUDO) install -Dm644 contrib/cdx@.service $(DESTDIR)$(UNIT_DIR)/cdx@.service; \
		echo "service: $(DESTDIR)$(UNIT_DIR)/cdx@.service"; \
		echo "enable with: sudo systemctl enable --now cdx@$$USER"; \
	else \
		echo "skipped systemd unit: $(DESTDIR)$(UNIT_DIR) is not writable"; \
	fi
	@echo "installed $(DESTDIR)$(BINDIR)/cdx"

# Удаление из системы. Данные пользователя не трогаются.
uninstall:
	-@if [ -x "$(DESTDIR)$(BINDIR)/cdx" ]; then \
		"$(DESTDIR)$(BINDIR)/cdx" daemon stop >/dev/null 2>&1 || true; \
	fi
	-$(SUDO) systemctl disable --now cdx@$(shell whoami) >/dev/null 2>&1 || true
	$(SUDO) rm -f $(DESTDIR)$(BINDIR)/cdx
	-@if [ -f "$(DESTDIR)$(UNIT_DIR)/cdx@.service" ]; then \
		$(SUDO) rm -f $(DESTDIR)$(UNIT_DIR)/cdx@.service; \
	fi
	@echo "removed $(DESTDIR)$(BINDIR)/cdx"
	@echo "user data is kept in ~/.local/share/cdx and ~/.config/cdx"

# Проверка окружения установленным бинарником.
doctor:
	cdx doctor
