import { useCallback, useEffect, useMemo, useState } from "react";
import { api, type Alert, type Stats, type Team } from "./api";
import { AlertCard } from "./AlertCard";
import { AlertDrawer } from "./AlertDrawer";
import { useLiveAlerts, type Connection, type StatusFilter } from "./useLiveAlerts";

const store = {
  get: (k: string, d: string) => {
    try {
      return localStorage.getItem(k) ?? d;
    } catch {
      return d;
    }
  },
  set: (k: string, v: string) => {
    try {
      localStorage.setItem(k, v);
    } catch {
      /* private mode */
    }
  },
};

const alertIdFromPath = () => window.location.pathname.match(/^\/alerts\/([A-Z0-9]+)/i)?.[1] ?? null;

export default function App() {
  const [team, setTeam] = useState(() => store.get("kardan.team", ""));
  const [filter, setFilter] = useState<StatusFilter>("active");
  const [actor, setActor] = useState(() => store.get("kardan.actor", ""));
  const [teams, setTeams] = useState<Team[]>([]);
  const [stats, setStats] = useState<Stats | null>(null);
  const [openId, setOpenId] = useState<string | null>(alertIdFromPath);
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [now, setNow] = useState(Date.now());
  const [toast, setToast] = useState<string | null>(null);

  const { alerts, connection, error, flash, replace } = useLiveAlerts(team, filter);

  useEffect(() => {
    api.teams().then((d) => setTeams(d.teams)).catch(() => setTeams([]));
  }, []);
  useEffect(() => store.set("kardan.team", team), [team]);
  useEffect(() => store.set("kardan.actor", actor), [actor]);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(t);
  }, []);
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
  useEffect(() => {
    const onPop = () => setOpenId(alertIdFromPath());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const open = useCallback((id: string | null) => {
    setOpenId(id);
    window.history.pushState(null, "", id ? `/alerts/${id}` : "/");
  }, []);

  const act = useCallback(
    async (a: Alert, action: "ack" | "resolve") => {
      setBusy((b) => new Set(b).add(a.id));
      try {
        const by = actor.trim() || "anonymous";
        replace(action === "ack" ? await api.ack(a.id, by) : await api.resolve(a.id, by));
      } catch (e) {
        setToast(`Could not ${action}: ${(e as Error).message}`);
        window.setTimeout(() => setToast(null), 4000);
      } finally {
        setBusy((b) => {
          const n = new Set(b);
          n.delete(a.id);
          return n;
        });
      }
    },
    [actor, replace],
  );

  const openAlert = useMemo(() => alerts.find((a) => a.id === openId), [alerts, openId]);

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 dark:bg-gray-950 dark:text-gray-100">
      <header className="sticky top-0 z-30 border-b border-gray-200 bg-white/90 backdrop-blur dark:border-gray-800 dark:bg-gray-950/90">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-3 px-4 py-3">
          <a href="/" onClick={(e) => (e.preventDefault(), open(null))} className="flex items-center gap-2 font-semibold">
            <span className="grid h-7 w-7 place-items-center rounded-md bg-gray-900 text-amber-400 dark:bg-white dark:text-amber-500">K</span>
            Kardan
          </a>
          <select
            value={team}
            onChange={(e) => setTeam(e.target.value)}
            className="rounded-md border-0 bg-white py-1.5 pr-8 pl-2.5 text-sm ring-1 ring-gray-300 ring-inset dark:bg-gray-900 dark:ring-gray-700"
            aria-label="Team"
          >
            <option value="">All teams</option>
            {teams.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </select>
          <LiveIndicator connection={connection} />
          <div className="ml-auto flex items-center gap-2">
            <label htmlFor="actor" className="text-xs text-gray-500">
              Acting as
            </label>
            <input
              id="actor"
              value={actor}
              onChange={(e) => setActor(e.target.value)}
              placeholder="your name"
              className="w-32 rounded-md border-0 bg-white px-2.5 py-1.5 text-sm ring-1 ring-gray-300 ring-inset dark:bg-gray-900 dark:ring-gray-700"
            />
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-5">
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
                onOpen={() => open(a.id)}
                onAck={() => void act(a, "ack")}
                onResolve={() => void act(a, "resolve")}
              />
            ))}
          </ul>
        )}
      </main>

      {openId && (
        <AlertDrawer
          id={openId}
          live={openAlert}
          now={now}
          onClose={() => open(null)}
          onAck={() => openAlert && void act(openAlert, "ack")}
          onResolve={() => openAlert && void act(openAlert, "resolve")}
        />
      )}

      {toast && (
        <div className="fixed right-4 bottom-4 z-50 rounded-md bg-gray-900 px-4 py-2 text-sm text-white shadow-lg">{toast}</div>
      )}
    </div>
  );
}

function LiveIndicator({ connection }: { connection: Connection }) {
  const map = {
    live: ["bg-emerald-500", "Live"],
    connecting: ["bg-amber-500 animate-pulse", "Connecting…"],
    offline: ["bg-red-500", "Offline"],
  } as const;
  const [dot, label] = map[connection];
  return (
    <span className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400" aria-live="polite">
      <span className={`h-2 w-2 rounded-full ${dot}`} />
      {label}
    </span>
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
