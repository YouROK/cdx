// How many stored messages the live view shows before the running turn.
export const RECENT_MESSAGE_COUNT = 4;

// How many more messages each "show earlier" click reveals.
export const EARLIER_PAGE_SIZE = 20;

// splitHistory separates the history before the running turn from the messages
// the turn already stored. An unknown count means the whole list is history.
export function splitHistory(messages, historyCount) {
  const list = Array.isArray(messages) ? messages : [];
  if (!Number.isInteger(historyCount) || historyCount < 0) {
    return { earlier: list, current: [] };
  }
  const count = Math.min(historyCount, list.length);
  return { earlier: list.slice(0, count), current: list.slice(count) };
}

// newestSlice keeps the newest items and preserves their order.
export function newestSlice(items, limit) {
  const list = Array.isArray(items) ? items : [];
  const size = Math.max(0, Math.trunc(limit) || 0);
  if (size === 0) return [];
  if (list.length <= size) return list;
  return list.slice(list.length - size);
}

// nextVisibleCount grows the visible window by one page, never past the total.
export function nextVisibleCount(current, total) {
  const shown = Math.max(0, Math.trunc(current) || 0);
  const all = Math.max(0, Math.trunc(total) || 0);
  return Math.min(all, shown + EARLIER_PAGE_SIZE);
}
