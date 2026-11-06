// Routes live in the URL hash, so the interface survives a reload and every
// section can be linked without a server-side fallback.
export function parseRoute(hash) {
  const raw = String(hash || "");
  const withoutHash = raw.startsWith("#") ? raw.slice(1) : raw;
  const path = (withoutHash.split("?")[0] || "").replace(/\/+$/, "") || "/";
  return { path, parts: path.split("/").filter(Boolean) };
}

export function routeHash(path) {
  const clean = "/" + String(path || "").replace(/^\/+/, "");
  return "#" + clean;
}

export function navigate(path) {
  const next = routeHash(path);
  if (window.location.hash !== next) {
    window.location.hash = next;
  }
}

export function activeSection(route, pluginIDs) {
  const [first, second] = route.parts;
  if (!first) return "general";
  if (first === "sessions") return "sessions";
  if (first === "plugins") return "plugins";
  if (first === "logs") return "logs";
  if (first === "plugin" && second && pluginIDs.includes(second)) return "plugin:" + second;
  return "general";
}
