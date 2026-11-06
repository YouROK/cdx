import test from "node:test";
import assert from "node:assert/strict";

import { EARLIER_PAGE_SIZE, newestSlice, nextVisibleCount, splitHistory } from "./sessionView.js";

function messages(count) {
  return Array.from({ length: count }, (_, index) => ({ role: "user", content: "m" + index }));
}

test("splitHistory keeps the messages stored before the running turn", () => {
  const list = messages(6);
  const split = splitHistory(list, 4);
  assert.equal(split.earlier.length, 4);
  assert.equal(split.earlier[3].content, "m3");
  assert.equal(split.current.length, 2);
  assert.equal(split.current[0].content, "m4");
});

test("splitHistory clamps a count beyond the list", () => {
  const split = splitHistory(messages(2), 10);
  assert.equal(split.earlier.length, 2);
  assert.equal(split.current.length, 0);
});

test("splitHistory treats an unknown count as a full history", () => {
  const list = messages(3);
  const split = splitHistory(list, null);
  assert.equal(split.earlier, list);
  assert.deepEqual(split.current, []);
  assert.deepEqual(splitHistory(undefined, undefined), { earlier: [], current: [] });
});

test("newestSlice keeps the newest messages in order", () => {
  const list = messages(5);
  const tail = newestSlice(list, 2);
  assert.equal(tail.length, 2);
  assert.equal(tail[0].content, "m3");
  assert.equal(tail[1].content, "m4");
  assert.equal(newestSlice(list, 0).length, 0);
  assert.equal(newestSlice(list, 10), list);
});

test("nextVisibleCount reveals one page without passing the total", () => {
  assert.equal(nextVisibleCount(4, 30), 4 + EARLIER_PAGE_SIZE);
  assert.equal(nextVisibleCount(25, 30), 30);
  assert.equal(nextVisibleCount(4, 4), 4);
});
