---
name: plugin-dev
description: Create, update, validate, or package cdx plugins (manifest.json, main.js, Goja cdx host API, HTTP routes, Gin middleware, JSON Schema settings, and custom Web UI panels).
---

# cdx Plugin Development Skill

Use this skill whenever creating, editing, debugging, or packaging a plugin for `cdx`.

## 1. Reference Files
Before writing plugin code, always check:
- `PLUGINS.md` - complete plugin structure, `manifest.json` specification, and `cdx` host API reference.
- `internal/plugin/doc.go` - source-of-truth host API definitions (keep `internal/plugin/doc.go` and `PLUGINS.md` in sync if new host methods are added).
- Existing default plugins in `plugins/` (`providers`, `presets`, `permissions`, `context`, `tools-local`, `webui`).

## 2. Plugin Checklist
1. **Directory & Manifest (`manifest.json`)**:
   - Required fields: `id` (lowercase alphanumeric/dash/underscore), `name`, `version` (`MAJOR.MINOR.PATCH`), `description`, `entry` (`main.js`).
   - Optional fields: `min_cdx_version`, `order`, `author`, `homepage`.
2. **Entry Script (`main.js`)**:
   - Define `function setup(cdx) { ... }`.
   - Keep compatibility with Goja (ES5.1 / ES6 subset supported by Goja).
   - Store non-sensitive configuration in `cdx.storage` and API keys/tokens in `cdx.secrets` (encrypted with AES-256-GCM in `cdx.db`).
3. **Web UI**:
   - For simple settings, pass a JSON Schema to `cdx.ui.register({ title, description, order, schema })` and expose `GET/POST /api/plugins/<id>/config`.
   - For rich interactive views, mount static files via `cdx.http.static("/ui/<id>", "./web")`, pass `entry: "/ui/<id>/panel.js"` to `cdx.ui.register(...)`, and assign `window.CDXPluginPanels["<id>"] = function(container) { ... }`.
4. **Verification**:
   - Run `make test && make vet` to ensure the plugin loads cleanly and passes all unit/daemon checks.
