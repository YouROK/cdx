// The token is embedded into the page by the webui plugin, so every request
// carries it and the API can stay closed to other origins.
export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export function apiToken() {
  if (typeof window === "undefined") return "";
  const token = window.__CDX_TOKEN__;
  return typeof token === "string" ? token : "";
}

export async function apiFetch(url, options) {
  const headers = new Headers(options?.headers || {});
  const token = apiToken();
  if (token) headers.set("X-CDX-Token", token);
  const res = await fetch(url, { ...options, headers });
  const text = await res.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    throw new ApiError(data.error || data.message || "HTTP " + res.status, res.status);
  }
  return data;
}

export function postJSON(url, body, method) {
  return apiFetch(url, {
    method: method || "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body)
  });
}
