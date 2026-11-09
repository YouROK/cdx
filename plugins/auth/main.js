function setup(cdx) {
  var TOKEN_KEY = "token";
  var REMOTE_ACCESS_ENV = "CDX_ALLOW_REMOTE_HOST";
  var LOOPBACK_HOSTS = ["127.0.0.1", "localhost", "::1", "[::1]"];

  var token = cdx.secrets.get(TOKEN_KEY);
  if (!token) {
    token = cdx.sys.random(32);
    cdx.secrets.set(TOKEN_KEY, token);
    cdx.log.info("Generated a new API token");
  }

  // Any non-empty value except an explicit negative enables the flag.
  function envEnabled(name) {
    var value = "";
    try {
      if (typeof cdx.sys.env === "function") value = String(cdx.sys.env(name) || "");
    } catch (e) {
      value = "";
    }
    value = value.trim().toLowerCase();
    if (value === "") return false;
    return value !== "0" && value !== "false" && value !== "no" && value !== "off";
  }

  var openAccess = envEnabled(REMOTE_ACCESS_ENV);
  if (openAccess) {
    cdx.log.warn("Remote access is enabled: host and token checks are disabled");
  }

  // Allows access from a non-local host header, which disables the
  // rebinding protection; only the token remains in that case.
  function allowRemoteHost() {
    return cdx.storage.get("allow_remote_host") === true;
  }

  // Host without the port: "127.0.0.1:8787", "[::1]:8787" and "localhost" all fit.
  function hostName(host) {
    host = String(host || "").trim().toLowerCase();
    if (host.charAt(0) === "[") {
      var closing = host.indexOf("]");
      return closing === -1 ? host : host.slice(0, closing + 1);
    }
    var colon = host.lastIndexOf(":");
    if (colon > 0) return host.slice(0, colon);
    return host;
  }

  function isLoopback(host) {
    var name = hostName(host);
    for (var i = 0; i < LOOPBACK_HOSTS.length; i++) {
      if (name === LOOPBACK_HOSTS[i]) return true;
    }
    return false;
  }

  // Compares the values without exiting early on the first different
  // character, so the comparison time does not depend on the input.
  function sameToken(given) {
    var a = String(given || "");
    var b = String(token);
    var diff = a.length ^ b.length;
    var length = Math.max(a.length, b.length);
    for (var i = 0; i < length; i++) {
      diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
    }
    return diff === 0;
  }

  cdx.http.use("/api", function(ctx) {
    if (openAccess) return;
    if (!allowRemoteHost() && !isLoopback(ctx.host())) {
      ctx.abortWithJSON(403, { error: "requests to the API must use a local host" });
      return;
    }
    if (!sameToken(ctx.getHeader("X-CDX-Token"))) {
      ctx.abortWithJSON(401, { error: "token is missing or invalid" });
      return;
    }
  });

  // The dashboard asks for the token to put it into the served page.
  cdx.bus.handle("auth:token", function() {
    if (openAccess) return { token: "" };
    return { token: token };
  });

  cdx.ui.register({
    title: "API access",
    description: "Token protection for the HTTP API and the local-host check",
    order: 1030,
    schema: {
      type: "object",
      title: "Access settings",
      properties: {
        allow_remote_host: {
          type: "boolean",
          title: "Allow non-local host header",
          description: "Lets other machines reach the API through a proxy or SSH tunnel. The token is still required. Setting CDX_ALLOW_REMOTE_HOST disables both checks.",
          default: false
        }
      }
    }
  });
}
