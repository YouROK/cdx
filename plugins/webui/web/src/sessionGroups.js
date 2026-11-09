// groupSessions builds parent rows with their direct child rows.
export function groupSessions(sessions) {
  const list = Array.isArray(sessions) ? sessions : [];
  const byID = new Map(list.map((session) => [session.id, session]));
  const children = new Map();
  const roots = [];

  for (const session of list) {
    const parentID = session.parent_id || "";
    if (parentID && byID.has(parentID)) {
      if (!children.has(parentID)) children.set(parentID, []);
      children.get(parentID).push(session);
    } else {
      roots.push(session);
    }
  }

  const byUpdated = (a, b) => (b.updated_at || 0) - (a.updated_at || 0);
  roots.sort(byUpdated);
  for (const items of children.values()) items.sort(byUpdated);

  return roots.map((session) => ({
    session,
    children: children.get(session.id) || [],
  }));
}
