export type Status = "open" | "acked" | "resolved";

export interface AlertField {
  label: string;
  value: string;
  sensitive?: boolean;
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
  notionalUsd?: number;
  impact: number;
  incidentId?: string;
  windowStart?: string;
  windowEnd?: string;
  /** The payment / settlement cut-off this alert races against. */
  cutoffName?: string;
  cutoffAt?: string;
  escalationLevel: number;
  escalatedAt?: string;
  /** Values computed when the rule fired: $count, $sum, $deadline, $lastSeen... */
  vars?: Record<string, string | number>;
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
  fallbackFor?: string;
  reason?: string;
  recipient: string;
  channel: string;
  address: string;
  subject: string;
  bodyText: string;
  status: "pending" | "sending" | "sent" | "retry" | "dead" | "standby" | "cancelled" | "held" | "suppressed";
  kind?: "alert" | "digest" | "resolved";
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

export interface Preference {
  severity?: string;
  channels: string[];
  between?: string;
  days?: string;
}

export interface OnCallConfig {
  timezone?: string;
  rotation?: { every: string; start: string; people: string[] };
  shifts?: { person: string; between?: string; days?: string; timezone?: string }[];
  overrides?: { person: string; from: string; to: string }[];
}

export interface Incident {
  id: string;
  key: string;
  keyField: string;
  keyValue: string;
  status: "open" | "resolved";
  alertCount: number;
  openAlerts: number;
  teams: string[];
  maxSeverity: string;
  notionalUsd: number;
  impact: number;
  firstSeen: string;
  lastSeen: string;
  resolvedAt?: string;
}

export interface Team {
  id: string;
  name: string;
  members: string[];
  preferences?: Preference[];
  onCall?: OnCallConfig;
}

export interface Person {
  id: string;
  name: string;
  email?: string;
  phone?: string;
  timezone?: string;
  preferences?: Preference[];
}

export interface OnCallNow {
  team: string;
  person: Person;
  reason: string;
}

export interface ClockState {
  now: string;
  offsetMinutes: number;
  demoMode: boolean;
}

export interface Delivery extends Notification {
  alertTitle: string;
  alertTeam: string;
  alertSeverity: string;
}

export interface ChannelView {
  channel: string;
  provider: string;
  breaker: "closed" | "half-open" | "open";
  sent: number;
  queued: number;
  retrying: number;
  dead: number;
  p50Ms: number;
  p99Ms: number;
  lastError?: string;
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
  kind?: "event" | "aggregate" | "absence";
  aggregate?: { by?: string[]; window: string; sum?: string; threshold: number };
  absence?: { by?: string; expect?: string[]; deadline?: string; every?: string; calendar?: string };
  during?: { calendar: string; otherwise: string };
  cutoff?: string;
  escalation?: { after: string; to: string; channels: string[] }[];
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
  notionalUsd?: number;
  impact?: number;
  incidentKey?: string;
  window?: { start: string; end: string };
  kind?: string;
  timeNote?: string;
  during?: { calendar: string; open: boolean; otherwise: string };
  cutoff?: { name: string; at: string; minutesLeft: number; urgency: number };
}

export interface Deadline {
  calendar: string;
  name: string;
  at: string;
}

export interface CalendarStatus {
  id: string;
  name: string;
  timezone: string;
  localTime: string;
  businessDay: boolean;
  open: boolean;
  hours?: string;
  holiday: boolean;
  cutoffs: Deadline[];
}

/** Which kind of rule produced an alert (absence alerts are synthetic). */
export function alertKind(a: Alert): "event" | "aggregate" | "absence" {
  if (a.sourceId === "kardan.absence") return "absence";
  if (a.vars && "$window" in a.vars) return "aggregate";
  return "event";
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
  gatewayUrl?: string;
  mailUrl?: string;
  demoMode?: boolean;
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
  alerts: (team: string, status: string, sort = "", incident = "") =>
    request<Alert[]>("GET", `/api/alerts${qs({ team, status, sort, incident })}`),
  incidents: (status = "open") => request<Incident[]>("GET", `/api/incidents${qs({ status })}`),
  alert: (id: string) => request<{ alert: Alert; notifications: Notification[] }>("GET", `/api/alerts/${id}`),
  ack: (id: string, by: string) => request<Alert>("POST", `/api/alerts/${id}/ack`, { by }),
  resolve: (id: string, by: string) => request<Alert>("POST", `/api/alerts/${id}/resolve`, { by }),
  teams: () => request<{ teams: Team[]; people: Person[]; defaultPreferences: Preference[] | null }>("GET", "/api/teams"),
  onCall: () => request<{ now: string; onCall: OnCallNow[] }>("GET", "/api/oncall"),
  clock: () => request<ClockState>("GET", "/api/clock"),
  setClock: (offsetMinutes: number) => request<ClockState>("PUT", "/api/clock", { offsetMinutes }),
  deliveries: (status: string, channel: string) => request<Delivery[]>("GET", `/api/deliveries${qs({ status, channel })}`),
  resend: (id: string) => request<void>("POST", `/api/notifications/${id}/resend`),
  channels: () => request<ChannelView[]>("GET", "/api/channels"),
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
  calendars: () => request<{ now: string; note?: string; calendars: CalendarStatus[] }>("GET", "/api/calendars"),
};
