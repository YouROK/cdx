// Rebuilds the interface and the plugin panels whenever a source file changes.
// The daemon reads index.html from disk on every request, so a page reload
// picks up the new bundles without restarting anything.
import { spawn } from "node:child_process";
import { once } from "node:events";

const configs = [
  "plugins/webui/web/vite.config.js",
  "plugins/providers/web/vite.config.js",
  "plugins/presets/web/vite.config.js",
  "plugins/permissions/web/vite.config.js"
];

function run(args) {
  const child = spawn("npx", ["vite", ...args], { stdio: "inherit" });
  child.on("exit", (code) => {
    if (code) process.exit(code);
  });
  return child;
}

// The shared React runtime is a one-off build; watchers only rebuild the app.
await once(run(["build", "--config", "plugins/webui/web/vite.vendor.config.js"]), "exit");

const children = configs.map((config) => run(["build", "--watch", "--config", config]));

function stop() {
  for (const child of children) child.kill("SIGTERM");
  process.exit(0);
}

process.on("SIGINT", stop);
process.on("SIGTERM", stop);
