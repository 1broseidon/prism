export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
    ...init,
  });
  const text = await res.text();

  if (!res.ok) {
    let body: unknown = text;
    try {
      body = JSON.parse(text);
    } catch {
      // keep raw text
    }
    let msg: string;
    if (typeof body === "object" && body && "error" in body) {
      msg = String((body as { error: unknown }).error);
    } else if (typeof body === "string" && body.trim()) {
      // Plain-text error body from http.Error — surface it directly.
      msg = body.trim();
    } else {
      msg = `${res.status} ${res.statusText}`;
    }
    throw new ApiError(res.status, body, msg);
  }

  if (!text) return undefined as T;

  try {
    return JSON.parse(text) as T;
  } catch {
    // Non-JSON success body — almost always a proxy hitting the wrong port
    // (e.g. Cockpit returning its loading HTML). Fail loudly so the
    // misconfiguration is visible instead of poisoning typed signals.
    const preview = text.slice(0, 120).replace(/\s+/g, " ");
    throw new ApiError(
      res.status,
      text,
      `expected JSON from ${path} but got: ${preview}`,
    );
  }
}

export function getJSON<T>(path: string): Promise<T> {
  return request<T>(path);
}

export function putJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: "PUT", body: JSON.stringify(body) });
}

export function postJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: "POST", body: JSON.stringify(body) });
}

export function deleteJSON<T>(path: string): Promise<T> {
  return request<T>(path, { method: "DELETE" });
}
