import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

const root = path.resolve(import.meta.dirname, "../../../..");

function walk(dir, out) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name === "dist") continue;
      walk(full, out);
    } else if (entry.name.endsWith(".js") || entry.name.endsWith(".jsx")) {
      if (!entry.name.endsWith(".test.js")) out.push(full);
    }
  }
}

function sources() {
  const files = [];
  walk(path.join(root, "plugins/webui/web/src"), files);
  for (const entry of fs.readdirSync(path.join(root, "plugins"), { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const panelSrc = path.join(root, "plugins", entry.name, "web", "src");
    if (fs.existsSync(panelSrc)) walk(panelSrc, files);
    const main = path.join(root, "plugins", entry.name, "main.js");
    if (fs.existsSync(main)) files.push(main);
  }
  return files;
}

test("every literal t() key exists in the base catalog", () => {
  const catalog = JSON.parse(fs.readFileSync(path.join(root, "plugins/i18n/i18n/en.json"), "utf8"));
  const missing = [];
  for (const file of sources()) {
    const text = fs.readFileSync(file, "utf8");
    for (const match of text.matchAll(/\bt\(\s*"([A-Za-z0-9_.]+)"/g)) {
      const tail = text.slice(match.index + match[0].length);
      if (/^\s*\+/.test(tail)) continue;
      const key = match[1];
      if (!Object.prototype.hasOwnProperty.call(catalog, key)) {
        missing.push(path.relative(root, file) + ": " + key);
      }
    }
  }
  assert.deepEqual([...new Set(missing)], []);
});
