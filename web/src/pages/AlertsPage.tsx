import { Activity, Bell, CheckCircle2, CircleAlert, Inbox, Search, Send, X } from "lucide-react";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { api, type Alert, type Stats, type Team } from "../api";
import { AlertCard } from "../AlertCard";
import { AlertDrawer } from "../AlertDrawer";
import { Link, navigate } from "../router";
import { Button, Notice, PageHeader, inputClass } from "../ui";
import type { Connection, StatusFilter } from "../useLiveAlerts";

interface Props {
  alerts: Alert[];
  error: string | null;
  loading: boolean;
  connection: Connection;
  onRetry: () => Promise<void>;
  flash: Set<string>;
  replace: (a: Alert) => void;
  team: string;
  teams: Team[];
  onTeamChange: (team: string) => void;
  filter: StatusFilter;
  setFilter: (f: StatusFilter) => void;
  actor: string;
  now: number;
  openId: string | null;
  onToast: (msg: string) => void;
}

const statuses = [["active", "Active"], ["resolved", "Resolved"], ["", "All alerts"]] as const;

export function AlertsPage({
  alerts, error, loading, connection, onRetry, flash, replace, team, teams, onTeamChange,
  filter, setFilter, actor, now, openId, onToast,
}: Props) {
  const [stats, setStats] = useState<{ team: string; values: Stats } | null>(null);
  const [query, setQuery] = useState("");
  const [severity, setSeverity] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = () => api.stats(team).then((values) => {
      if (!cancelled) setStats({ team, values });
    }).catch(() => {
      if (!cancelled) setStats(null);
    });
    void load();
    const timer = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [team]);

  const act = useCallback(async (alert: Alert, action: "ack" | "resolve") => {
    const by = actor.trim() || "anonymous";
    const result = action === "ack" ? await api.ack(alert.id, by) : await api.resolve(alert.id, by);
    replace(result);
    onToast(action === "ack" ? "Alert acknowledged" : "Alert resolved");
    return result;
  }, [actor, replace, onToast]);

  const totals = stats?.team === team ? stats.values : null;
  const text = query.trim().toLowerCase();
  const filtered = alerts.filter((alert) => {
    if (severity && alert.severity.toLowerCase() !== severity) return false;
    return !text || [alert.title, alert.body, alert.team, alert.sourceId, alert.ruleId].some((value) => value.toLowerCase().includes(text));
  });
  const severityOptions = new Set(["critical", "error", "warning", "info", "debug", ...alerts.map((alert) => alert.severity.toLowerCase())]);

  if (severity) severityOptions.add(severity);

  const closeAlert = () => navigate("/", true, { preserveScroll: true });

  return (
    <>
      <PageHeader title="Alerts" />
      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        <Metric icon={Bell} label="Open alerts" value={totals?.openAlerts} />
        <Metric icon={CheckCircle2} label="Acknowledged" value={totals?.ackedAlerts} />
        <Metric icon={Activity} label="Matching events · 1h" value={totals?.eventsLastHour} />
        <Metric icon={Send} label="Pending deliveries" value={totals?.pendingDeliveries}>
          {totals && totals.failedDeliveries > 0 && (
            <Link to="/deliveries" className="font-medium text-red-700 hover:underline dark:text-red-300">{totals.failedDeliveries} failed</Link>
          )}
        </Metric>
      </div>

      <section aria-labelledby="alert-queue-title" className="console-panel mt-6">
        <div className="px-4 pt-4 sm:px-5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2 id="alert-queue-title" tabIndex={-1} className="text-lg font-semibold tracking-tight">Alert queue</h2>
            {!loading && !error && <span className="console-muted text-xs tabular-nums">{filtered.length} of {alerts.length} loaded</span>}
          </div>
          <div className="mt-3 flex w-fit max-w-full flex-wrap gap-1 rounded-lg bg-[var(--console-canvas)] p-1" role="group" aria-label="Alert status">
            {statuses.map(([value, label]) => (
              <button key={label} type="button" aria-pressed={filter === value} onClick={() => setFilter(value)}
                className="console-muted min-h-8 rounded-md px-2.5 text-sm font-medium hover:text-[var(--console-ink)] aria-pressed:bg-[var(--console-surface)] aria-pressed:text-[var(--console-ink)] aria-pressed:shadow-xs">
                {label}
              </button>
            ))}
          </div>
          <div className="my-4 grid grid-cols-2 gap-2 sm:grid-cols-[minmax(10rem,1fr)_minmax(8rem,11rem)_minmax(8rem,10rem)]">
            <div className="relative col-span-2 sm:col-span-1">
              <Search className="console-muted pointer-events-none absolute top-1/2 left-3 -translate-y-1/2" size={16} aria-hidden="true" />
              <input value={query} onChange={(event) => setQuery(event.target.value)} aria-label="Search loaded alerts"
                type="search" placeholder="Search alerts…" className="h-10 w-full rounded-md border border-[var(--console-line)] pr-10 pl-9 text-sm" />
              {query && <button type="button" onClick={() => setQuery("")} aria-label="Clear search"
                className="console-muted absolute inset-y-0 right-0 grid w-10 place-items-center"><X size={16} aria-hidden="true" /></button>}
            </div>
            <select aria-label="Team" value={team} onChange={(event) => onTeamChange(event.target.value)} className={`${inputClass} h-10`}>
              <option value="">All teams</option>
              {teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
            <select aria-label="Severity" value={severity} onChange={(event) => setSeverity(event.target.value)} className={`${inputClass} h-10`}>
              <option value="">All severities</option>
              {[...severityOptions].map((value) => <option key={value} value={value}>{value.charAt(0).toUpperCase() + value.slice(1)}</option>)}
            </select>
          </div>
        </div>

        {error && (
          <div className="mx-4 mb-4 sm:mx-5">
            <Notice tone="error">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2"><CircleAlert size={16} className="shrink-0" aria-hidden="true" /><span>Could not load alerts. {error}</span></div>
                <Button onClick={() => void onRetry()}>Retry</Button>
              </div>
            </Notice>
          </div>
        )}

        {loading ? (
          <p role="status" className="console-muted border-t border-[var(--console-line)] p-8 text-center text-sm">Loading alerts…</p>
        ) : filtered.length > 0 ? (
          <>
            <div aria-hidden="true" className="queue-grid queue-table-head hidden border-t border-[var(--console-line)] text-xs font-semibold min-[1120px]:grid">
              <span>Alert</span><span>Severity / status</span><span>Events</span><span>Last seen</span><span />
            </div>
            <ul className="overflow-hidden rounded-b-xl">
              {filtered.map((alert) => <AlertCard key={alert.id} alert={alert} now={now} flash={flash.has(alert.id)} />)}
            </ul>
          </>
        ) : !error && (
          <div className="console-muted grid justify-items-center gap-2 border-t border-[var(--console-line)] px-4 py-10 text-center text-sm">
            <Inbox size={28} aria-hidden="true" />
            <h3 className="font-semibold text-[var(--console-ink)]">{text || severity ? "No matching alerts" : filter === "resolved" ? "No resolved alerts" : "No alerts in this view"}</h3>
            {text || severity ? (
              <Button onClick={() => { setQuery(""); setSeverity(""); }}>Clear search and severity</Button>
            ) : (
              <><p>{connection === "live" ? "New alerts appear here as events arrive." : "Live updates will resume when the stream reconnects."}</p><Link to="/sources" className="font-medium text-[var(--console-accent)] hover:underline">View event sources</Link></>
            )}
          </div>
        )}
      </section>
      {openId && <AlertDrawer key={openId} id={openId} live={alerts.find((a) => a.id === openId)} now={now} onClose={closeAlert} onAction={act} />}
    </>
  );
}

function Metric({ icon: Icon, label, value, children }: {
  icon: typeof Bell;
  label: string;
  value?: number;
  children?: ReactNode;
}) {
  return (
    <div className="console-panel min-w-0 px-3 py-2.5 sm:p-4">
      <div className="console-muted flex items-center gap-2 text-xs font-medium sm:text-sm"><Icon size={16} className="shrink-0 text-[var(--console-accent)]" aria-hidden="true" />{label}</div>
      <div className="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs">
        <span className="text-2xl font-semibold tracking-tight tabular-nums">{value?.toLocaleString() ?? "—"}</span>
        {children}
      </div>
    </div>
  );
}
