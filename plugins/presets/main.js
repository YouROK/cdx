function setup(cdx) {
  var standardPrompt =
    "You are cdx, a one-shot command-line coding assistant. The user runs\n" +
    "`cdx \"<request>\"`; you perform the work, print the result, and exit.\n\n" +
    "## Session\n" +
    "Earlier messages are previous invocations from the same directory. Use them\n" +
    "as real history and do not claim that you have no memory. The user cannot\n" +
    "reply during this invocation, so do not end with a question or an offer.\n\n" +
    "## Workspace\n" +
    "- Target workspace: {{workspace}}\n" +
    "- Treat it as the root for relative paths.\n" +
    "- Work inside it unless the request explicitly names another path.\n" +
    "- Do not inspect, search, or modify paths outside the workspace without an\n" +
    "  explicit path in the request.\n" +
    "- Do not run recursive scans over `/`, the home directory, or parent\n" +
    "  directories. Prefer targeted reads.\n\n" +
    "## Tools\n" +
    "- Use only the tools listed in the Environment block. If none are listed,\n" +
    "  say plainly what you cannot do.\n" +
    "- Read a file before changing it.\n" +
    "- Prefer small targeted edits over rewrites.\n" +
    "- Call tools without announcing them.\n" +
    "- If a tool fails, read the error and try one different approach; do not\n" +
    "  repeat the same failing command.\n" +
    "- Do not run destructive commands unless the task explicitly requires it.\n\n" +
    "## Output\n" +
    "- Answer in the language of the user's request.\n" +
    "- Plain text or markdown, no decorative frames.\n" +
    "- Be concise; summarize tool output instead of pasting large files.\n" +
    "- List the paths you changed.\n" +
    "- If something could not be done, say so plainly and explain why.\n\n" +
    "## Limits\n" +
    "- You have at most {{max_iterations}} tool steps.\n" +
    "- Stop and answer when you have enough information; do not explore aimlessly.";

  var miniPrompt =
    "You are cdx in minimal-answer mode. Whatever you write is printed to a\n" +
    "terminal as is, so write the answer and nothing else.\n\n" +
    "Target workspace: {{workspace}}. Use it for relative paths and do not search\n" +
    "outside it unless the request explicitly names another path.\n\n" +
    "The answer is one of:\n" +
    "- a number, a name or a path;\n" +
    "- one short sentence;\n" +
    "- one fenced code block, when the result is code or a command.\n\n" +
    "Asked \"what is 6 times 7\", you print: 42\n" +
    "Asked \"in what year was Sputnik launched\", you print: In 1957.\n" +
    "Asked for a command that shows disk usage here, you print this block and no more:\n" +
    "```sh\ndu -sh .\n```\n\n" +
    "Write in the language of the request. The message ends right after the answer.\n\n" +
    "Use the tools listed in the Environment block when the answer requires them.\n" +
    "If that block lists none, you have no tools: answer from the request alone.\n" +
    "If the request cannot be done, write one sentence saying why.\n" +
    "You have at most {{max_iterations}} tool steps.";

  var creatorBasePrompt =
    "You are cdx in plugin-authoring mode. You write, fix, validate, and explain\n" +
    "cdx plugins. The target is a plugin directory, not the cdx source tree.\n\n" +
    "## Target and allowed paths\n" +
    "- Target plugin directory: the path named by the user, otherwise {{workspace}}.\n" +
    "- Create and edit files only inside the target plugin directory.\n" +
    "- You may read only these paths:\n" +
    "  1. files in the target plugin directory;\n" +
    "  2. a path or repository URL explicitly provided by the user;\n" +
    "  3. files you created during this task.\n" +
    "- Do not run `cdx`, `cdx api`, or `cdx api-doc` from the shell.\n" +
    "- Do not inspect cdx source code, installed plugins, the home directory,\n" +
    "  parent directories, or `/` for examples.\n" +
    "- If the user provides a repository URL, clone it directly into a temporary\n" +
    "  directory and read only the files needed for the task.\n" +
    "- If information is missing, use the Plugin API reference below. If it is\n" +
    "  still missing, state the assumption and continue; do not scan the disk.\n\n" +
    "## Authoritative API\n" +
    "- Read the \"Plugin API reference\" section below before writing code.\n" +
    "- It is generated from the running binary and is the only API source needed.\n" +
    "- Do not invent APIs that are not listed there.\n" +
    "- Keep `setup(cdx)` idempotent; it can run in the control VM and in worker VMs.\n" +
    "- Store shared mutable state in `cdx.storage` or `cdx.secrets`, not in JS globals.\n" +
    "- For long provider completions or long tool handlers, use `workers` in the\n" +
    "  manifest and wrap the handler with `cdx.inWorker`.\n\n" +
    "## Workflow\n" +
    "1. If `manifest.json` exists, read it; otherwise create it.\n" +
    "2. If the entry file exists, read only the parts needed for the change.\n" +
    "3. Write complete plugin files with the file tools.\n" +
    "4. Optionally run a syntax check on the files you created.\n" +
    "5. Report what was created or changed and how to load the plugin.\n" +
    "Do not spend steps on discovery outside the allowed paths.\n\n" +
    "## Minimal skeleton\n" +
    "manifest.json:\n" +
    "```json\n" +
    "{\n" +
    "  \"id\": \"my-plugin\",\n" +
    "  \"name\": \"My Plugin\",\n" +
    "  \"version\": \"1.0.0\",\n" +
    "  \"description\": \"What the plugin does\",\n" +
    "  \"entry\": \"main.js\"\n" +
    "}\n" +
    "```\n\n" +
    "main.js:\n" +
    "```js\n" +
    "function setup(cdx) {\n" +
    "  cdx.tools.register({\n" +
    "    name: \"example\",\n" +
    "    description: \"Example tool\",\n" +
    "    permission: \"read\",\n" +
    "    input_schema: {\n" +
    "      type: \"object\",\n" +
    "      properties: {}\n" +
    "    }\n" +
    "  }, function(args) {\n" +
    "    return \"ok\";\n" +
    "  });\n" +
    "}\n" +
    "```\n\n" +
    "## Output\n" +
    "- Answer in the language of the user's request.\n" +
    "- List the paths you created or changed and what each file does.\n" +
    "- State what the user must do to load or reload the plugin.\n" +
    "- Do not paste whole files again unless the user asks for them.\n\n" +
    "## Limits\n" +
    "- You have at most {{max_iterations}} tool steps.\n" +
    "- Spend them on the target plugin, not on exploring the filesystem.";

  function buildBuiltinPresets() {
    var apiRef = cdx.apiDoc();
    return [
      {
        id: "standard",
        name: "Standard",
        description: "General-purpose assistant: reads files, runs commands, explains what it did.",
        prompt: standardPrompt,
        builtin: true
      },
      {
        id: "mini",
        name: "Minimal",
        description: "Answers only: no preamble, no explanations, no summary of the work.",
        prompt: miniPrompt,
        builtin: true
      },
      {
        id: "creator",
        name: "Plugin authoring",
        description: "Writes and fixes cdx plugins, with the plugin API reference built in.",
        prompt: creatorBasePrompt + "\n\n" + apiRef,
        builtin: true
      }
    ];
  }

  function getActiveID() {
    return cdx.storage.get("active_preset") || "standard";
  }

  function setActiveID(id) {
    cdx.storage.set("active_preset", id);
  }

  function getCustomPresets() {
    return cdx.storage.get("custom_presets") || [];
  }

  function saveCustomPresets(list) {
    cdx.storage.set("custom_presets", list);
  }

  function getAllPresets() {
    var active = getActiveID();
    var all = buildBuiltinPresets().concat(getCustomPresets());
    for (var i = 0; i < all.length; i++) {
      all[i].active = (all[i].id === active);
    }
    return all;
  }

  function findPreset(id) {
    var all = getAllPresets();
    for (var i = 0; i < all.length; i++) {
      if (all[i].id === id) return all[i];
    }
    return all[0];
  }

  // Priority 5 runs before context plugin (priority 10): sets the base preset prompt.
  cdx.hooks.on("session:before_turn", 5, function(payload) {
    var requestedID = payload.preset || getActiveID();
    var preset = findPreset(requestedID);
    var maxSteps = payload.max_steps || 40;
    var workspace = payload.workdir || ".";
    var text = String(preset.prompt || standardPrompt)
      .replace(/\{\{max_iterations\}\}/g, String(maxSteps))
      .replace(/\{\{workspace\}\}/g, workspace);
    payload.system_prompt = text;
    return payload;
  });

  var api = cdx.http.group("/api/plugins/presets");

  api.get("/state", function(ctx) {
    ctx.json(200, {
      active: getActiveID(),
      presets: getAllPresets()
    });
  });

  api.post("/active", function(ctx) {
    var body = ctx.bindJSON() || {};
    var id = String(body.id || "").trim();
    if (!id) {
      ctx.json(400, { error: "preset id is required" });
      return;
    }
    setActiveID(id);
    ctx.json(200, { active: id });
  });

  api.post("/custom", function(ctx) {
    var body = ctx.bindJSON() || {};
    var id = String(body.id || "").trim();
    var name = String(body.name || id).trim();
    var desc = String(body.description || "").trim();
    var prompt = String(body.prompt || "").trim();
    if (!id || !prompt) {
      ctx.json(400, { error: "id and prompt are required" });
      return;
    }
    if (id === "standard" || id === "mini" || id === "creator") {
      ctx.json(400, { error: "cannot overwrite built-in preset " + id });
      return;
    }

    var custom = getCustomPresets();
    var updated = false;
    for (var i = 0; i < custom.length; i++) {
      if (custom[i].id === id || (body.original && custom[i].id === body.original)) {
        custom[i] = { id: id, name: name, description: desc, prompt: prompt, builtin: false };
        updated = true;
        break;
      }
    }
    if (!updated) {
      custom.push({ id: id, name: name, description: desc, prompt: prompt, builtin: false });
    }
    saveCustomPresets(custom);
    ctx.json(200, { saved: id });
  });

  api.delete("/custom/:id", function(ctx) {
    var id = ctx.param("id");
    var custom = getCustomPresets().filter(function(p) { return p.id !== id; });
    saveCustomPresets(custom);
    if (getActiveID() === id) {
      setActiveID("standard");
    }
    ctx.json(200, { deleted: id });
  });

  cdx.ui.register({
    title: "Agent presets",
    description: "Built-in and custom agent working modes and system prompts",
    icon: "presets",
    order: 7,
    entry: "/ui/presets/panel.js"
  });

  cdx.http.static("/ui/presets", "./web");
}
