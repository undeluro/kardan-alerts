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

export interface Update {
  kind: "created" | "count" | "status";
  team: string;
  alert?: Alert;
  id?: string;
  count?: number;
  lastSeen?: string;
  receivedAt?: string;
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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

const qs = (params: Record<string, string>) => {
  const p = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== ""));
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const api = {
  alerts: (team: string, status: string) => request<Alert[]>(`/api/alerts${qs({ team, status })}`),
  alert: (id: string) => request<{ alert: Alert; notifications: Notification[] }>(`/api/alerts/${id}`),
  ack: (id: string, by: string) =>
    request<Alert>(`/api/alerts/${id}/ack`, { method: "POST", body: JSON.stringify({ by }) }),
  resolve: (id: string, by: string) =>
    request<Alert>(`/api/alerts/${id}/resolve`, { method: "POST", body: JSON.stringify({ by }) }),
  teams: () => request<{ teams: Team[]; people: Person[] }>("/api/teams"),
  stats: (team: string) => request<Stats>(`/api/stats${qs({ team })}`),
  streamURL: (team: string) => `/api/stream${qs({ team })}`,
};
