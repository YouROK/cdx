function setup(cdx) {
  var indexHTML = cdx.sys.readFile(cdx.dir + "/web/index.html");
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
