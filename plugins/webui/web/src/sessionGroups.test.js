import test from "node:test";
import assert from "node:assert/strict";

import { groupSessions } from "./sessionGroups.js";

test("groups child sessions under their parent", () => {
  const groups = groupSessions([
    { id: "ses_child_old", parent_id: "ses_parent", updated_at: 20 },
    { id: "ses_parent", updated_at: 10 },
    { id: "ses_child_new", parent_id: "ses_parent", updated_at: 30 },
    { id: "ses_other", updated_at: 40 },
  ]);

  assert.equal(groups.length, 2);
  assert.equal(groups[0].session.id, "ses_other");
  assert.equal(groups[1].session.id, "ses_parent");
  assert.deepEqual(
    groups[1].children.map((session) => session.id),
    ["ses_child_new", "ses_child_old"],
  );
});

test("keeps orphans at the root", () => {
  const groups = groupSessions([{ id: "ses_orphan", parent_id: "ses_missing", updated_at: 1 }]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].session.id, "ses_orphan");
  assert.equal(groups[0].children.length, 0);
});
