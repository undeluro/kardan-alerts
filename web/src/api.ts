export type Status = "open" | "acked" | "resolved";

export interface AlertField {
  label: string;
  value: string;
}

export interface Alert {
  id: string;
  fingerprint: string;
  ruleId: string;
  team: string;
  status: Status;
  severity: string;
  color: string;
  title: string;
  body: string;
  fields: AlertField[] | null;
  sourceId: string;
  firstEventId: string;
  lastEventId: string;
  sample?: unknown;
  sampleRaw?: string;
  sampleFormat?: string;
  count: number;
  firstSeen: string;
  lastSeen: string;
  ackedBy?: string;
  ackedAt?: string;
  resolvedBy?: string;
  resolvedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Notification {
  id: string;
  alertId: string;
  recipient: string;
  channel: string;
  address: string;
  subject: string;
  bodyText: string;
  status: "pending" | "sending" | "sent" | "retry" | "dead";
  attempts: number;
  nextAttemptAt: string;
  lastError?: string;
  eventReceivedAt: string;
  createdAt: string;
  sentAt?: string;
}

export interface SchemaNotice {
  id: string;
  name: string;
  source: string;
  format: string;
  fields: number;
  detected?: string[];
}

export interface Update {
  kind: "created" | "count" | "status" | "schema";
  team: string;
  alert?: Alert;
  id?: string;
  count?: number;
  lastSeen?: string;
  receivedAt?: string;
  schema?: SchemaNotice;
}

export interface Team {
  id: string;
  name: string;
  members: string[];
}

export interface Person {
  id: string;
  name: string;
  email?: string;
  phone?: string;
  timezone?: string;
}

export interface Stats {
  openAlerts: number;
  ackedAlerts: number;
  pendingDeliveries: number;
  failedDeliveries: number;
  eventsLastHour: number;
}

export interface Rule {
  id: string;
  name?: string;
  description?: string;
  team: string;
  enabled?: boolean;
  match: unknown;
  routing?: { to: string; channels: string[] }[];
  presentation?: { title?: string; body?: string; fields?: string[]; severity?: string; color?: string };
  dedup?: { key?: string; window?: string };
  runbooks?: string[];
  origin?: string;
  overrides?: string;
}

export interface RuleView extends Rule {
  fields: string[];
  enabled: boolean;
}

export interface RuleDetail {
  rule: Rule;
  yaml: string;
  fields: string[];
  version?: number;
  updatedBy?: string;
  updatedAt?: string;
}

export interface TestResult {
  valid: boolean;
  error?: string;
  matches?: boolean;
  fields?: Record<string, unknown>;
  format?: string;
  payload?: unknown;
  alert?: { title: string; body: string; severity: string; color: string; fields: AlertField[] | null };
  fingerprint?: string;
}

export interface FieldInfo {
  path: string;
  type: string;
  example?: string;
  detected?: string;
  sensitive?: boolean;
}

export interface Schema {
  id: string;
  name: string;
  sources: string[];
  format: string;
  fields: FieldInfo[] | null;
  autoFacets: Record<string, string> | null;
  manualFacets: Record<string, string> | null;
  sample?: unknown;
  sampleRaw?: string;
  count: number;
  firstSeen: string;
  lastSeen: string;
}

export interface SchemaDetail extends Schema {
  facets: Record<string, string>;
  facetValues: Record<string, unknown>;
}

export interface SourceStatus {
  id: string;
  type: "http" | "kafka" | "file";
  description?: string;
  parser?: string;
  pattern?: string;
  topics?: string[];
  path?: string;
  running: boolean;
  owner?: boolean;
  events: number;
  lastSeen?: string;
  error?: string;
}

export interface Info {
  version: string;
  instance: string;
  roles: string[];
  facets: string[];
  ops: string[];
  configErrors: string[];
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}

export function getActor(): string {
  try {
    return localStorage.getItem("kardan.actor")?.trim() || "anonymous";
  } catch {
    return "anonymous";
  }
}

async function request<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
  const isText = typeof body === "string";
  const res = await fetch(path, {
    method,
    headers: {
      "Content-Type": isText ? "application/yaml" : "application/json",
      "X-User": getActor(),
      ...headers,
    },
    body: body === undefined ? undefined : isText ? (body as string) : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(msg, res.status);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

const qs = (params: Record<string, string>) => {
  const p = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== ""));
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const api = {
  info: () => request<Info>("GET", "/api/info"),
  alerts: (team: string, status: string) => request<Alert[]>("GET", `/api/alerts${qs({ team, status })}`),
  alert: (id: string) => request<{ alert: Alert; notifications: Notification[] }>("GET", `/api/alerts/${id}`),
  ack: (id: string, by: string) => request<Alert>("POST", `/api/alerts/${id}/ack`, { by }),
  resolve: (id: string, by: string) => request<Alert>("POST", `/api/alerts/${id}/resolve`, { by }),
  teams: () => request<{ teams: Team[]; people: Person[] }>("GET", "/api/teams"),
  stats: (team: string) => request<Stats>("GET", `/api/stats${qs({ team })}`),
  streamURL: (team: string) => `/api/stream${qs({ team })}`,

  rules: () => request<{ rules: RuleView[]; errors: string[]; loadedAt: string }>("GET", "/api/rules"),
  rule: (id: string) => request<RuleDetail>("GET", `/api/rules/${encodeURIComponent(id)}`),
  createRule: (yaml: string) => request<RuleDetail>("POST", "/api/rules", yaml),
  updateRule: (id: string, yaml: string, version?: number) =>
    request<RuleDetail>("PUT", `/api/rules/${encodeURIComponent(id)}`, yaml, version ? { "If-Match": String(version) } : {}),
  deleteRule: (id: string) => request<void>("DELETE", `/api/rules/${encodeURIComponent(id)}`),
  testRule: (ruleYaml: string, sample: string) => {
    const trimmed = sample.trim();
    let event: unknown;
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        event = JSON.parse(trimmed);
      } catch {
        /* send as raw text */
      }
    }
    return request<TestResult>("POST", "/api/rules/test", event !== undefined ? { ruleYaml, event } : { ruleYaml, raw: sample });
  },

  schemas: () => request<Schema[]>("GET", "/api/schemas"),
  schema: (id: string) => request<SchemaDetail>("GET", `/api/schemas/${id}`),
  updateSchema: (id: string, name: string, facets: Record<string, string>) =>
    request<SchemaDetail>("PUT", `/api/schemas/${id}`, { name, facets }),

  sources: () => request<SourceStatus[]>("GET", "/api/sources"),
};
