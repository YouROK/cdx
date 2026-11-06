function setup(cdx) {
  var DEFAULT_CONTEXT_WINDOW = 128000;
  var DEFAULT_MAX_TOKENS = 4096;

  function getProviders() {
    return cdx.storage.get("providers") || [];
  }

  function saveProviders(list) {
    cdx.storage.set("providers", list);
  }

  function getModels() {
    return cdx.storage.get("models") || [];
  }

  function saveModels(list) {
    cdx.storage.set("models", list);
  }

  function positiveInt(v) {
    var n = Number(v);
    if (isFinite(n) && n > 0) return Math.floor(n);
    return 0;
  }

  function firstPositiveInt(values) {
    for (var i = 0; i < values.length; i++) {
      var n = positiveInt(values[i]);
      if (n > 0) return n;
    }
    return 0;
  }

  // Read context and output limits from the common model metadata shapes.
  function discoverModelMeta(item) {
    var top = item.top_provider || {};
    var limits = item.limits || {};
    var meta = item.meta || {};
    return {
      id: String(item.id || ""),
      context_window: firstPositiveInt([
        item.context_length,
        item.context_window,
        item.max_context_length,
        item.max_input_tokens,
        top.context_length,
        limits.context_window,
        meta.n_ctx_train
      ]) || DEFAULT_CONTEXT_WINDOW,
      max_tokens: firstPositiveInt([
        item.max_completion_tokens,
        item.max_output_tokens,
        item.max_tokens,
        top.max_completion_tokens,
        limits.completion_tokens
      ]) || DEFAULT_MAX_TOKENS
    };
  }

  function withModelDefaults(m) {
    var out = {};
    for (var k in m) {
      if (Object.prototype.hasOwnProperty.call(m, k)) out[k] = m[k];
    }
    out.context_window = positiveInt(out.context_window) || DEFAULT_CONTEXT_WINDOW;
    out.max_tokens = positiveInt(out.max_tokens) || DEFAULT_MAX_TOKENS;
    return out;
  }

  function secretKeyFor(providerName) {
    return "provider_key:" + providerName;
  }

  function findProvider(name) {
    var list = getProviders();
    for (var i = 0; i < list.length; i++) {
      if (list[i].name === name) return list[i];
    }
    return null;
  }

  function findModel(idOrName) {
    var list = getModels();
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === idOrName || list[i].name === idOrName) return list[i];
    }
    return null;
  }

  // Convert internal cdx messages into OpenAI Chat Completions format.
  function toOpenAIMessages(systemPrompt, messages) {
    var out = [];
    if (systemPrompt) {
      out.push({ role: "system", content: systemPrompt });
    }
    for (var i = 0; i < messages.length; i++) {
      var m = messages[i];
      if (m.role === "assistant" && m.tool_calls && m.tool_calls.length > 0) {
        var tcs = [];
        for (var j = 0; j < m.tool_calls.length; j++) {
          var tc = m.tool_calls[j];
          tcs.push({
            id: tc.id || ("call_" + j),
            type: "function",
            function: {
              name: tc.name,
              arguments: JSON.stringify(tc.arguments || {})
            }
          });
        }
        out.push({
          role: "assistant",
          content: m.content || null,
          tool_calls: tcs
        });
      } else if (m.role === "tool") {
        out.push({
          role: "tool",
          tool_call_id: m.tool_call_id || "call_0",
          content: String(m.content || "")
        });
      } else {
        out.push({
          role: m.role || "user",
          content: String(m.content || "")
        });
      }
    }
    return out;
  }

  function toOpenAITools(tools) {
    if (!tools || tools.length === 0) return undefined;
    var out = [];
    for (var i = 0; i < tools.length; i++) {
      var t = tools[i];
      out.push({
        type: "function",
        function: {
          name: t.name,
          description: t.description || "",
          parameters: t.input_schema || { type: "object", properties: {} }
        }
      });
    }
    return out;
  }

  function completeOpenAI(provider, modelRec, apiKey, req, emit) {
    var baseURL = (provider.base_url || "https://api.openai.com/v1").replace(/\/+$/, "");
    var url = baseURL + "/chat/completions";

    var bodyObj = {
      model: modelRec.remote || modelRec.name,
      messages: toOpenAIMessages(req.system_prompt, req.messages || []),
      stream: true
    };
    if (!modelRec.skip_temperature && modelRec.temperature !== undefined && modelRec.temperature !== null) {
      bodyObj.temperature = modelRec.temperature;
    }
    if (modelRec.max_tokens && modelRec.max_tokens > 0) {
      bodyObj.max_tokens = modelRec.max_tokens;
    }
    if (modelRec.supports_reasoning_effort && modelRec.reasoning_default) {
      bodyObj.reasoning_effort = modelRec.reasoning_default;
    }
    var oaiTools = toOpenAITools(req.tools);
    if (oaiTools) {
      bodyObj.tools = oaiTools;
    }

    var headers = {
      "Content-Type": "application/json",
      "Accept": "text/event-stream",
      "x-opencode-session": (req.options && req.options.session_id) || "ses_cdx_default",
      "x-opencode-request": "req_" + Date.now()
    };
    if (apiKey) {
      headers["Authorization"] = "Bearer " + apiKey;
    }

    var content = "";
    var reasoning = "";
    var toolCallsByIndex = {};
    var usage = null;

    var resp = cdx.sys.fetch(url, {
      method: "POST",
      headers: headers,
      body: JSON.stringify(bodyObj),
      timeout_ms: 180000
    }, function(line) {
      var trimmed = line.trim();
      if (!trimmed || trimmed.indexOf("data:") !== 0) return;
      var dataStr = trimmed.slice(5).trim();
      if (dataStr === "[DONE]") return;

      var chunk;
      try {
        chunk = JSON.parse(dataStr);
      } catch (e) {
        return;
      }

      if (chunk.usage) {
        usage = chunk.usage;
      }
      if (!chunk.choices || chunk.choices.length === 0) return;
      var delta = chunk.choices[0].delta || {};

      if (delta.reasoning_content) {
        reasoning += delta.reasoning_content;
        emit({ type: "reasoning", delta: delta.reasoning_content });
      }
      if (delta.content) {
        content += delta.content;
        emit({ type: "text", delta: delta.content });
      }
      if (delta.tool_calls) {
        for (var i = 0; i < delta.tool_calls.length; i++) {
          var tc = delta.tool_calls[i];
          var idx = tc.index !== undefined ? tc.index : i;
          if (!toolCallsByIndex[idx]) {
            toolCallsByIndex[idx] = { id: tc.id || "", name: "", argsRaw: "" };
          }
          if (tc.id) toolCallsByIndex[idx].id = tc.id;
          if (tc.function) {
            if (tc.function.name) toolCallsByIndex[idx].name += tc.function.name;
            if (tc.function.arguments) toolCallsByIndex[idx].argsRaw += tc.function.arguments;
          }
        }
      }
    });

    if (!resp.ok) {
      throw new Error("OpenAI API error (" + resp.status + "): " + (resp.body || ""));
    }

    var toolCalls = [];
    var indices = Object.keys(toolCallsByIndex);
    for (var k = 0; k < indices.length; k++) {
      var item = toolCallsByIndex[indices[k]];
      var parsedArgs = {};
      if (item.argsRaw) {
        try {
          parsedArgs = JSON.parse(item.argsRaw);
        } catch (e) {}
      }
      toolCalls.push({
        id: item.id || ("call_" + k),
        name: item.name,
        arguments: parsedArgs
      });
    }

    return {
      content: content,
      reasoning: reasoning,
      tool_calls: toolCalls,
      usage: usage
    };
  }

  function completeAnthropic(provider, modelRec, apiKey, req, emit) {
    var baseURL = (provider.base_url || "https://api.anthropic.com").replace(/\/+$/, "");
    var url = baseURL + "/v1/messages";

    var anthroMsgs = [];
    var msgs = req.messages || [];
    for (var i = 0; i < msgs.length; i++) {
      var m = msgs[i];
      if (m.role === "tool") {
        anthroMsgs.push({
          role: "user",
          content: [{
            type: "tool_result",
            tool_use_id: m.tool_call_id || "call_0",
            content: String(m.content || "")
          }]
        });
      } else if (m.role === "assistant" && m.tool_calls && m.tool_calls.length > 0) {
        var blocks = [];
        if (m.content) {
          blocks.push({ type: "text", text: m.content });
        }
        for (var j = 0; j < m.tool_calls.length; j++) {
          var tc = m.tool_calls[j];
          blocks.push({
            type: "tool_use",
            id: tc.id || ("call_" + j),
            name: tc.name,
            input: tc.arguments || {}
          });
        }
        anthroMsgs.push({ role: "assistant", content: blocks });
      } else {
        anthroMsgs.push({
          role: m.role === "assistant" ? "assistant" : "user",
          content: String(m.content || "")
        });
      }
    }

    var bodyObj = {
      model: modelRec.remote || modelRec.name,
      max_tokens: modelRec.max_tokens || 4096,
      messages: anthroMsgs,
      stream: true
    };
    if (req.system_prompt) {
      bodyObj.system = req.system_prompt;
    }
    if (req.tools && req.tools.length > 0) {
      var aTools = [];
      for (var tIdx = 0; tIdx < req.tools.length; tIdx++) {
        var tl = req.tools[tIdx];
        aTools.push({
          name: tl.name,
          description: tl.description || "",
          input_schema: tl.input_schema || { type: "object", properties: {} }
        });
      }
      bodyObj.tools = aTools;
    }

    var headers = {
      "Content-Type": "application/json",
      "anthropic-version": "2023-06-01",
      "Accept": "text/event-stream"
    };
    if (apiKey) {
      headers["x-api-key"] = apiKey;
    }

    var content = "";
    var reasoning = "";
    var currentTools = {};

    var resp = cdx.sys.fetch(url, {
      method: "POST",
      headers: headers,
      body: JSON.stringify(bodyObj),
      timeout_ms: 180000
    }, function(line) {
      var trimmed = line.trim();
      if (trimmed.indexOf("data:") !== 0) return;
      var dataStr = trimmed.slice(5).trim();
      var ev;
      try {
        ev = JSON.parse(dataStr);
      } catch (e) {
        return;
      }

      if (ev.type === "content_block_start" && ev.content_block && ev.content_block.type === "tool_use") {
        currentTools[ev.index] = {
          id: ev.content_block.id,
          name: ev.content_block.name,
          jsonRaw: ""
        };
      } else if (ev.type === "content_block_delta" && ev.delta) {
        if (ev.delta.type === "text_delta" && ev.delta.text) {
          content += ev.delta.text;
          emit({ type: "text", delta: ev.delta.text });
        } else if (ev.delta.type === "thinking_delta" && ev.delta.thinking) {
          reasoning += ev.delta.thinking;
          emit({ type: "reasoning", delta: ev.delta.thinking });
        } else if (ev.delta.type === "input_json_delta" && currentTools[ev.index]) {
          currentTools[ev.index].jsonRaw += ev.delta.partial_json;
        }
      }
    });

    if (!resp.ok) {
      throw new Error("Anthropic API error (" + resp.status + "): " + (resp.body || ""));
    }

    var toolCalls = [];
    var keys = Object.keys(currentTools);
    for (var k = 0; k < keys.length; k++) {
      var item = currentTools[keys[k]];
      var parsed = {};
      if (item.jsonRaw) {
        try {
          parsed = JSON.parse(item.jsonRaw);
        } catch (e) {}
      }
      toolCalls.push({
        id: item.id,
        name: item.name,
        arguments: parsed
      });
    }

    return {
      content: content,
      reasoning: reasoning,
      tool_calls: toolCalls
    };
  }

  // Register provider capabilities with the core.
  cdx.providers.register({
    listModels: function() {
      var models = getModels();
      var out = [];
      for (var i = 0; i < models.length; i++) {
        var m = models[i];
        out.push({
          id: m.id || (m.provider + "/" + m.name),
          name: m.name,
          provider: m.provider,
          default: Boolean(m.default),
          context_window: positiveInt(m.context_window) || DEFAULT_CONTEXT_WINDOW
        });
      }
      return out;
    },
    complete: cdx.inWorker(function(req, emit) {
      var modelRec = findModel(req.model);
      if (!modelRec) {
        var all = getModels();
        if (all.length === 0) {
          throw new Error("No models configured. Add a provider and model in the Web UI.");
        }
        modelRec = all[0];
      }
      var provider = findProvider(modelRec.provider);
      if (!provider) {
        throw new Error("Provider '" + modelRec.provider + "' is not configured");
      }
      var apiKey = cdx.secrets.get(secretKeyFor(provider.name)) || "";
      if (provider.protocol === "anthropic") {
        return completeAnthropic(provider, modelRec, apiKey, req, emit);
      }
      return completeOpenAI(provider, modelRec, apiKey, req, emit);
    })
  });

  // Self-contained HTTP endpoints for managing providers, encrypted keys, and models.
  var api = cdx.http.group("/api/plugins/providers");

  api.get("/state", function(ctx) {
    var providers = getProviders();
    var outProviders = [];
    for (var i = 0; i < providers.length; i++) {
      var p = providers[i];
      outProviders.push({
        name: p.name,
        protocol: p.protocol || "openai-chat",
        base_url: p.base_url,
        has_api_key: cdx.secrets.has(secretKeyFor(p.name))
      });
    }
    ctx.json(200, {
      providers: outProviders,
      models: getModels().map(withModelDefaults)
    });
  });

  api.post("/providers", function(ctx) {
    var body = ctx.bindJSON() || {};
    var name = String(body.name || "").trim();
    if (!name) {
      ctx.json(400, { error: "provider name is required" });
      return;
    }
    var protocol = body.protocol || "openai-chat";
    var baseURL = String(body.base_url || "").trim();
    if (!baseURL) {
      baseURL = protocol === "anthropic" ? "https://api.anthropic.com" : "https://api.openai.com/v1";
    }

    var list = getProviders();
    var updated = false;
    for (var i = 0; i < list.length; i++) {
      if (list[i].name === name) {
        list[i].protocol = protocol;
        list[i].base_url = baseURL;
        updated = true;
        break;
      }
    }
    if (!updated) {
      list.push({ name: name, protocol: protocol, base_url: baseURL });
    }
    saveProviders(list);

    if (typeof body.api_key === "string" && body.api_key.length > 0) {
      cdx.secrets.set(secretKeyFor(name), body.api_key);
    }

    ctx.json(200, {
      name: name,
      protocol: protocol,
      base_url: baseURL,
      has_api_key: cdx.secrets.has(secretKeyFor(name))
    });
  });

  api.delete("/providers", function(ctx) {
    var name = ctx.query("name");
    if (!name) {
      ctx.json(400, { error: "provider name is required" });
      return;
    }
    var list = getProviders().filter(function(p) { return p.name !== name; });
    saveProviders(list);
    cdx.secrets.delete(secretKeyFor(name));

    var models = getModels().filter(function(m) { return m.provider !== name; });
    saveModels(models);
    ctx.json(200, { deleted: name });
  });

  api.post("/providers/test", function(ctx) {
    var body = ctx.bindJSON() || {};
    var name = String(body.name || "");
    var provider = findProvider(name);
    if (!provider) {
      ctx.json(404, { error: "provider not found" });
      return;
    }
    var apiKey = cdx.secrets.get(secretKeyFor(name)) || "";
    var url = (provider.base_url || "https://api.openai.com/v1").replace(/\/+$/, "") + "/models";
    var headers = { "Accept": "application/json" };
    if (apiKey) {
      if (provider.protocol === "anthropic") {
        headers["x-api-key"] = apiKey;
        headers["anthropic-version"] = "2023-06-01";
      } else {
        headers["Authorization"] = "Bearer " + apiKey;
      }
    }
    try {
      var resp = cdx.sys.fetch(url, { method: "GET", headers: headers, timeout_ms: 10000 });
      if (resp.ok) {
        ctx.json(200, { ok: true, message: "Connection OK (" + resp.status + ")" });
      } else {
        ctx.json(200, { ok: false, message: "HTTP " + resp.status });
      }
    } catch (e) {
      ctx.json(200, { ok: false, message: String(e) });
    }
  });

  api.post("/providers/key", function(ctx) {
    var body = ctx.bindJSON() || {};
    var name = String(body.name || "");
    if (!name) {
      ctx.json(400, { error: "provider name is required" });
      return;
    }
    var key = String(body.api_key || "");
    if (!key) {
      cdx.secrets.delete(secretKeyFor(name));
    } else {
      cdx.secrets.set(secretKeyFor(name), key);
    }
    ctx.json(200, { has_api_key: cdx.secrets.has(secretKeyFor(name)) });
  });

  api.post("/models/default", function(ctx) {
    var body = ctx.bindJSON() || {};
    var provider = String(body.provider || "");
    var name = String(body.name || "");
    var id = provider + "/" + name;
    var models = getModels();
    for (var i = 0; i < models.length; i++) {
      models[i].default = (models[i].provider === provider && models[i].name === name);
    }
    saveModels(models);
    ctx.json(200, { default: id });
  });

  api.post("/providers/discover", function(ctx) {
    var body = ctx.bindJSON() || {};
    var name = String(body.name || "");
    var provider = findProvider(name);
    if (!provider) {
      ctx.json(404, { error: "provider not found" });
      return;
    }
    var apiKey = cdx.secrets.get(secretKeyFor(name)) || "";
    var url = (provider.base_url || "https://api.openai.com/v1").replace(/\/+$/, "") + "/models";
    var headers = { "Accept": "application/json" };
    if (apiKey) {
      if (provider.protocol === "anthropic") {
        headers["x-api-key"] = apiKey;
        headers["anthropic-version"] = "2023-06-01";
      } else {
        headers["Authorization"] = "Bearer " + apiKey;
      }
    }
    var resp;
    try {
      resp = cdx.sys.fetch(url, { method: "GET", headers: headers, timeout_ms: 15000 });
    } catch (e) {
      ctx.json(502, { error: "discover failed: " + String(e) });
      return;
    }
    if (!resp.ok) {
      ctx.json(502, { error: "discover failed (HTTP " + resp.status + ")" });
      return;
    }
    var parsed = JSON.parse(resp.body || "{}");
    var items = parsed.data || [];
    var discovered = [];
    for (var i = 0; i < items.length; i++) {
      if (items[i] && items[i].id) {
        discovered.push(discoverModelMeta(items[i]));
      }
    }
    ctx.json(200, { models: discovered });
  });

  api.post("/models", function(ctx) {
    var body = ctx.bindJSON() || {};
    var provider = String(body.provider || "").trim();
    var name = String(body.name || "").trim();
    var remote = String(body.remote || name).trim();
    if (!provider || !name) {
      ctx.json(400, { error: "provider and model name are required" });
      return;
    }
    var id = provider + "/" + name;
    var models = getModels();
    var isDefault = Boolean(body.default) || models.length === 0;

    if (isDefault) {
      for (var i = 0; i < models.length; i++) {
        models[i].default = false;
      }
    }

    var rec = {
      id: id,
      provider: provider,
      name: name,
      remote: remote,
      default: isDefault,
      context_window: positiveInt(body.context_window) || DEFAULT_CONTEXT_WINDOW,
      max_tokens: positiveInt(body.max_tokens) || DEFAULT_MAX_TOKENS,
      temperature: body.temperature !== undefined ? Number(body.temperature) : 1,
      reasoning_levels: body.reasoning_levels || [],
      reasoning_default: body.reasoning_default || "",
      supports_reasoning_effort: Boolean(body.supports_reasoning_effort),
      skip_temperature: Boolean(body.skip_temperature)
    };

    var replaced = false;
    for (var j = 0; j < models.length; j++) {
      if (models[j].id === id || (models[j].provider === provider && models[j].name === name)) {
        models[j] = rec;
        replaced = true;
        break;
      }
    }
    if (!replaced) {
      models.push(rec);
    }
    saveModels(models);
    ctx.json(200, rec);
  });

  api.delete("/models", function(ctx) {
    var provider = ctx.query("provider");
    var name = ctx.query("name");
    if (!provider || !name) {
      ctx.json(400, { error: "provider and model name are required" });
      return;
    }
    var id = provider + "/" + name;
    var before = getModels();
    var models = before.filter(function(m) {
      return !(m.id === id || (m.provider === provider && m.name === name));
    });
    saveModels(models);
    ctx.json(200, { deleted: id, removed: before.length - models.length });
  });

  cdx.ui.register({
    title: "Providers & Models",
    description: "Manage LLM connections, encrypted API keys, and model configurations",
    order: 5,
    entry: "/ui/providers/dist/panel.js",
    ui_api: 1
  });

  cdx.http.static("/ui/providers", "./web");
}
