import test from "node:test";
import assert from "node:assert/strict";

import { ApiError, apiFetch } from "./api.js";

function jsonResponse(body, status) {
  return new Response(JSON.stringify(body), {
    status: status || 200,
    headers: { "Content-Type": "application/json" }
  });
}

function stubFetch(handler) {
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return handler(url, options);
  };
  return calls;
}

test.afterEach(() => {
  delete globalThis.fetch;
  delete globalThis.window;
});

test("apiFetch returns the parsed body", async () => {
  stubFetch(() => jsonResponse({ version: "1.0.0" }));
  const data = await apiFetch("/api/core/status");
  assert.equal(data.version, "1.0.0");
});

test("apiFetch sends the token from the page", async () => {
  globalThis.window = { __CDX_TOKEN__: "secret-token" };
  const calls = stubFetch(() => jsonResponse({ ok: true }));
  await apiFetch("/api/core/status");
  assert.equal(calls[0].options.headers.get("X-CDX-Token"), "secret-token");
});

test("apiFetch keeps the response error message", async () => {
  stubFetch(() => jsonResponse({ error: "plugin is not running" }, 400));
  await assert.rejects(
    () => apiFetch("/api/core/plugins/x/settings"),
    (err) => {
      assert.ok(err instanceof ApiError);
      assert.equal(err.status, 400);
      assert.equal(err.message, "plugin is not running");
      return true;
    }
  );
});

test("apiFetch falls back to the message field", async () => {
  stubFetch(() => jsonResponse({ message: "provider not found" }, 404));
  await assert.rejects(
    () => apiFetch("/api/plugins/providers/providers/test"),
    (err) => {
      assert.equal(err.message, "provider not found");
      return true;
    }
  );
});

test("apiFetch reports a plain text failure", async () => {
  stubFetch(() => new Response("not found", { status: 404 }));
  await assert.rejects(
    () => apiFetch("/missing"),
    (err) => {
      assert.equal(err.message, "not found");
      return true;
    }
  );
});
