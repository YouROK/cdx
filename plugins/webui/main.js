function setup(cdx) {
  var indexHTML;
  try {
    indexHTML = cdx.sys.readFile(cdx.dir + "/web/dist/index.html");
  } catch (e) {
    indexHTML = "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>cdx</title></head>" +
      "<body><main style=\"font-family:system-ui;padding:32px\">" +
      "<h1>cdx Web UI is not built</h1>" +
      "<p>Run <code>make</code> from the cdx source tree and reload this page.</p>" +
      "</main></body></html>";
  }
  var faviconSVG = cdx.sys.readFile(cdx.dir + "/web/favicon.svg");

  cdx.http.get("/", function(ctx) {
    ctx.html(200, indexHTML);
  });

  cdx.http.get("/favicon.svg", function(ctx) {
    ctx.header("Content-Type", "image/svg+xml");
    ctx.string(200, faviconSVG);
  });

  cdx.http.get("/favicon.ico", function(ctx) {
    ctx.header("Content-Type", "image/svg+xml");
    ctx.string(200, faviconSVG);
  });

  cdx.http.static("/ui/webui", "./web");
}
