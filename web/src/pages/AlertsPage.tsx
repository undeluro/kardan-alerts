import { Activity, Bell, CheckCircle2, CircleAlert, Inbox, Link2, Search, Send, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, type Alert, type Incident, type Stats, type Team } from "../api";
import { AlertCard } from "../AlertCard";
import { AlertDrawer } from "../AlertDrawer";
import { Link, navigate } from "../router";
import { Button, Notice, PageHeader, Select, formatUSD, toneFor } from "../ui";
import type { Connection, SortOrder, StatusFilter } from "../useLiveAlerts";

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
  sort: SortOrder;
  setSort: (s: SortOrder) => void;
  incident: string;
  setIncident: (id: string) => void;
  actor: string;
  now: number;
  openId: string | null;
  onToast: (msg: string) => void;
}

const statuses = [["active", "Active"], ["resolved", "Resolved"], ["", "All alerts"]] as const;

export function AlertsPage({
  alerts, error, loading, connection, onRetry, flash, replace, team, teams, onTeamChange,
  filter, setFilter, sort, setSort, incident, setIncident, actor, now, openId, onToast,
}: Props) {
  const [stats, setStats] = useState<{ team: string; values: Stats } | null>(null);
  const [query, setQuery] = useState("");
  const [severity, setSeverity] = useState("");
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [incidents, setIncidents] = useState<Incident[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = () => api.incidents("open").then((list) => {
      if (!cancelled) setIncidents(list);
    }).catch(() => {});
    void load();
    const timer = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [alerts.length]);
  const incidentById = useMemo(() => new Map(incidents.map((i) => [i.id, i])), [incidents]);
  const selected = incident ? incidentById.get(incident) : undefined;

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
  }, [team, alerts.length]);

  // Used by the alert sheet: errors surface inside the sheet.
  const act = useCallback(async (alert: Alert, action: "ack" | "resolve") => {
    const by = actor.trim() || "anonymous";
    const result = action === "ack" ? await api.ack(alert.id, by) : await api.resolve(alert.id, by);
    replace(result);
    onToast(action === "ack" ? "Alert acknowledged" : "Alert resolved");
    return result;
  }, [actor, replace, onToast]);

  // Used by the queue's inline buttons: errors surface as a toast.
  const quickAct = useCallback(async (alert: Alert, action: "ack" | "resolve") => {
    setBusy((b) => new Set(b).add(alert.id));
    try {
      await act(alert, action);
    } catch (e) {
      onToast(`Could not ${action === "ack" ? "acknowledge" : "resolve"}: ${(e as Error).message}`);
    } finally {
      setBusy((b) => {
        const next = new Set(b);
        next.delete(alert.id);
        return next;
      });
    }
  }, [act, onToast]);

  const totals = stats?.team === team ? stats.values : null;
  const text = query.trim().toLowerCase();
  const filtered = alerts.filter((alert) => {
    if (severity && alert.severity.toLowerCase() !== severity) return false;
    return !text || [alert.title, alert.body, alert.team, alert.sourceId, alert.ruleId].some((value) => value.toLowerCase().includes(text));
  });
  const severityOptions = new Set(["critical", "error", "warning", "info", "debug", ...alerts.map((alert) => alert.severity.toLowerCase())]);

  if (severity) severityOptions.add(severity);

  const closeAlert = () => navigate(`/${window.location.search}`, true, { preserveScroll: true });

  return (
    <>
      <PageHeader title="Alerts" />
      <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
        <Metric icon={Bell} tone="open" label="Open alerts" value={totals?.openAlerts} />
        <Metric icon={CheckCircle2} tone="acked" label="Acknowledged" value={totals?.ackedAlerts} />
        <Metric icon={Activity} tone="events" label="Matching events · 1h" value={totals?.eventsLastHour} />
        <Metric icon={Send} tone="delivery" label="Pending deliveries" value={totals?.pendingDeliveries}>
          {totals && totals.failedDeliveries > 0 && (
            <Link to="/deliveries" className="font-medium text-red-700 hover:underline dark:text-red-300">{totals.failedDeliveries} failed</Link>
          )}
        </Metric>
      </div>

      {incidents.length > 0 && (
        <section aria-labelledby="incidents-title" className="mt-3">
          <h2 id="incidents-title" className="console-muted mb-1.5 flex items-center gap-1.5 text-xs font-semibold">
            <Link2 size={13} aria-hidden="true" /> Correlated incidents
          </h2>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {incidents.map((i) => (
              <button
                key={i.id}
                type="button"
                aria-pressed={incident === i.id}
                onClick={() => setIncident(incident === i.id ? "" : i.id)}
                className="console-panel incident-card min-w-[240px] px-3 py-2.5 text-left"
              >
                <div className="flex items-center gap-2">
                  <span className={`size-2 rounded-full ${toneFor(sevColor(i.maxSeverity)).dot}`} aria-hidden="true" />
                  <span className="console-muted text-xs">{incidentLabel(i.keyField)}</span>
                  <span className="ml-auto font-mono text-xs font-semibold tabular-nums" title="Impact (0-100)">{i.impact}</span>
                </div>
                <div className="mt-1 truncate font-semibold">{i.keyValue}</div>
                <div className="console-muted mt-0.5 text-xs">
                  {i.openAlerts} open {i.openAlerts === 1 ? "alert" : "alerts"} · {i.teams.join(", ")}
                </div>
                {i.notionalUsd > 0 && (
                  <div className="mt-1 text-sm font-semibold text-emerald-700 dark:text-emerald-400">{formatUSD(i.notionalUsd)} at risk</div>
                )}
              </button>
            ))}
          </div>
        </section>
      )}

      <section aria-labelledby="alert-queue-title" className="console-panel mt-3">
        <div className="px-3 pt-3 sm:px-4">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2 id="alert-queue-title" tabIndex={-1} className="text-sm font-semibold">Alert queue</h2>
            {!loading && !error && <span className="console-muted text-xs tabular-nums">{filtered.length} of {alerts.length} loaded</span>}
          </div>
          {incident && (
            <div className="mt-2">
              <Notice tone="warn">
                <div className="flex flex-wrap items-center gap-2">
                  <span>
                    Showing incident <b>{selected ? `${incidentLabel(selected.keyField)} ${selected.keyValue}` : incident}</b>
                  </span>
                  <button type="button" onClick={() => setIncident("")} className="ml-auto inline-flex items-center gap-1 font-medium hover:underline">
                    Show all <X size={14} aria-hidden="true" />
                  </button>
                </div>
              </Notice>
            </div>
          )}
          <div className="my-3 grid gap-2 lg:grid-cols-[auto_minmax(0,1fr)] xl:gap-4">
            <div className="flex flex-wrap gap-1 self-start" role="group" aria-label="Alert status">
              {statuses.map(([value, label]) => (
                <button key={label} type="button" aria-pressed={filter === value} onClick={() => setFilter(value)}
                  className="console-toggle">
                  {label}
                </button>
              ))}
            </div>
            <div className="grid min-w-0 grid-cols-2 gap-2 sm:grid-cols-3 2xl:grid-cols-[minmax(8rem,1fr)_minmax(7rem,10rem)_minmax(7rem,10rem)_minmax(8rem,11rem)]">
              <div className="relative col-span-2 sm:col-span-3 2xl:col-span-1">
                <Search className="console-muted pointer-events-none absolute top-1/2 left-3 -translate-y-1/2" size={16} aria-hidden="true" />
                <input value={query} onChange={(event) => setQuery(event.target.value)} aria-label="Search loaded alerts"
                  type="search" placeholder="Search alerts…" className="console-control queue-search w-full" />
                {query && <button type="button" onClick={() => setQuery("")} aria-label="Clear search"
                  className="console-muted absolute inset-y-0 right-0 grid w-9 place-items-center"><X size={16} aria-hidden="true" /></button>}
              </div>
              <Select aria-label="Team" value={team} onChange={(event) => onTeamChange(event.target.value)}>
                <option value="">All teams</option>
                {teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </Select>
              <Select aria-label="Severity" value={severity} onChange={(event) => setSeverity(event.target.value)}>
                <option value="">All severities</option>
                {[...severityOptions].map((value) => <option key={value} value={value}>{value.charAt(0).toUpperCase() + value.slice(1)}</option>)}
              </Select>
              <Select aria-label="Sort" value={sort} onChange={(event) => setSort(event.target.value as SortOrder)}>
                <option value="impact">Sort: impact</option>
                <option value="money">Sort: money at risk</option>
                <option value="recent">Sort: most recent</option>
              </Select>
            </div>
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
              <span>Alert</span><span>Severity / status</span><span>Impact</span><span>Events</span><span>Last seen</span><span>Actions</span>
            </div>
            <ul className="overflow-hidden rounded-b-md">
              {filtered.map((alert) => (
                <AlertCard
                  key={alert.id}
                  alert={alert}
                  incident={alert.incidentId ? incidentById.get(alert.incidentId) : undefined}
                  onIncident={setIncident}
                  now={now}
                  flash={flash.has(alert.id)}
                  busy={busy.has(alert.id)}
                  onAck={() => void quickAct(alert, "ack")}
                  onResolve={() => void quickAct(alert, "resolve")}
                />
              ))}
            </ul>
          </>
        ) : !error && (
          <div className="console-muted grid justify-items-center gap-2 border-t border-[var(--console-line)] px-4 py-10 text-center text-sm">
            <Inbox size={28} aria-hidden="true" />
            <h3 className="font-semibold text-[var(--console-ink)]">{text || severity ? "No matching alerts" : filter === "resolved" ? "No resolved alerts" : "No alerts in this view"}</h3>
            {text || severity ? (
              <Button onClick={() => { setQuery(""); setSeverity(""); }}>Clear search and severity</Button>
            ) : (
              <>
                <p>{connection === "live" ? "New alerts appear here as events arrive. Try sending the sample events:" : "Live updates will resume when the stream reconnects."}</p>
                {connection === "live" && <code className="rounded bg-[var(--console-canvas)] px-2 py-1 font-mono text-xs text-[var(--console-ink)]">./samples/send.sh</code>}
                <Link to="/sources" className="font-medium text-[var(--console-accent)] hover:underline">View event sources</Link>
              </>
            )}
          </div>
        )}
      </section>
      {openId && <AlertDrawer key={openId} id={openId} live={alerts.find((a) => a.id === openId)} now={now} onClose={closeAlert} onAction={act} />}
    </>
  );
}

function Metric({ icon: Icon, label, value, tone, children }: {
  icon: typeof Bell;
  label: string;
  value?: number;
  tone: "open" | "acked" | "events" | "delivery";
  children?: ReactNode;
}) {
  return (
    <div className={`console-panel metric-panel metric-${tone} min-w-0 px-3 py-2.5`}>
      <div className="console-muted flex items-center gap-2 text-xs font-medium"><Icon size={14} className="shrink-0" aria-hidden="true" />{label}</div>
      <div className="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs">
        <span className="metric-value font-mono text-2xl font-semibold tracking-tight tabular-nums" data-unavailable={value === undefined}>{value?.toLocaleString() ?? "—"}</span>
        {children}
      </div>
    </div>
  );
}

const sevColor = (s: string) => ({ critical: "red", error: "orange", warning: "amber", info: "blue" })[s] ?? "gray";

const incidentLabels: Record<string, string> = {
  $counterparty: "Counterparty",
  $instrument: "Instrument",
  $service: "Service",
  $tradeId: "Trade",
  $desk: "Desk",
};

export function incidentLabel(field: string) {
  return incidentLabels[field] ?? field.replace("$", "");
}
