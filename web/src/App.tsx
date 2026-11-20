import { useCallback, useEffect, useState } from "react";
import { api, type ClockState, type SchemaNotice, type Team } from "./api";
import { AlertsPage } from "./pages/AlertsPage";
import { DeliveriesPage } from "./pages/DeliveriesPage";
import { TeamsPage } from "./pages/TeamsPage";
import { RuleEditor } from "./pages/RuleEditor";
import { RulesPage } from "./pages/RulesPage";
import { SchemaDetail } from "./pages/SchemaDetail";
import { SchemasPage } from "./pages/SchemasPage";
import { SourcesPage } from "./pages/SourcesPage";
import { Link, navigate, useLocation } from "./router";
import { FinanceChip } from "./ui";
import { useLiveAlerts, type Connection, type SortOrder, type StatusFilter } from "./useLiveAlerts";

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

const NAV = [
  { to: "/", label: "Alerts", match: (p: string) => p === "/" || p.startsWith("/alerts") },
  { to: "/rules", label: "Rules", match: (p: string) => p.startsWith("/rules") },
  { to: "/schemas", label: "Schemas", match: (p: string) => p.startsWith("/schemas") },
  { to: "/sources", label: "Sources", match: (p: string) => p.startsWith("/sources") },
  { to: "/deliveries", label: "Deliveries", match: (p: string) => p.startsWith("/deliveries") },
  { to: "/teams", label: "Teams", match: (p: string) => p.startsWith("/teams") },
];

