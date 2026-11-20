import { useCallback, useEffect, useMemo, useState } from "react";
import { api, type Alert, type Stats } from "../api";
import { AlertCard } from "../AlertCard";
import { AlertDrawer } from "../AlertDrawer";
import { navigate } from "../router";
import type { StatusFilter } from "../useLiveAlerts";

interface Props {
  alerts: Alert[];
  error: string | null;
  flash: Set<string>;
  replace: (a: Alert) => void;
  team: string;
  filter: StatusFilter;
  setFilter: (f: StatusFilter) => void;
  actor: string;
  now: number;
  openId: string | null;
  onToast: (msg: string) => void;
}

export function AlertsPage({ alerts, error, flash, replace, team, filter, setFilter, actor, now, openId, onToast }: Props) {
  const [stats, setStats] = useState<Stats | null>(null);
  const [busy, setBusy] = useState<Set<string>>(new Set());

  useEffect(() => {
    let cancelled = false;
    const load = () => api.stats(team).then((s) => !cancelled && setStats(s)).catch(() => {});
    void load();
    const t = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(t);
    };
  }, [team, alerts.length]);

  const act = useCallback(
    async (a: Alert, action: "ack" | "resolve") => {
      setBusy((b) => new Set(b).add(a.id));
      try {
        const by = actor.trim() || "anonymous";
        replace(action === "ack" ? await api.ack(a.id, by) : await api.resolve(a.id, by));
      } catch (e) {
        onToast(`Could not ${action}: ${(e as Error).message}`);
      } finally {
        setBusy((b) => {
          const n = new Set(b);
          n.delete(a.id);
          return n;
        });
      }
    },
    [actor, replace, onToast],
  );

  const openAlert = useMemo(() => alerts.find((a) => a.id === openId), [alerts, openId]);

  return (
    <>
      {stats && (
        <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="Open" value={stats.openAlerts} emphasis={stats.openAlerts > 0} />
          <Stat label="Acknowledged" value={stats.ackedAlerts} />
          <Stat label="Matching events (1h)" value={stats.eventsLastHour} />
          <Stat
            label="Deliveries pending / failed"
            value={`${stats.pendingDeliveries} / ${stats.failedDeliveries}`}
            emphasis={stats.failedDeliveries > 0}
          />
        </div>
      )}

      <div className="mb-3 flex items-center gap-1 text-sm">
        {(
          [
            ["active", "Active"],
            ["resolved", "Resolved"],
            ["", "All"],
          ] as const
        ).map(([v, label]) => (
          <button
            key={label}
            onClick={() => setFilter(v)}
            className={`rounded-md px-3 py-1.5 font-medium ${
              filter === v
                ? "bg-gray-900 text-white dark:bg-white dark:text-gray-900"
                : "text-gray-600 hover:bg-gray-200/60 dark:text-gray-300 dark:hover:bg-gray-800"
            }`}
          >
            {label}
          </button>
        ))}
        <span className="ml-auto text-xs text-gray-500">{alerts.length} shown</span>
      </div>

      {error && <div className="mb-3 rounded-md bg-red-50 p-3 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300">{error}</div>}

      {alerts.length === 0 ? (
        <EmptyState filter={filter} />
      ) : (
        <ul className="flex flex-col gap-2.5">
          {alerts.map((a) => (
            <AlertCard
              key={a.id}
              alert={a}
              now={now}
              flash={flash.has(a.id)}
              busy={busy.has(a.id)}
              onOpen={() => navigate(`/alerts/${a.id}`)}
              onAck={() => void act(a, "ack")}
              onResolve={() => void act(a, "resolve")}
            />
          ))}
        </ul>
      )}

      {openId && (
        <AlertDrawer
          id={openId}
          live={openAlert}
          now={now}
          onClose={() => navigate("/")}
          onAck={() => openAlert && void act(openAlert, "ack")}
          onResolve={() => openAlert && void act(openAlert, "resolve")}
        />
      )}
    </>
  );
}

function Stat({ label, value, emphasis }: { label: string; value: number | string; emphasis?: boolean }) {
  return (
    <div className="rounded-lg bg-white p-3 ring-1 ring-gray-200 dark:bg-gray-900 dark:ring-gray-800">
      <div className="text-xs text-gray-500 dark:text-gray-400">{label}</div>
      <div className={`mt-0.5 text-xl font-semibold tabular-nums ${emphasis ? "text-red-600 dark:text-red-400" : ""}`}>
        {typeof value === "number" ? value.toLocaleString() : value}
      </div>
    </div>
  );
}

function EmptyState({ filter }: { filter: StatusFilter }) {
  return (
    <div className="rounded-lg border border-dashed border-gray-300 bg-white p-10 text-center dark:border-gray-700 dark:bg-gray-900">
      <div className="text-3xl">✓</div>
      <h3 className="mt-2 font-semibold">{filter === "resolved" ? "Nothing resolved yet" : "All quiet"}</h3>
      <p className="mx-auto mt-1 max-w-md text-sm text-gray-500 dark:text-gray-400">
        New alerts appear here instantly. Try sending the sample events:
      </p>
      <code className="mt-3 inline-block rounded bg-gray-100 px-2 py-1 font-mono text-xs dark:bg-gray-800">./samples/send.sh</code>
    </div>
  );
}
