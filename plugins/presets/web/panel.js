// Self-contained UI module for Agent Presets, matching the original cdx PresetsPage layout.
window.CDXPluginPanels = window.CDXPluginPanels || {};
window.CDXPluginPanels["presets"] = function(container) {
  function closeModal() {
    var m = document.getElementById("cdx-modal-root");
    if (m) m.innerHTML = "";
  }

  function openModal(title, bodyHTML, onMount) {
    var root = document.getElementById("cdx-modal-root");
    if (!root) {
      root = document.createElement("div");
      root.id = "cdx-modal-root";
      document.body.appendChild(root);
    }
    root.innerHTML =
      '<div class="modal-backdrop">' +
      '  <div class="modal-box" style="max-width:680px">' +
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

  function renderTile(p) {
    var activeStyle = p.active ? ' style="border-color:var(--accent);border-width:2px;display:flex;flex-direction:column;gap:10px"' : ' style="display:flex;flex-direction:column;gap:10px"';
    var badge = p.active ? '<span class="badge badge-accent">active</span>' : '';
    var selectBtn = p.active
      ? '<button class="btn btn-light btn-xs" disabled>Use this preset</button>'
      : '<button class="btn btn-light btn-xs" data-select-preset="' + p.id + '">Use this preset</button>';
    var actions = selectBtn + ' <button class="btn btn-default btn-xs" data-view-preset="' + p.id + '">Show prompt</button>';

    if (!p.builtin) {
      actions +=
        ' <button class="btn btn-default btn-xs" data-edit-preset="' + p.id + '">Edit</button>' +
        ' <button class="btn btn-danger btn-xs" data-del-preset="' + p.id + '">Delete</button>';
    }

    return (
      '<div class="provider-card"' + activeStyle + '>' +
      '  <div>' +
      '    <div class="row-gap-xs">' +
      '      <h4 class="m-0">' + (p.name || p.id) + '</h4>' +
             badge +
      '    </div>' +
      '    <div class="mono text-xs muted mt-2">' + p.id + '</div>' +
      '  </div>' +
      '  <div class="text-sm" style="flex:1">' + (p.description || "-") + '</div>' +
      '  <div class="row-gap-xs">' + actions + '</div>' +
      '</div>'
    );
  }

  async function render() {
    var r = await fetch("/api/plugins/presets/state");
    var data = await r.json();
    var presets = data.presets || [];
    var builtin = presets.filter(function(p) { return p.builtin; });
    var custom = presets.filter(function(p) { return !p.builtin; });

    var html =
      '<div class="page-header">' +
      '  <h3>Agent presets</h3>' +
      '  <button class="btn btn-primary" id="btn-create-preset">Create preset</button>' +
      '</div>' +
      '<p class="page-intro">A preset changes how the agent behaves and answers. The list of tools and environment are collected automatically on every request.</p>' +
      '<div class="stack-md">' +
      '  <div>' +
      '    <div class="section-caption mb-xs">Built in</div>' +
      '    <div class="grid-3">' + builtin.map(renderTile).join("") + '</div>' +
      '  </div>';

    if (custom.length > 0) {
      html +=
        '  <div>' +
        '    <div class="section-caption mb-xs">Yours</div>' +
        '    <div class="grid-3">' + custom.map(renderTile).join("") + '</div>' +
        '  </div>';
    }
    html += '</div>';
    container.innerHTML = html;

    function openEditModal(existing) {
      var p = existing || { id: "", name: "", description: "", prompt: "" };
      openModal(existing ? "Edit preset: " + p.name : "New preset",
        '<form id="form-preset" class="form-stack">' +
        '  <div class="grid-2">' +
        '    <label>Name<span class="field-hint">Shown on the tile</span>' +
        '      <input name="name" placeholder="Review" value="' + (p.name || "") + '" required></label>' +
        '    <label>Identifier<span class="field-hint">Used by --preset</span>' +
        '      <input name="id" placeholder="review" value="' + (p.id || "") + '" ' + (existing ? 'readonly' : 'required') + '></label>' +
        '  </div>' +
        '  <label>Description<span class="field-hint">One line telling when to use this preset</span>' +
        '    <input name="description" value="' + (p.description || "") + '"></label>' +
        '  <label>Prompt<span class="field-hint">The explaining part of the system prompt</span>' +
        '    <textarea name="prompt" rows="12" class="mono text-xs" required>' + String(p.prompt || "").replace(/</g, "&lt;") + '</textarea></label>' +
        '  <div class="modal-actions">' +
        '    <button type="button" class="btn btn-default" id="modal-cancel">Cancel</button>' +
        '    <button type="submit" class="btn btn-primary">Save</button>' +
        '  </div>' +
        '</form>',
        function(box) {
          box.querySelector("#modal-cancel").onclick = closeModal;
          box.querySelector("#form-preset").onsubmit = async function(e) {
            e.preventDefault();
            var fd = new FormData(e.target);
            await fetch("/api/plugins/presets/custom", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                original: existing ? existing.id : "",
                id: fd.get("id"),
                name: fd.get("name"),
                description: fd.get("description"),
                prompt: fd.get("prompt")
              })
            });
            closeModal();
            window.cdxToast(true, "Preset saved");
            render();
          };
        }
      );
    }

    document.getElementById("btn-create-preset").onclick = function() {
      openEditModal(null);
    };

    container.querySelectorAll("[data-select-preset]").forEach(function(btn) {
      btn.onclick = async function() {
        var id = btn.getAttribute("data-select-preset");
        await fetch("/api/plugins/presets/active", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: id })
        });
        window.cdxToast(true, "Preset activated: " + id);
        render();
      };
    });

    container.querySelectorAll("[data-view-preset]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-view-preset");
        var p = presets.find(function(x) { return x.id === id; });
        if (!p) return;
        openModal("Prompt: " + (p.name || p.id),
          '<p class="field-hint mb-xs">' + (p.builtin ? "Built-in preset (read-only)" : "Custom preset") + '</p>' +
          '<pre class="code-block">' + String(p.prompt || "").replace(/</g, "&lt;") + '</pre>' +
          '<div class="modal-actions mt-md"><button class="btn btn-default" id="modal-cancel">Close</button></div>',
          function(box) {
            box.querySelector("#modal-cancel").onclick = closeModal;
          }
        );
      };
    });

    container.querySelectorAll("[data-edit-preset]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-edit-preset");
        var p = presets.find(function(x) { return x.id === id; });
        if (p) openEditModal(p);
      };
    });

    container.querySelectorAll("[data-del-preset]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-del-preset");
        window.cdxConfirm("Delete preset", "Delete custom preset '" + id + "'?", async function() {
          await fetch("/api/plugins/presets/custom/" + encodeURIComponent(id), { method: "DELETE" });
          window.cdxToast(true, "Preset deleted");
          render();
        });
      };
    });
  }

  render();
};
