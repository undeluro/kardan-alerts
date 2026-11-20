import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Alert, type SchemaNotice, type Update } from "./api";

export type Connection = "connecting" | "live" | "offline";
export type StatusFilter = "active" | "resolved" | "";

const matchesFilter = (a: Alert, filter: StatusFilter) =>
  filter === "" || (filter === "active" ? a.status !== "resolved" : a.status === filter);

const byLastSeen = (a: Alert, b: Alert) => b.lastSeen.localeCompare(a.lastSeen);

/**
 * Loads alerts over REST, then keeps them current with the server-sent event
 * stream (push, no page refresh). Every (re)connect re-syncs from REST, so a
 * dropped connection never leaves the feed stale.
 */
export function useLiveAlerts(team: string, filter: StatusFilter, onSchema?: (s: SchemaNotice) => void) {
  const onSchemaRef = useRef(onSchema);
  onSchemaRef.current = onSchema;
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [connection, setConnection] = useState<Connection>("connecting");
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
    try {
      const list = await api.alerts(team, filter);
      setAlerts(list);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }, [team, filter]);

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
          return next.sort(byLastSeen);
        }
        if (u.alert) {
          const rest = prev.filter((a) => a.id !== u.alert!.id);
          if (!matchesFilter(u.alert, filter)) return rest;
          return [u.alert, ...rest].sort(byLastSeen);
        }
        return prev;
      });
      const id = u.alert?.id ?? u.id;
      if (id) markFlash(id);
    },
    [filter, markFlash],
  );

  useEffect(() => {
    setConnection("connecting");
    const es = new EventSource(api.streamURL(team));
    es.addEventListener("ready", () => {
      setConnection("live");
      void reload();
    });
    es.addEventListener("alert", (ev) => apply(JSON.parse((ev as MessageEvent<string>).data) as Update));
    es.onerror = () => setConnection(es.readyState === EventSource.CLOSED ? "offline" : "connecting");
    return () => es.close();
  }, [team, reload, apply]);

  // Apply optimistic results of ack/resolve immediately.
  const replace = useCallback((a: Alert) => apply({ kind: "status", team: a.team, alert: a }), [apply]);

  return { alerts, connection, error, flash, reload, replace };
}
