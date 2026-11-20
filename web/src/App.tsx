import { BellRing, Cable, Clock3, Database, Radio, Send, SlidersHorizontal, Users, UserRound } from "lucide-react";
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

const NAV = [
  { to: "/", label: "Alerts", icon: BellRing, match: (p: string) => p === "/" || p.startsWith("/alerts") },
  { to: "/rules", label: "Rules", icon: SlidersHorizontal, match: (p: string) => p.startsWith("/rules") },
  { to: "/schemas", label: "Schemas", icon: Database, match: (p: string) => p.startsWith("/schemas") },
  { to: "/sources", label: "Sources", icon: Cable, match: (p: string) => p.startsWith("/sources") },
  { to: "/deliveries", label: "Deliveries", icon: Send, match: (p: string) => p.startsWith("/deliveries") },
  { to: "/teams", label: "Teams", icon: Users, match: (p: string) => p.startsWith("/teams") },
];

export default function App() {
  const { path } = useLocation();
  const [team, setTeam] = useState(() => store.get("kardan.team", ""));
  const [filter, setFilter] = useState<StatusFilter>("active");
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
  const { alerts, connection, error, loading, flash, replace, reload } = useLiveAlerts(team, filter, onSchema);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 4000);
  }, []);

  useEffect(() => {
    api.teams().then((d) => setTeams(d.teams)).catch(() => setTeams([]));
  }, []);
  useEffect(() => store.set("kardan.team", team), [team]);
  useEffect(() => store.set("kardan.actor", actor), [actor]);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(t);
  }, []);

  const alertId = path.match(/^\/alerts\/([A-Z0-9]+)/i)?.[1] ?? null;
  const ruleMatch = path.match(/^\/rules\/(.+)$/);
  const schemaMatch = path.match(/^\/schemas\/([0-9a-f]+)$/);

  let page;
  if (path === "/rules") page = <RulesPage />;
  else if (path === "/rules/new") page = <RuleEditor key="new" id={null} teams={teams} />;
  else if (ruleMatch) page = <RuleEditor key={ruleMatch[1]} id={decodeURIComponent(ruleMatch[1])} teams={teams} />;
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
        loading={loading}
        connection={connection}
        onRetry={reload}
        flash={flash}
        replace={replace}
        team={team}
        teams={teams}
        onTeamChange={setTeam}
        filter={filter}
        setFilter={setFilter}
        actor={actor}
        now={now}
        openId={alertId}
        onToast={showToast}
      />
    );

  const current = NAV.find((item) => item.match(path));

  return (
    <div className="console-layout">
      <a className="skip-link" href="#main-content">Skip to content</a>
      <aside className="console-sidebar">
        <Link to="/" className="console-brand">
          <span className="console-mark"><Radio size={19} aria-hidden="true" /></span>
          Kardan
        </Link>
        <nav aria-label="Workspace" className="console-nav">
          {NAV.map(({ to, label, icon: Icon, match }) => (
            <Link key={to} to={to} aria-current={match(path) ? "page" : undefined} className="console-nav-item">
              <Icon size={17} aria-hidden="true" />
              {label}
            </Link>
          ))}
        </nav>
      </aside>

      <div className="console-workspace">
        <header className="console-topbar">
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <span className="font-medium">{current?.label ?? "Workspace"}</span>
            <LiveIndicator connection={connection} />
            {clock && clock.offsetMinutes !== 0 && (
              <Link to="/teams" className="inline-flex items-center gap-1 rounded-md bg-amber-100 px-2 py-1 text-xs font-medium text-amber-900 dark:bg-amber-500/20 dark:text-amber-200">
                <Clock3 size={13} aria-hidden="true" />
                Simulated {new Date(clock.now).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })}
              </Link>
            )}
          </div>
          <label className="console-actor">
            <UserRound size={15} aria-hidden="true" />
            <span className="hidden sm:inline">Acting as</span>
            <input
              aria-label="Acting as"
              value={actor}
              onChange={(e) => setActor(e.target.value)}
              placeholder="Your name"
            />
          </label>
        </header>
        <main id="main-content" tabIndex={-1} className="console-main">{page}</main>
      </div>

      <div className="pointer-events-none fixed right-4 bottom-4 z-50 flex w-[min(20rem,calc(100%-2rem))] flex-col gap-2">
        {schemaNotices.map((s) => (
          <button
            key={s.id}
            onClick={() => {
              setSchemaNotices((prev) => prev.filter((p) => p.id !== s.id));
              navigate(`/schemas/${s.id}`);
            }}
            className="pointer-events-auto rounded-lg bg-white p-3 text-left shadow-lg ring-1 ring-gray-200 hover:ring-amber-400 dark:bg-gray-900 dark:ring-gray-700"
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
        {toast && <div role="status" className="rounded-md bg-gray-900 px-4 py-2 text-sm text-white shadow-lg">{toast}</div>}
      </div>
    </div>
  );
}

function LiveIndicator({ connection }: { connection: Connection }) {
  const map = {
    live: ["bg-emerald-500", "Live"],
    connecting: ["bg-amber-500", "Connecting…"],
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
