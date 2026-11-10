import assert from "node:assert/strict";
import { test } from "node:test";
import { translate } from "./i18n.js";

test("translate returns the catalog value", () => {
  assert.equal(translate({ "webui.Nav.Sessions": "Sessions" }, "webui.Nav.Sessions"), "Sessions");
});

test("translate falls back to the key", () => {
  assert.equal(translate({}, "webui.Nav.Sessions"), "webui.Nav.Sessions");
});

test("translate fills placeholders", () => {
  assert.equal(
    translate({ "webui.Schema.FieldRequired": "Field {name} is required" }, "webui.Schema.FieldRequired", { name: "Token" }),
    "Field Token is required"
  );
});

test("translate keeps unknown placeholders", () => {
  assert.equal(translate({ "x": "Hello {name}" }, "x", {}), "Hello {name}");
});

test("translate ignores a non-string key", () => {
  assert.equal(translate({}, undefined), undefined);
});
