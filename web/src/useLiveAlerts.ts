import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Alert, type SchemaNotice, type Update } from "./api";

export type Connection = "connecting" | "live" | "offline";
export type StatusFilter = "active" | "resolved" | "";
export type SortOrder = "recent" | "impact" | "money";

export interface FeedOptions {
  filter: StatusFilter;
  sort: SortOrder;
  incident: string;
}

const matchesFilter = (a: Alert, filter: StatusFilter) =>
  filter === "" || (filter === "active" ? a.status !== "resolved" : a.status === filter);

const byLastSeen = (a: Alert, b: Alert) => b.lastSeen.localeCompare(a.lastSeen);
const comparators: Record<SortOrder, (a: Alert, b: Alert) => number> = {
  recent: byLastSeen,
  impact: (a, b) => b.impact - a.impact || byLastSeen(a, b),
  money: (a, b) => (b.notionalUsd ?? -1) - (a.notionalUsd ?? -1) || b.impact - a.impact || byLastSeen(a, b),
};

/**
 * Loads alerts over REST, then keeps them current with the server-sent event
 * stream (push, no page refresh). Every (re)connect re-syncs from REST, so a
 * dropped connection never leaves the feed stale.
 */
export function useLiveAlerts(team: string, opts: FeedOptions, onSchema?: (s: SchemaNotice) => void) {
  const { filter, sort, incident } = opts;
  const cmp = comparators[sort];
  const keep = useCallback(
    (a: Alert) => (!team || a.team === team) && matchesFilter(a, filter) && (!incident || a.incidentId === incident),
    [team, filter, incident],
  );
  const onSchemaRef = useRef(onSchema);
  onSchemaRef.current = onSchema;
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [connection, setConnection] = useState<Connection>("connecting");
  const [loading, setLoading] = useState(true);
  const request = useRef(0);
  const [error, setError] = useState<string | null>(null);
  const [flash, setFlash] = useState<Set<string>>(new Set());
  const flashTimers = useRef(new Map<string, number>());

  const markFlash = useCallback((id: string) => {
    setFlash((prev) => new Set(prev).add(id));
    const timers = flashTimers.current;
    window.clearTimeout(timers.get(id));
    timers.set(
      id,
      window.setTimeout(() => {
        setFlash((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
        timers.delete(id);
      }, 1600),
    );
  }, []);

  const reload = useCallback(async () => {
    const id = ++request.current;
    try {
      const list = await api.alerts(team, filter, sort, incident);
      if (id !== request.current) return;
      setAlerts(list);
      setError(null);
    } catch (e) {
      if (id === request.current) setError((e as Error).message);
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [team, filter, sort, incident]);

  const apply = useCallback(
    (u: Update) => {
      if (u.kind === "schema") {
        if (u.schema) onSchemaRef.current?.(u.schema);
        return;
      }
      setAlerts((prev) => {
        if (u.kind === "count" && u.id) {
          const idx = prev.findIndex((a) => a.id === u.id);
          if (idx < 0) return prev;
          const next = [...prev];
          next[idx] = { ...next[idx], count: u.count ?? next[idx].count, lastSeen: u.lastSeen ?? next[idx].lastSeen };
          return next.sort(cmp);
        }
        if (u.alert) {
          const rest = prev.filter((a) => a.id !== u.alert!.id);
          if (!keep(u.alert)) return rest;
          return [u.alert, ...rest].sort(cmp);
        }
        return prev;
      });
      const id = u.alert?.id ?? u.id;
      if (id) markFlash(id);
    },
    [keep, cmp, markFlash],
  );

  useEffect(() => {
    setConnection("connecting");
    setLoading(true);
    setAlerts([]);
    setError(null);
    void reload();
    const es = new EventSource(api.streamURL(team));
    es.addEventListener("ready", () => {
      setConnection("live");
      void reload();
    });
    es.addEventListener("alert", (ev) => apply(JSON.parse((ev as MessageEvent<string>).data) as Update));
    es.onerror = () => setConnection(es.readyState === EventSource.CLOSED ? "offline" : "connecting");
    return () => {
      es.close();
      request.current++;
    };
  }, [team, reload, apply]);

  // Apply optimistic results of ack/resolve immediately.
  const replace = useCallback((a: Alert) => apply({ kind: "status", team: a.team, alert: a }), [apply]);

  return { alerts, connection, error, loading, flash, reload, replace };
}
