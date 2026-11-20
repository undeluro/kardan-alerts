import { alertKind, type Alert, type Incident } from "./api";
import { useBusinessNow } from "./clock";
import { Badge, Button, CutoffChip, EscalationBadge, formatCount, formatUSD, RuleKindChip, statusBadge, timeAgo, toneFor } from "./ui";

interface Props {
  alert: Alert;
  incident?: Incident;
  onIncident?: (id: string) => void;
  flash: boolean;
  now: number;
  busy: boolean;
  onOpen: () => void;
  onAck: () => void;
  onResolve: () => void;
}

const hhmm = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

export function AlertCard({ alert: a, incident, onIncident, flash, now, busy, onOpen, onAck, onResolve }: Props) {
  const windowLabel = a.windowStart && a.windowEnd ? `${hhmm(a.windowStart)}–${hhmm(a.windowEnd)} window` : null;
  const t = toneFor(a.color);
  const resolved = a.status === "resolved";
  const bizNow = useBusinessNow(15_000);
  return (
    <li
      onClick={onOpen}
      className={`group relative flex cursor-pointer overflow-hidden rounded-lg bg-white shadow-xs ring-1 ring-gray-200 transition hover:shadow-md hover:ring-gray-300 dark:bg-gray-900 dark:ring-gray-800 dark:hover:ring-gray-700 ${
        flash ? "animate-flash" : ""
      } ${resolved ? "opacity-70" : ""}`}
    >
      <div className={`w-1.5 shrink-0 ${t.stripe}`} aria-hidden />
      <div className="flex min-w-0 flex-1 flex-col gap-2 p-4 sm:flex-row sm:items-start sm:gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge className={t.badge}>{a.severity}</Badge>
            <Badge className={statusBadge[a.status]}>{a.status}</Badge>
            <RuleKindChip kind={alertKind(a)} />
            {a.count > 1 && (
              <Badge className="bg-gray-900 text-white ring-gray-900 dark:bg-white dark:text-gray-900 dark:ring-white">
                ×{formatCount(a.count)}
              </Badge>
            )}
            {a.notionalUsd != null && a.notionalUsd > 0 && (
              <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30">
                {formatUSD(a.notionalUsd)} at risk
              </Badge>
            )}
            {a.cutoffName && a.cutoffAt && <CutoffChip name={a.cutoffName} at={a.cutoffAt} now={bizNow} resolved={resolved} />}
            <EscalationBadge level={a.escalationLevel} />
            {windowLabel && (
              <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30">
                {windowLabel}
              </Badge>
            )}
            {incident && incident.alertCount > 1 && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onIncident?.(incident.id);
                }}
                className="inline-flex items-center gap-1 rounded-md bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-900 ring-1 ring-amber-600/20 ring-inset hover:bg-amber-100 dark:bg-amber-500/10 dark:text-amber-200 dark:ring-amber-400/30"
                title="Alerts sharing this key are grouped into one incident"
              >
                ⛓ {incident.keyValue} · {incident.alertCount}
              </button>
            )}
            <span className="text-xs text-gray-500 dark:text-gray-400">
              {a.team} · {a.sourceId} · {timeAgo(a.lastSeen, now)}
            </span>
          </div>
          <h3 className="mt-1.5 truncate text-base font-semibold text-gray-900 dark:text-gray-50" title={a.title}>
            {a.title}
          </h3>
          {a.body && <p className="mt-0.5 line-clamp-2 text-sm text-gray-600 dark:text-gray-300">{a.body}</p>}
          {a.fields && a.fields.length > 0 && (
            <dl className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs">
              {a.fields.slice(0, 5).map((f) => (
                <div key={f.label} className="flex min-w-0 gap-1.5">
                  <dt className="text-gray-500 dark:text-gray-400">
                    {f.sensitive && (
                      <span title="Sensitive: masked on SMS, calls and email" className="mr-0.5">
                        🔒
                      </span>
                    )}
                    {f.label}
                  </dt>
                  <dd className="truncate font-mono text-gray-800 dark:text-gray-200" title={f.value}>
                    {f.value}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </div>
        <div className="hidden shrink-0 flex-col items-center sm:flex" title="Impact: severity, money at risk and urgency combined (0-100)">
          <span className="text-lg leading-none font-semibold tabular-nums">{a.impact}</span>
          <span className="text-[10px] tracking-wide text-gray-500 uppercase">impact</span>
        </div>
        {!resolved && (
          <div className="flex shrink-0 gap-2">
            {a.status === "open" && (
              <Button onClick={onAck} disabled={busy}>
                Acknowledge
              </Button>
            )}
            <Button onClick={onResolve} variant="primary" disabled={busy}>
              Resolve
            </Button>
          </div>
        )}
      </div>
    </li>
  );
}
