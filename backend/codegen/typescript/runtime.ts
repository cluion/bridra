// The browser carries its session cookie. The backend credential belongs to the gateway.
export class HttpError extends Error {
  constructor(public readonly status: number) {
    super(`RPC HTTP ${status}`);
    this.name = "HttpError";
  }
}

export class RpcError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = "RpcError";
  }
}

export function record(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`Expected an object at ${label}`);
  }
  return value as Record<string, unknown>;
}

// A missing wire field must stay missing even when its name exists on Object.prototype.
export function field(data: Record<string, unknown>, name: string): unknown {
  return Object.hasOwn(data, name) ? data[name] : undefined;
}

export function stringValue(value: unknown, label: string): string {
  if (typeof value !== "string") throw new Error(`Expected a string at ${label}`);
  return value;
}

export function integerValue(value: unknown, label: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) {
    throw new Error(`Expected a safe integer at ${label}`);
  }
  return value;
}

export function dateTimeValue(value: unknown, label: string): string {
  const timestamp = stringValue(value, label);
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/.test(timestamp)
      || !Number.isFinite(Date.parse(timestamp))) {
    throw new Error(`Invalid timestamp at ${label}`);
  }
  return timestamp;
}

export function arrayValue<T>(value: unknown, label: string,
    decode: (item: unknown, label: string) => T): T[] {
  if (!Array.isArray(value)) throw new Error(`Expected an array at ${label}`);
  return value.map((item, index) => decode(item, `${label}[${index}]`));
}

export async function call(method: string, params?: object): Promise<Record<string, unknown>> {
  const id = crypto.randomUUID();
  const response = await fetch("/rpc", {
    method: "POST",
    credentials: "same-origin",
    mode: "same-origin",
    redirect: "error",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ id, method, params }),
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new HttpError(response.status);
  const reply = record(await response.json(), method);
  if (reply.id !== id) throw new Error(`Mismatched response ID for ${method}`);
  if (reply.error != null) {
    const failure = record(reply.error, "error");
    throw new RpcError(stringValue(failure.code, "error.code"), stringValue(failure.message, "error.message"));
  }
  if (reply.stream != null) throw new Error(`Unexpected streaming response for ${method}`);
  return reply;
}

export function numberValue(value: unknown, label: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) throw new Error(`Expected a number at ${label}`);
  return value;
}

export function booleanValue(value: unknown, label: string): boolean {
  if (typeof value !== "boolean") throw new Error(`Expected a boolean at ${label}`);
  return value;
}

export function enumValue<const T extends string>(value: unknown, label: string, values: readonly T[]): T {
  const match = values.find(item => item === value);
  if (match === undefined) throw new Error(`Unexpected value at ${label}`);
  return match;
}

export function nullableValue<T>(value: unknown, label: string, decode: (v: unknown, label: string) => T): T | null {
  return value == null ? null : decode(value, label);
}
