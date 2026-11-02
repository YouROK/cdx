// Self-contained UI module for the Models & Providers section, matching the original cdx ModelsPage layout.
window.CDXPluginPanels = window.CDXPluginPanels || {};
window.CDXPluginPanels["providers"] = function(container) {
  function esc(v) {
    return String(v == null ? "" : v)
      .replace(/&/g, "&amp;")
      .replace(/"/g, "&quot;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  function fmtNum(n) {
    if (!n) return "not set";
    return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, " ");
  }

  async function loadState() {
    var res = await fetch("/api/plugins/providers/state");
    return await res.json();
  }

  function showToast(ok, msg) {
    if (window.cdxToast) {
      window.cdxToast(ok, msg);
    }
  }

  function closeModal() {
    var m = document.getElementById("cdx-modal-root");
    if (m) m.innerHTML = "";
  }

  function openModal(title, bodyHTML, onMount, boxClass) {
    var root = document.getElementById("cdx-modal-root");
    if (!root) {
      root = document.createElement("div");
      root.id = "cdx-modal-root";
      document.body.appendChild(root);
    }
    root.innerHTML =
      '<div class="modal-backdrop">' +
      '  <div class="modal-box' + (boxClass ? " " + boxClass : "") + '">' +
      '    <div class="modal-header">' +
      '      <h4>' + title + '</h4>' +
      '      <button class="btn-icon" id="modal-close-x">&times;</button>' +
      '    </div>' +
      '    <div class="modal-body">' + bodyHTML + '</div>' +
      '  </div>' +
      '</div>';
    root.querySelector("#modal-close-x").onclick = closeModal;
    root.querySelector(".modal-backdrop").onclick = function(e) {
      if (e.target.classList.contains("modal-backdrop")) closeModal();
    };
    if (onMount) onMount(root.querySelector(".modal-body"));
  }

  async function render() {
    var state = await loadState();
    var providers = state.providers || [];
    var models = state.models || [];

    var html =
      '<div class="page-header">' +
      '  <h3>Models</h3>' +
      '  <button class="btn btn-primary" id="btn-add-provider">Add provider</button>' +
      '</div>' +
      '<p class="page-intro">A provider holds the connection: address, protocol and key. Models belong to a provider and differ only in their own settings.</p>';

    if (providers.length === 0) {
      html +=
        '<div class="panel empty-panel">' +
        '  <p class="muted">No providers configured yet.</p>' +
        '  <button class="btn btn-primary" id="btn-add-provider-empty">Add provider</button>' +
        '</div>';
    } else {
      html += '<div class="stack-md">';
      providers.forEach(function(p) {
        var pModels = models.filter(function(m) { return m.provider === p.name; });
        var keyBadge = p.has_api_key
          ? '<span class="badge badge-teal">key set</span>'
          : '<span class="badge badge-warn">no key</span>';

        html +=
          '<div class="provider-card">' +
          '  <div class="row-between mb-md">' +
          '    <div>' +
          '      <div class="row-gap-xs">' +
          '        <h4 class="m-0">' + esc(p.name) + '</h4>' +
                   keyBadge +
          '        <span class="badge badge-outline">' + esc(p.protocol || "openai-chat") + '</span>' +
          '      </div>' +
          '      <div class="mono text-xs muted mt-2">' + esc(p.base_url || "-") + '</div>' +
          '    </div>' +
          '    <div class="row-gap-xs">' +
          '      <button class="btn btn-light btn-xs" data-test-prov="' + esc(p.name) + '">Test</button>' +
          '      <button class="btn btn-light btn-xs" data-disc-prov="' + esc(p.name) + '">Get models</button>' +
          '      <button class="btn btn-default btn-xs" data-key-prov="' + esc(p.name) + '">Key</button>' +
          '      <button class="btn btn-default btn-xs" data-edit-prov="' + esc(p.name) + '">Edit</button>' +
          '      <button class="btn btn-danger btn-xs" data-del-prov="' + esc(p.name) + '">Delete</button>' +
          '    </div>' +
          '  </div>' +
          '  <div class="row-between mb-xs">' +
          '    <span class="section-caption">MODELS: ' + pModels.length + '</span>' +
          '    <button class="btn btn-subtle btn-xs" data-add-model="' + esc(p.name) + '">+ Add model</button>' +
          '  </div>';

        if (pModels.length === 0) {
          html += '<div class="muted text-sm">This provider has no models yet.</div>';
        } else {
          html += '<div class="stack-xs">';
          pModels.forEach(function(m) {
            html +=
              '<div class="model-row">' +
              '  <div class="row-between">' +
              '    <div>' +
              '      <div class="row-gap-xs">' +
              '        <span class="fw-600">' + esc(m.name) + '</span>' +
                       (m.default ? '<span class="badge badge-accent">default</span>' : '') +
              '      </div>' +
              '      <div class="mono text-xs muted">' + esc(m.remote || m.name) + '</div>' +
              '      <div class="row-gap-md text-xs muted mt-2">' +
              '        <span>Context: ' + fmtNum(m.context_window) + '</span>' +
              '        <span>Max output: ' + fmtNum(m.max_tokens) + '</span>' +
              '        <span>Temperature: ' + (m.temperature !== undefined ? m.temperature : 0.7) + '</span>' +
              '      </div>' +
              '    </div>' +
              '    <div class="row-gap-xs">' +
              '      <button class="btn btn-light btn-xs" data-def-model-prov="' + esc(p.name) + '" data-def-model-name="' + esc(m.name) + '"' + (m.default ? ' disabled' : '') + '>Default</button>' +
              '      <button class="btn btn-default btn-xs" data-edit-model-prov="' + esc(p.name) + '" data-edit-model-name="' + esc(m.name) + '">Edit</button>' +
              '      <button class="btn btn-danger btn-xs" data-del-model-prov="' + esc(p.name) + '" data-del-model-name="' + esc(m.name) + '">Delete</button>' +
              '    </div>' +
              '  </div>' +
              '</div>';
          });
          html += '</div>';
        }
        html += '</div>';
      });
      html += '</div>';
    }

    container.innerHTML = html;

    function openProviderModal(existing) {
      var p = existing || { name: "", protocol: "openai-chat", base_url: "" };
      openModal(existing ? "Edit provider: " + p.name : "New provider",
        '<form id="modal-prov-form" class="form-stack">' +
        '  <label>Name<span class="field-hint">Unique provider connection name</span>' +
        '    <input name="name" value="' + p.name + '" ' + (existing ? 'readonly' : 'required') + '></label>' +
        '  <label>Protocol' +
        '    <select name="protocol">' +
        '      <option value="openai-chat"' + (p.protocol !== "anthropic" ? " selected" : "") + '>openai-chat</option>' +
        '      <option value="anthropic"' + (p.protocol === "anthropic" ? " selected" : "") + '>anthropic</option>' +
        '    </select></label>' +
        '  <label>Base URL<span class="field-hint">API endpoint address, e.g. https://api.openai.com/v1</span>' +
        '    <input name="base_url" placeholder="https://api.openai.com/v1" value="' + (p.base_url || "") + '"></label>' +
        '  <label>API Key<span class="field-hint">' + (existing ? 'Leave blank to keep existing encrypted key' : 'Stored encrypted in BoltDB secrets') + '</span>' +
        '    <input type="password" name="api_key" placeholder="sk-..."></label>' +
        '  <div class="modal-actions">' +
        '    <button type="button" class="btn btn-default" id="modal-cancel">Cancel</button>' +
        '    <button type="submit" class="btn btn-primary">Save</button>' +
        '  </div>' +
        '</form>',
        function(box) {
          box.querySelector("#modal-cancel").onclick = closeModal;
          box.querySelector("#modal-prov-form").onsubmit = async function(e) {
            e.preventDefault();
            var fd = new FormData(e.target);
            await fetch("/api/plugins/providers/providers", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                name: fd.get("name"),
                protocol: fd.get("protocol"),
                base_url: fd.get("base_url"),
                api_key: fd.get("api_key")
              })
            });
            closeModal();
            showToast(true, "Provider saved");
            render();
          };
        }
      );
    }

    function openKeyModal(provName) {
      openModal("Key: " + provName,
        '<form id="modal-key-form" class="form-stack">' +
        '  <label>API Key<span class="field-hint">Encrypted with AES-256-GCM inside cdx.db</span>' +
        '    <input type="password" name="api_key" placeholder="Enter new key..."></label>' +
        '  <div class="modal-actions">' +
        '    <button type="button" class="btn btn-default" id="modal-cancel">Cancel</button>' +
        '    <button type="submit" class="btn btn-primary">Save</button>' +
        '  </div>' +
        '</form>',
        function(box) {
          box.querySelector("#modal-cancel").onclick = closeModal;
          box.querySelector("#modal-key-form").onsubmit = async function(e) {
            e.preventDefault();
            var fd = new FormData(e.target);
            await fetch("/api/plugins/providers/providers/key", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name: provName, api_key: fd.get("api_key") })
            });
            closeModal();
            showToast(true, "API key saved");
            render();
          };
        }
      );
    }

    function openModelModal(provName, existing) {
      var m = existing || {
        provider: provName,
        name: "",
        remote: "",
        context_window: 128000,
        max_tokens: 4096,
        temperature: 1,
        default: models.length === 0,
        reasoning_levels: [],
        reasoning_default: "",
        supports_reasoning_effort: false,
        skip_temperature: false
      };
      var tempVal = m.temperature !== undefined ? Number(m.temperature) : 1;
      var levelsStr = (m.reasoning_levels || []).join(",");
      var ctxVal = m.context_window || 128000;
      var maxOutVal = m.max_tokens || 4096;

      openModal(existing ? "Edit model: " + m.name : "New model: " + provName,
        '<form id="modal-model-form" class="form-stack">' +
        '  <div class="grid-2">' +
        '    <label>Name<span class="field-hint">Unique across all providers, used by --model and in history.</span>' +
        '      <input name="name" value="' + m.name + '" required></label>' +
        '    <label>Remote model id<span class="field-hint">The identifier the provider expects, e.g. deepseek-ai/DeepSeek-V4-Flash.</span>' +
        '      <input name="remote" placeholder="deepseek-ai/DeepSeek-V4-Flash" value="' + (m.remote || "") + '"></label>' +
        '  </div>' +
        '  <div class="grid-3">' +
        '    <label>Context<span class="field-hint">Uses the provider value when available, otherwise 128000.</span>' +
        '      <input type="text" name="context_window" placeholder="128000" value="' + fmtNum(ctxVal) + '"></label>' +
        '    <label>Max output<span class="field-hint">Uses the provider value when available, otherwise 4096.</span>' +
        '      <input type="text" name="max_tokens" placeholder="4096" value="' + fmtNum(maxOutVal) + '"></label>' +
        '    <label><div>Temperature <span class="muted" id="temp-val-label">' + tempVal.toFixed(2) + '</span></div>' +
        '      <div class="slider-wrap">' +
        '        <input type="range" name="temperature" id="temp-slider" min="0" max="2" step="0.05" value="' + tempVal + '">' +
        '        <div class="slider-marks"><span>0</span><span>1</span><span>2</span></div>' +
        '      </div>' +
        '      <span class="field-hint">Lower is more predictable, higher is more creative. Providers usually accept 0 to 2.</span>' +
        '    </label>' +
        '  </div>' +
        '  <label>Reasoning levels<span class="field-hint">Comma separated, in order from weak to strong.</span>' +
        '    <input name="reasoning_levels" id="reasoning-levels-input" placeholder="low,medium,high,max" value="' + levelsStr + '"></label>' +
        '  <label>Default reasoning level' +
        '    <select name="reasoning_default" id="reasoning-default-select"></select></label>' +
        '  <div class="switch-grid">' +
        '    <label class="switch-card"><input type="checkbox" name="default"' + (m.default ? ' checked' : '') + '>' +
        '      <div><div>Use by default</div><div class="field-hint">One model is the default for the whole program.</div></div></label>' +
        '    <label class="switch-card"><input type="checkbox" name="supports_reasoning_effort"' + (m.supports_reasoning_effort ? ' checked' : '') + '>' +
        '      <div><div>Send reasoning_effort</div><div class="field-hint">Only when the provider accepts the parameter.</div></div></label>' +
        '    <label class="switch-card"><input type="checkbox" name="skip_temperature"' + (m.skip_temperature ? ' checked' : '') + '>' +
        '      <div><div>Don\'t send temperature</div><div class="field-hint">Needed when the provider rejects the parameter.</div></div></label>' +
        '  </div>' +
        '  <div class="modal-actions">' +
        '    <button type="button" class="btn btn-default" id="modal-cancel">Cancel</button>' +
        '    <button type="submit" class="btn btn-primary">Save</button>' +
        '  </div>' +
        '</form>',
        function(box) {
          box.querySelector("#modal-cancel").onclick = closeModal;
          var slider = box.querySelector("#temp-slider");
          var tempLbl = box.querySelector("#temp-val-label");
          slider.oninput = function() {
            tempLbl.textContent = Number(slider.value).toFixed(2);
          };

          var lvlInput = box.querySelector("#reasoning-levels-input");
          var defSelect = box.querySelector("#reasoning-default-select");
          function syncReasoningSelect(selectedVal) {
            var parts = String(lvlInput.value || "").split(/[,\n]/).map(function(s) { return s.trim(); }).filter(Boolean);
            var opts = '<option value="">(none)</option>';
            parts.forEach(function(lv) {
              opts += '<option value="' + lv + '"' + (lv === selectedVal ? ' selected' : '') + '>' + lv + '</option>';
            });
            defSelect.innerHTML = opts;
          }
          syncReasoningSelect(m.reasoning_default || "");
          lvlInput.oninput = function() {
            syncReasoningSelect(defSelect.value);
          };

          box.querySelector("#modal-model-form").onsubmit = async function(e) {
            e.preventDefault();
            var fd = new FormData(e.target);
            var cwDigits = String(fd.get("context_window") || "").replace(/[^\d]/g, "");
            var mtDigits = String(fd.get("max_tokens") || "").replace(/[^\d]/g, "");
            var rLevels = String(fd.get("reasoning_levels") || "").split(/[,\n]/).map(function(s) { return s.trim(); }).filter(Boolean);
            await fetch("/api/plugins/providers/models", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                provider: provName,
                name: fd.get("name"),
                remote: fd.get("remote") || fd.get("name"),
                context_window: cwDigits ? Number(cwDigits) : 0,
                max_tokens: mtDigits ? Number(mtDigits) : 0,
                temperature: Number(fd.get("temperature")),
                reasoning_levels: rLevels,
                reasoning_default: fd.get("reasoning_default") || "",
                supports_reasoning_effort: fd.get("supports_reasoning_effort") === "on",
                skip_temperature: fd.get("skip_temperature") === "on",
                default: fd.get("default") === "on"
              })
            });
            closeModal();
            showToast(true, "Model saved");
            render();
          };
        },
        "modal-wide"
      );
    }

    var addBtn = container.querySelector("#btn-add-provider");
    if (addBtn) addBtn.onclick = function() { openProviderModal(null); };
    var addEmptyBtn = container.querySelector("#btn-add-provider-empty");
    if (addEmptyBtn) addEmptyBtn.onclick = function() { openProviderModal(null); };

    container.querySelectorAll("[data-edit-prov]").forEach(function(btn) {
      btn.onclick = function() {
        var name = btn.getAttribute("data-edit-prov");
        var found = providers.find(function(x) { return x.name === name; });
        if (found) openProviderModal(found);
      };
    });

    container.querySelectorAll("[data-key-prov]").forEach(function(btn) {
      btn.onclick = function() { openKeyModal(btn.getAttribute("data-key-prov")); };
    });

    container.querySelectorAll("[data-test-prov]").forEach(function(btn) {
      btn.onclick = async function() {
        var name = btn.getAttribute("data-test-prov");
        btn.disabled = true;
        var r = await fetch("/api/plugins/providers/providers/test", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: name })
        });
        var d = await r.json();
        btn.disabled = false;
        showToast(Boolean(d.ok), d.message || "Test finished");
      };
    });

    container.querySelectorAll("[data-disc-prov]").forEach(function(btn) {
      btn.onclick = async function() {
        var name = btn.getAttribute("data-disc-prov");
        btn.disabled = true;
        var r = await fetch("/api/plugins/providers/providers/discover", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: name })
        });
        var d = await r.json();
        btn.disabled = false;
        if (!r.ok || d.error) {
          showToast(false, d.error || "Failed to fetch models");
          return;
        }
        var list = (d.models || []).map(function(item) {
          if (typeof item === "string") {
            return { id: item, context_window: 128000, max_tokens: 4096 };
          }
          return {
            id: String(item.id || ""),
            context_window: Number(item.context_window) || 128000,
            max_tokens: Number(item.max_tokens) || 4096
          };
        }).filter(function(item) { return item.id !== ""; });
        var metaByID = {};
        list.forEach(function(item) { metaByID[item.id] = item; });

        var listHTML = list.length === 0
          ? '<p class="muted">The provider returned no models.</p>'
          : '<div class="discover-controls">' +
              '<button type="button" class="btn btn-light btn-xs" id="modal-select-all">Select all</button>' +
              '<button type="button" class="btn btn-light btn-xs" id="modal-select-none">Clear all</button>' +
              '<span class="text-xs muted" id="modal-selected-count"></span>' +
            '</div>' +
            '<div class="discover-list">' + list.map(function(item) {
              return '<label class="checkbox-row discover-item"><input type="checkbox" value="' + esc(item.id) + '" checked> <span class="mono text-sm">' + esc(item.id) + '</span></label>';
            }).join("") + '</div>';

        openModal("Get models: " + name,
          listHTML +
          '<div class="modal-actions mt-md">' +
          '  <button type="button" class="btn btn-default" id="modal-cancel">Cancel</button>' +
          '  <button type="button" class="btn btn-primary" id="modal-add-disc">Add selected</button>' +
          '</div>',
          function(box) {
            box.querySelector("#modal-cancel").onclick = closeModal;
            var checks = box.querySelectorAll("input[type=checkbox]");
            var countEl = box.querySelector("#modal-selected-count");
            function updateCount() {
              var selected = box.querySelectorAll("input[type=checkbox]:checked").length;
              if (countEl) countEl.textContent = selected + " of " + checks.length + " selected";
            }
            var selectAll = box.querySelector("#modal-select-all");
            if (selectAll) {
              selectAll.onclick = function() {
                for (var i = 0; i < checks.length; i++) checks[i].checked = true;
                updateCount();
              };
            }
            var selectNone = box.querySelector("#modal-select-none");
            if (selectNone) {
              selectNone.onclick = function() {
                for (var i = 0; i < checks.length; i++) checks[i].checked = false;
                updateCount();
              };
            }
            for (var i = 0; i < checks.length; i++) {
              checks[i].onchange = updateCount;
            }
            updateCount();

            var addSel = box.querySelector("#modal-add-disc");
            if (addSel) {
              addSel.onclick = async function() {
                var checked = box.querySelectorAll("input[type=checkbox]:checked");
                for (var i = 0; i < checked.length; i++) {
                  var rem = checked[i].value;
                  var local = rem.replace(/[^a-zA-Z0-9._-]+/g, "-");
                  var meta = metaByID[rem] || { context_window: 128000, max_tokens: 4096 };
                  await fetch("/api/plugins/providers/models", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({
                      provider: name,
                      name: local,
                      remote: rem,
                      context_window: meta.context_window,
                      max_tokens: meta.max_tokens,
                      temperature: 0.7
                    })
                  });
                }
                closeModal();
                showToast(true, "Models added: " + checked.length);
                render();
              };
            }
          }
        );
      };
    });

    container.querySelectorAll("[data-del-prov]").forEach(function(btn) {
      btn.onclick = function() {
        var name = btn.getAttribute("data-del-prov");
        window.cdxConfirm("Delete provider", "Delete provider '" + name + "' and its models?", async function() {
          var r = await fetch("/api/plugins/providers/providers?name=" + encodeURIComponent(name), { method: "DELETE" });
          var d = await r.json();
          if (!r.ok || d.error) {
            showToast(false, d.error || "Failed to delete provider");
            return;
          }
          showToast(true, "Provider deleted");
          render();
        });
      };
    });

    container.querySelectorAll("[data-add-model]").forEach(function(btn) {
      btn.onclick = function() { openModelModal(btn.getAttribute("data-add-model"), null); };
    });

    container.querySelectorAll("[data-edit-model-prov]").forEach(function(btn) {
      btn.onclick = function() {
        var provider = btn.getAttribute("data-edit-model-prov");
        var name = btn.getAttribute("data-edit-model-name");
        var found = models.find(function(x) { return x.provider === provider && x.name === name; });
        if (found) openModelModal(found.provider, found);
      };
    });

    container.querySelectorAll("[data-def-model-prov]").forEach(function(btn) {
      btn.onclick = async function() {
        var provider = btn.getAttribute("data-def-model-prov");
        var name = btn.getAttribute("data-def-model-name");
        var r = await fetch("/api/plugins/providers/models/default", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ provider: provider, name: name })
        });
        var d = await r.json();
        if (!r.ok || d.error) {
          showToast(false, d.error || "Failed to update default model");
          return;
        }
        showToast(true, "Default model updated");
        render();
      };
    });

    container.querySelectorAll("[data-del-model-prov]").forEach(function(btn) {
      btn.onclick = function() {
        var provider = btn.getAttribute("data-del-model-prov");
        var name = btn.getAttribute("data-del-model-name");
        window.cdxConfirm("Delete model", "Delete model '" + provider + "/" + name + "'?", async function() {
          var r = await fetch("/api/plugins/providers/models?provider=" + encodeURIComponent(provider) + "&name=" + encodeURIComponent(name), { method: "DELETE" });
          var d = await r.json();
          if (!r.ok || d.error) {
            showToast(false, d.error || "Failed to delete model");
            return;
          }
          showToast(true, "Model deleted");
          render();
        });
      };
    });
  }

  render();
};
