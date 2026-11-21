import { ArrowLeft, Clock3, Link2 } from "lucide-react";
import { useEffect, useState } from "react";
import { api, type Alert, type Incident } from "../api";
import { useBusinessNow } from "../clock";
import { Link } from "../router";
import { Badge, CutoffChip, Notice, PageHeader, alertStatusLabel, formatUSD, statusBadge, timeAgo, toneFor } from "../ui";
import { incidentLabel } from "./AlertsPage";

/** One correlated incident: what it costs, how close the deadline is, and every alert in it. */
export function IncidentPage({ id, now }: { id: string; now: number }) {
  const [data, setData] = useState<{ incident: Incident; alerts: Alert[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const bizNow = useBusinessNow(15_000);

  useEffect(() => {
    let cancelled = false;
    const load = () => api.incident(id).then((d) => { if (!cancelled) { setData(d); setError(null); } })
      .catch((e: Error) => { if (!cancelled) setError(e.message); });
    void load();
    const t = window.setInterval(load, 5000);
    return () => { cancelled = true; window.clearInterval(t); };
  }, [id]);

  if (error && !data) return <Notice tone="error">{error}</Notice>;
  if (!data) return <p role="status" className="console-muted text-sm">Loading incident…</p>;
  const { incident: inc, alerts } = data;
  const nearest = alerts
    .filter((a) => a.cutoffAt && a.status !== "resolved")
    .sort((a, b) => Date.parse(a.cutoffAt!) - Date.parse(b.cutoffAt!))[0];
  const sources = [...new Set(alerts.map((a) => a.sourceId))];

  return (
    <>
      <Link to={`/?incident=${encodeURIComponent(inc.id)}`} className="console-muted mb-3 inline-flex items-center gap-1.5 text-sm hover:underline">
        <ArrowLeft size={15} aria-hidden="true" /> Alert queue
      </Link>
      <PageHeader
        title={`${incidentLabel(inc.keyField)} ${inc.keyValue}`}
        subtitle={<span className="inline-flex flex-wrap items-center gap-2">
          <Link2 size={14} aria-hidden="true" /> Alerts sharing this {incidentLabel(inc.keyField).toLowerCase()} fold into one incident.
          <Badge className={inc.status === "open" ? statusBadge.open : statusBadge.resolved}>{inc.status === "open" ? "Open" : "Resolved"}</Badge>
        </span>}
      />
      <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
        <Tile label="Money at risk" value={inc.notionalUsd > 0 ? formatUSD(inc.notionalUsd) : "—"} tone="events" />
        <Tile label="Open alerts" value={`${inc.openAlerts} of ${inc.alertCount}`} tone="open" />
        <Tile label="Impact (0-100)" value={String(inc.impact)} tone="delivery" />
        <Tile label="Teams" value={inc.teams.join(", ") || "—"} tone="acked" />
      </div>
      {nearest && (
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
          <Clock3 size={15} aria-hidden="true" className="console-muted" /> Nearest deadline:
          <CutoffChip name={nearest.cutoffName!} at={nearest.cutoffAt!} now={bizNow} />
        </div>
      )}
      <p className="console-muted mt-2 text-xs">
        First seen {timeAgo(inc.firstSeen, now)}, last activity {timeAgo(inc.lastSeen, now)} · from {sources.join(", ")}
      </p>

      <section className="console-panel mt-3" aria-labelledby="incident-alerts">
        <h2 id="incident-alerts" className="px-4 pt-3 pb-2 text-sm font-semibold">Alerts in this incident</h2>
        <ul>
          {alerts.map((a) => (
            <li key={a.id} className="queue-row flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5">
              <span className={`size-2 shrink-0 rounded-full ${toneFor(a.color).dot}`} aria-hidden="true" />
              <Link to={`/alerts/${a.id}`} className="min-w-0 flex-1 font-medium break-words hover:underline">{a.title}</Link>
              <Badge className={statusBadge[a.status]}>{alertStatusLabel[a.status]}</Badge>
              {a.notionalUsd ? <span className="font-mono text-xs text-emerald-700 dark:text-emerald-400">{formatUSD(a.notionalUsd)}</span> : null}
              <span className="console-muted text-xs">{a.team} · ×{a.count} · {timeAgo(a.lastSeen, now)}</span>
            </li>
          ))}
        </ul>
      </section>
    </>
  );
}

function Tile({ label, value, tone }: { label: string; value: string; tone: "open" | "acked" | "events" | "delivery" }) {
  return (
    <div className={`console-panel metric-panel metric-${tone} min-w-0 px-3 py-2.5`}>
      <div className="console-muted text-xs font-medium">{label}</div>
      <div className="metric-value mt-2 truncate font-mono text-xl font-semibold tabular-nums" title={value}>{value}</div>
    </div>
  );
}
