import { ArrowRight, BellRing, BookOpenCheck, Cable, CalendarClock, Clock3, Compass, Database, Moon, Radio, ScrollText, Send, SlidersHorizontal, Sun, Users, UserRound } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, type ClockState, type SchemaNotice, type Team } from "./api";
import { AlertsPage } from "./pages/AlertsPage";
import { DeliveriesPage } from "./pages/DeliveriesPage";
import { TeamsPage } from "./pages/TeamsPage";
import { MarketsPage } from "./pages/MarketsPage";
import { RunbooksPage } from "./pages/RunbooksPage";
import { RunbookEditor } from "./pages/RunbookEditor";
import { AuditPage } from "./pages/AuditPage";
import { IncidentPage } from "./pages/IncidentPage";
import { OnboardingPage } from "./pages/OnboardingPage";
import { setBusinessClock } from "./clock";
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
  { to: "/", label: "Alerts", icon: BellRing, match: (p: string) => p === "/" || p.startsWith("/alerts") || p.startsWith("/incidents") },
  { to: "/rules", label: "Rules", icon: SlidersHorizontal, match: (p: string) => p.startsWith("/rules") },
  { to: "/schemas", label: "Schemas", icon: Database, match: (p: string) => p.startsWith("/schemas") },
  { to: "/sources", label: "Sources", icon: Cable, match: (p: string) => p.startsWith("/sources") },
  { to: "/runbooks", label: "Runbooks", icon: BookOpenCheck, match: (p: string) => p.startsWith("/runbooks") },
  { to: "/markets", label: "Markets", icon: CalendarClock, match: (p: string) => p.startsWith("/markets") },
  { to: "/deliveries", label: "Deliveries", icon: Send, match: (p: string) => p.startsWith("/deliveries") },
  { to: "/teams", label: "Teams", icon: Users, match: (p: string) => p.startsWith("/teams") || p.startsWith("/onboarding") },
  { to: "/audit", label: "Audit", icon: ScrollText, match: (p: string) => p.startsWith("/audit") },
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
  const [theme, setTheme] = useState(() => store.get("kardan.theme", "dark") === "light" ? "light" : "dark");
  const [actor, setActor] = useState(() => store.get("kardan.actor", ""));
  const [teams, setTeams] = useState<Team[]>([]);
  const [now, setNow] = useState(Date.now());
  const [toast, setToast] = useState<string | null>(null);
  const [schemaNotices, setSchemaNotices] = useState<SchemaNotice[]>([]);
  const [clock, setClock] = useState<ClockState | null>(null);
  const loadClock = useCallback(() => {
    api
      .clock()
      .then((c) => {
        setClock(c);
        setBusinessClock(c.now);
      })
      .catch(() => {});
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
  const { alerts, connection, error, loading, flash, replace, reload } = useLiveAlerts(team, { filter, sort, incident }, onSchema);

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
    document.documentElement.dataset.theme = theme;
    store.set("kardan.theme", theme);
  }, [theme]);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(t);
  }, []);

  const alertId = path.match(/^\/alerts\/([A-Z0-9]+)/i)?.[1] ?? null;
  const ruleMatch = path.match(/^\/rules\/(.+)$/);
  const schemaMatch = path.match(/^\/schemas\/([0-9a-f]+)$/);
  const runbookMatch = path.match(/^\/runbooks\/(.+)$/);
  const incidentMatch = path.match(/^\/incidents\/(.+)$/);

  let page;
  if (path === "/rules") page = <RulesPage />;
  else if (path === "/rules/new") page = <RuleEditor key="new" id={null} teams={teams} />;
  else if (ruleMatch) page = <RuleEditor key={ruleMatch[1]} id={decodeURIComponent(ruleMatch[1])} teams={teams} />;
  else if (path === "/schemas") page = <SchemasPage now={now} />;
  else if (schemaMatch) page = <SchemaDetail id={schemaMatch[1]} teams={teams} now={now} />;
  else if (path === "/sources") page = <SourcesPage now={now} />;
  else if (path === "/deliveries") page = <DeliveriesPage now={now} onToast={showToast} />;
  else if (path === "/teams") page = <TeamsPage onClockChange={loadClock} onToast={showToast} />;
  else if (path === "/markets") page = <MarketsPage />;
  else if (path === "/runbooks") page = <RunbooksPage />;
  else if (path === "/runbooks/new") page = <RunbookEditor key="new" id={null} teams={teams} />;
  else if (runbookMatch) page = <RunbookEditor key={runbookMatch[1]} id={decodeURIComponent(runbookMatch[1])} teams={teams} />;
  else if (path === "/audit") page = <AuditPage />;
  else if (path === "/onboarding") page = <OnboardingPage onToast={showToast} />;
  else if (incidentMatch) page = <IncidentPage id={decodeURIComponent(incidentMatch[1])} now={now} />;
  else if (path !== "/" && !alertId)
    page = (
      <div className="py-16 text-center">
        <Compass size={28} aria-hidden="true" className="console-muted mx-auto" />
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
            <LiveIndicator connection={connection} />
            {clock && clock.offsetMinutes !== 0 && (
              <Link to="/teams" className="inline-flex items-center gap-1 rounded-md bg-amber-100 px-2 py-1 text-xs font-medium text-amber-900 dark:bg-amber-500/20 dark:text-amber-200">
                <Clock3 size={13} aria-hidden="true" />
                Simulated {new Date(clock.now).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })}
              </Link>
            )}
          </div>
          <div className="flex items-center gap-2">
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
            <button type="button" className="console-theme"
              aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
              title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
              {theme === "dark" ? <Sun size={15} aria-hidden="true" /> : <Moon size={15} aria-hidden="true" />}
            </button>
          </div>
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
            <div className="console-muted mt-1.5 flex items-center gap-1 text-xs">Review schema <ArrowRight size={13} aria-hidden="true" /></div>
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
    <span className="console-live flex items-center gap-1.5 text-xs font-medium" aria-live="polite">
      <span aria-hidden="true" className={`size-1.5 rounded-full ${dot}`} />
      {label}
    </span>
  );
}
