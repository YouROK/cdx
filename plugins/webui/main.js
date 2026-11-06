var INDEX_FILE = "/web/dist/index.html";
var FAVICON_FILE = "/web/favicon.svg";

function setup(cdx) {
  var faviconSVG = "";
  try {
    faviconSVG = cdx.sys.readFile(cdx.dir + FAVICON_FILE);
  } catch (e) {
    cdx.log.warn("Favicon is missing: " + e);
  }

  function indexHTML() {
    try {
      return cdx.sys.readFile(cdx.dir + INDEX_FILE);
    } catch (e) {
      return "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>cdx</title></head>" +
        "<body><main style=\"font-family:system-ui;padding:32px\">" +
        "<h1>cdx Web UI is not built</h1>" +
        "<p>Run <code>make</code> from the cdx source tree and reload this page.</p>" +
        "</main></body></html>";
    }
  }

  // The API token comes from the auth plugin; without it the API stays open.
  function apiToken() {
    try {
      var res = cdx.bus.request("auth:token", {});
      if (res && typeof res.token === "string") return res.token;
    } catch (e) {
      // The auth plugin is disabled or missing.
    }
    return "";
  }

  // The token is embedded into the page, so the interface does not depend on
  // the URL and survives a tab reload. Only same-origin scripts can read it.
  function withToken(html, token) {
    if (!token) return html;
    var tag = "<script>window.__CDX_TOKEN__=" + JSON.stringify(token) + "</script>";
    if (html.indexOf("</head>") === -1) return html + tag;
    return html.replace("</head>", tag + "</head>");
  }

  cdx.http.get("/", function(ctx) {
    ctx.header("Cache-Control", "no-store");
    ctx.html(200, withToken(indexHTML(), apiToken()));
  });

  cdx.http.get("/favicon.svg", function(ctx) {
    ctx.header("Content-Type", "image/svg+xml");
    ctx.string(200, faviconSVG);
  });

  cdx.http.get("/favicon.ico", function(ctx) {
    ctx.header("Content-Type", "image/svg+xml");
    ctx.string(200, faviconSVG);
  });

  // Only built assets are served; sources and build metadata stay internal.
  cdx.http.static("/ui/webui", "./web/dist");
}