export default function App() {
  const { path } = useLocation();
  const [team, setTeam] = useState(() => store.get("kardan.team", ""));
  const [filter, setFilter] = useState<StatusFilter>("active");
  const [sort, setSort] = useState<SortOrder>(() => (store.get("kardan.sort", "impact") as SortOrder) || "impact");
  // ?incident=<id> deep-links straight to one incident's alerts.
  const [incident, setIncidentState] = useState(() => new URLSearchParams(window.location.search).get("incident") ?? "");
  const setIncident = useCallback((id: string) => {
    setIncidentState(id);
    const url = new URL(window.location.href);
    if (id) url.searchParams.set("incident", id);
    else url.searchParams.delete("incident");
    window.history.replaceState(null, "", url);
  }, []);
  const [actor, setActor] = useState(() => store.get("kardan.actor", ""));
  const [teams, setTeams] = useState<Team[]>([]);
  const [now, setNow] = useState(Date.now());
  const [toast, setToast] = useState<string | null>(null);
  const [schemaNotices, setSchemaNotices] = useState<SchemaNotice[]>([]);
  const [clock, setClock] = useState<ClockState | null>(null);
  const loadClock = useCallback(() => {
    api.clock().then(setClock).catch(() => {});
  }, []);
  useEffect(() => {
    loadClock();
    const t = window.setInterval(loadClock, 15_000);
    return () => window.clearInterval(t);
  }, [loadClock]);

  const onSchema = useCallback((s: SchemaNotice) => {
    setSchemaNotices((prev) => [s, ...prev.filter((p) => p.id !== s.id)].slice(0, 3));
    window.setTimeout(() => setSchemaNotices((prev) => prev.filter((p) => p.id !== s.id)), 12_000);
  }, []);

  // The live feed stays connected on every page, so alert counts and
  // "new schema" notices arrive wherever the user is.
  const { alerts, connection, error, flash, replace } = useLiveAlerts(team, { filter, sort, incident }, onSchema);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 4000);
  }, []);

  useEffect(() => {
    api.teams().then((d) => setTeams(d.teams)).catch(() => setTeams([]));
  }, []);
  useEffect(() => store.set("kardan.team", team), [team]);
  useEffect(() => store.set("kardan.actor", actor), [actor]);
  useEffect(() => store.set("kardan.sort", sort), [sort]);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(t);
  }, []);

  const alertId = path.match(/^\/alerts\/([A-Z0-9]+)/i)?.[1] ?? null;
  const ruleMatch = path.match(/^\/rules\/(.+)$/);
  const schemaMatch = path.match(/^\/schemas\/([0-9a-f]+)$/);

  let page;
  if (path === "/rules") page = <RulesPage />;
  else if (path === "/rules/new") page = <RuleEditor id={null} teams={teams} />;
  else if (ruleMatch) page = <RuleEditor id={decodeURIComponent(ruleMatch[1])} teams={teams} />;
  else if (path === "/schemas") page = <SchemasPage now={now} />;
  else if (schemaMatch) page = <SchemaDetail id={schemaMatch[1]} teams={teams} now={now} />;
  else if (path === "/sources") page = <SourcesPage now={now} />;
  else if (path === "/deliveries") page = <DeliveriesPage now={now} onToast={showToast} />;
  else if (path === "/teams") page = <TeamsPage onClockChange={loadClock} />;
  else if (path !== "/" && !alertId)
    page = (
      <div className="py-16 text-center">
        <div className="text-4xl">🧭</div>
        <h1 className="mt-2 text-lg font-semibold">Page not found</h1>
        <p className="mt-1 text-sm text-gray-500">
          Nothing lives at <code className="font-mono">{path}</code>.{" "}
          <Link to="/" className="text-amber-600 hover:underline">
            Back to alerts
          </Link>
        </p>
      </div>
    );
  else
    page = (
      <AlertsPage
        alerts={alerts}
        error={error}
        flash={flash}
        replace={replace}
        team={team}
        filter={filter}
        setFilter={setFilter}
        sort={sort}
        setSort={setSort}
        incident={incident}
        setIncident={setIncident}
        actor={actor}
        now={now}
        openId={alertId}
        onToast={showToast}
      />
    );

  const onAlerts = NAV[0].match(path);

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 dark:bg-gray-950 dark:text-gray-100">
      <header className="sticky top-0 z-30 border-b border-gray-200 bg-white/90 backdrop-blur dark:border-gray-800 dark:bg-gray-950/90">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5">
          <Link to="/" className="flex items-center gap-2 font-semibold">
            <span className="grid h-7 w-7 place-items-center rounded-md bg-gray-900 text-amber-400 dark:bg-white dark:text-amber-500">K</span>
            Kardan
          </Link>
          <nav className="flex gap-1 text-sm">
            {NAV.map((n) => (
              <Link
                key={n.to}
                to={n.to}
                className={`rounded-md px-2.5 py-1.5 font-medium ${
                  n.match(path)
                    ? "bg-gray-100 text-gray-900 dark:bg-gray-800 dark:text-white"
                    : "text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white"
                }`}
              >
                {n.label}
              </Link>
            ))}
          </nav>
          {onAlerts && (
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
          )}
          <LiveIndicator connection={connection} />
          {clock && clock.offsetMinutes !== 0 && (
            <Link
              to="/teams"
              className="rounded-md bg-amber-100 px-2 py-1 text-xs font-medium text-amber-900 dark:bg-amber-500/20 dark:text-amber-200"
              title="The business clock is shifted for the demo"
            >
              ⏱ simulated {new Date(clock.now).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })}
            </Link>
          )}
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

      <main className="mx-auto max-w-6xl px-4 py-5">{page}</main>

      <div className="fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2">
        {schemaNotices.map((s) => (
          <button
            key={s.id}
            onClick={() => {
              setSchemaNotices((prev) => prev.filter((p) => p.id !== s.id));
              navigate(`/schemas/${s.id}`);
            }}
            className="rounded-lg bg-white p-3 text-left shadow-lg ring-1 ring-gray-200 hover:ring-amber-400 dark:bg-gray-900 dark:ring-gray-700"
          >
            <div className="text-xs font-medium tracking-wide text-amber-600 uppercase dark:text-amber-400">New schema detected</div>
            <div className="mt-0.5 font-medium">{s.name}</div>
            <div className="text-xs text-gray-500">
              {s.format} from {s.source} · {s.fields} fields
            </div>
            {s.detected && s.detected.length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {s.detected.map((d) => (
                  <FinanceChip key={d} kind={d} />
                ))}
              </div>
            )}
            <div className="mt-1.5 text-xs text-gray-500">Click to review fields and create a rule →</div>
          </button>
        ))}
        {toast && <div className="rounded-md bg-gray-900 px-4 py-2 text-sm text-white shadow-lg">{toast}</div>}
      </div>
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
