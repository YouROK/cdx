// Collects translation tables from plugins and serves the merged catalog.
function setup(cdx) {
  var tables = {};      // plugin id -> language -> flat string map
  var locale = "en";
  var revision = 0;
  var cache = null;

  function invalidate() {
    revision++;
    cache = null;
  }

  function normalizeTables(input) {
    var out = {};
    if (!input || typeof input !== "object") return out;
    for (var lang in input) {
      if (!Object.prototype.hasOwnProperty.call(input, lang)) continue;
      if (typeof lang !== "string" || lang === "") continue;
      var table = input[lang];
      if (!table || typeof table !== "object") continue;
      var clean = {};
      for (var key in table) {
        if (!Object.prototype.hasOwnProperty.call(table, key)) continue;
        if (typeof table[key] === "string") clean[key] = table[key];
      }
      out[lang] = clean;
    }
    return out;
  }

  function languages() {
    var seen = { en: true };
    for (var id in tables) {
      for (var lang in tables[id]) seen[lang] = true;
    }
    return Object.keys(seen).sort();
  }

  function effectiveLocale() {
    if (locale === "en" || languages().indexOf(locale) !== -1) return locale;
    return "en";
  }

  // Reads a plugin field from cdx.plugins.list(), which exposes Go field names.
  function pluginField(info, name) {
    if (!info) return undefined;
    if (info[name] !== undefined) return info[name];
    var cap = name.charAt(0).toUpperCase() + name.slice(1);
    if (info[cap] !== undefined) return info[cap];
    return undefined;
  }

  function layerIDs() {
    var list = cdx.plugins.list() || [];
    var byID = {};
    for (var i = 0; i < list.length; i++) {
      var id = pluginField(list[i], "id");
      if (typeof id === "string") byID[id] = list[i];
    }
    function orderOf(info) {
      var value = pluginField(info, "order");
      var number = Number(value);
      return isNaN(number) ? 5000 : number;
    }
    var ids = Object.keys(tables);
    ids.sort(function (a, b) {
      var oa = orderOf(byID[a]);
      var ob = orderOf(byID[b]);
      if (oa !== ob) return oa - ob;
      return a < b ? -1 : a > b ? 1 : 0;
    });
    return ids;
  }

  function mergeInto(target, table) {
    if (!table) return;
    for (var key in table) {
      if (Object.prototype.hasOwnProperty.call(table, key)) target[key] = table[key];
    }
  }

  function build(lang) {
    var ids = layerIDs();
    var strings = {};
    var i;
    for (i = 0; i < ids.length; i++) mergeInto(strings, tables[ids[i]].en);
    if (lang !== "en") {
      for (i = 0; i < ids.length; i++) mergeInto(strings, tables[ids[i]][lang]);
    }
    return { locale: lang, revision: revision, strings: strings };
  }

  function catalog() {
    var lang = effectiveLocale();
    if (cache && cache.locale === lang && cache.revision === revision) return cache.value;
    cache = { locale: lang, revision: revision, value: build(lang) };
    return cache.value;
  }

  function loadBase() {
    var base = {};
    try {
      base = normalizeTables({ en: JSON.parse(cdx.sys.readFile(cdx.dir + "/i18n/en.json")) }).en;
    } catch (e) {
      cdx.log.warn("Failed to load the base catalog: " + e);
    }
    tables[cdx.id] = { en: base || {} };
  }

  function dropPlugin(payload) {
    var id = payload && payload.id;
    if (typeof id !== "string" || id === cdx.id) return;
    if (!tables[id]) return;
    delete tables[id];
    invalidate();
  }

  loadBase();
  try {
    var saved = cdx.storage.get("locale");
    if (typeof saved === "string" && saved !== "") locale = saved;
  } catch (e) {
    cdx.log.warn("Failed to read the saved locale: " + e);
  }

  cdx.bus.handle("i18n:register", function (payload) {
    var id = payload && payload.id;
    if (typeof id !== "string" || id === "") {
      throw new Error("i18n:register requires a plugin id");
    }
    if (id === cdx.id) {
      throw new Error("the i18n base catalog cannot be replaced");
    }
    var incoming = normalizeTables(payload.tables);
    if (Object.keys(incoming).length === 0) {
      throw new Error("i18n:register requires at least one language table");
    }
    tables[id] = incoming;
    invalidate();
    return { id: id, languages: Object.keys(incoming).sort() };
  });

  cdx.bus.handle("i18n:catalog", function () {
    return catalog();
  });

  cdx.bus.subscribe("plugin:disabled", dropPlugin);
  cdx.bus.subscribe("plugin:removed", dropPlugin);

  cdx.http.get("/api/plugins/i18n/catalog", function (ctx) {
    ctx.header("Cache-Control", "no-store");
    ctx.json(200, catalog());
  });

  cdx.http.get("/api/plugins/i18n/locales", function (ctx) {
    ctx.header("Cache-Control", "no-store");
    ctx.json(200, { locale: effectiveLocale(), locales: languages() });
  });

  cdx.http.post("/api/plugins/i18n/locale", function (ctx) {
    var body = ctx.bindJSON() || {};
    var next = body.locale;
    var available = languages();
    if (typeof next !== "string" || available.indexOf(next) === -1) {
      ctx.json(400, { error: "unsupported locale" });
      return;
    }
    locale = next;
    cdx.storage.set("locale", locale);
    invalidate();
    ctx.json(200, { locale: effectiveLocale(), locales: available });
  });

  cdx.http.static("/ui/i18n", "./web");
  cdx.ui.register({
    title: "i18n.Title",
    description: "i18n.Description",
    order: 995,
    entry: "/ui/i18n/panel.js",
    ui_api: 1
  });
}
