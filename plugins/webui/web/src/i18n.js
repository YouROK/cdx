// translate resolves a catalog value and fills {name} placeholders.
export function translate(strings, key, vars) {
  if (typeof key !== "string") return key;
  const has = strings && Object.prototype.hasOwnProperty.call(strings, key);
  let text = has ? strings[key] : key;
  if (!vars || typeof text !== "string") return text;
  return text.replace(/\{(\w+)\}/g, (match, name) => (
    vars[name] === undefined || vars[name] === null ? match : String(vars[name])
  ));
}
