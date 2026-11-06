import test from "node:test";
import assert from "node:assert/strict";

import { activeSection, parseRoute, routeHash } from "./route.js";

test("parseRoute handles the default route", () => {
  assert.deepEqual(parseRoute(""), { path: "/", parts: [] });
  assert.deepEqual(parseRoute("#"), { path: "/", parts: [] });
  assert.deepEqual(parseRoute("#/"), { path: "/", parts: [] });
});

test("parseRoute splits the path", () => {
  assert.deepEqual(parseRoute("#/sessions"), { path: "/sessions", parts: ["sessions"] });
  assert.deepEqual(parseRoute("#/sessions/ses_1"), { path: "/sessions/ses_1", parts: ["sessions", "ses_1"] });
  assert.deepEqual(parseRoute("#/plugin/providers"), { path: "/plugin/providers", parts: ["plugin", "providers"] });
});

test("parseRoute drops trailing slashes and the query", () => {
  assert.deepEqual(parseRoute("#/sessions/"), { path: "/sessions", parts: ["sessions"] });
  assert.deepEqual(parseRoute("#/?token=abc"), { path: "/", parts: [] });
});

test("routeHash builds a hash from a path", () => {
  assert.equal(routeHash("/"), "#/");
  assert.equal(routeHash("sessions"), "#/sessions");
});

test("activeSection maps a route to a menu entry", () => {
  const plugins = ["providers", "permissions"];
  assert.equal(activeSection(parseRoute(""), plugins), "general");
  assert.equal(activeSection(parseRoute("#/sessions/ses_1"), plugins), "sessions");
  assert.equal(activeSection(parseRoute("#/plugins"), plugins), "plugins");
  assert.equal(activeSection(parseRoute("#/logs"), plugins), "logs");
  assert.equal(activeSection(parseRoute("#/plugin/providers"), plugins), "plugin:providers");
  assert.equal(activeSection(parseRoute("#/plugin/removed"), plugins), "general");
});
