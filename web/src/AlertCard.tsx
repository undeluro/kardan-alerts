import { Hand, Link2, LockKeyhole } from "lucide-react";
import { alertKind, type Alert, type Incident } from "./api";
import { useBusinessNow } from "./clock";
import { Link } from "./router";
import {
  Badge, Button, CutoffChip, EscalationBadge, RuleKindChip,
  alertStatusLabel, formatUSD, statusBadge, timeAgo, toneFor,
} from "./ui";

interface Props {
  alert: Alert;
  incident?: Incident;
  onIncident?: (id: string) => void;
  flash: boolean;
  now: number;
  busy: boolean;
  onAck: () => void;
  onResolve: () => void;
}

const hhmm = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

export function AlertCard({ alert, incident, onIncident, flash, now, busy, onAck, onResolve }: Props) {
  const { id, title, severity, status, team, sourceId, body, count, lastSeen, fields } = alert;
  const tone = toneFor(alert.color);
  const resolved = status === "resolved";
  const bizNow = useBusinessNow(15_000);
  const windowLabel = alert.windowStart && alert.windowEnd ? `${hhmm(alert.windowStart)}–${hhmm(alert.windowEnd)} window` : null;
  const kind = alertKind(alert);
  const hasChips =
    kind !== "event" || (alert.notionalUsd ?? 0) > 0 || (alert.cutoffName && alert.cutoffAt) || alert.escalationLevel > 0 ||
    (alert.pendingApprovals ?? 0) > 0 || windowLabel || (incident && incident.alertCount > 1);

  return (
    <li className={`queue-grid queue-row ${flash ? "animate-flash" : ""} ${resolved ? "queue-row-resolved" : ""}`}>
      <div className="min-w-0">
        <Link
          to={`/alerts/${id}`}
          preserveScroll
          data-alert-link={id}
          className="queue-alert-link font-semibold leading-snug break-words hover:underline"
        >
          {title || `Alert from ${alert.ruleId}`}
        </Link>
        {body && <p className="console-muted mt-1 line-clamp-2 text-sm">{body}</p>}
        {hasChips && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <RuleKindChip kind={kind} />
            {alert.notionalUsd != null && alert.notionalUsd > 0 && (
              <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30">
                {formatUSD(alert.notionalUsd)} at risk
              </Badge>
            )}
            {alert.cutoffName && alert.cutoffAt && <CutoffChip name={alert.cutoffName} at={alert.cutoffAt} now={bizNow} resolved={resolved} />}
            <EscalationBadge level={alert.escalationLevel} />
            {(alert.pendingApprovals ?? 0) > 0 && (
              <Badge className="bg-fuchsia-600 text-white ring-fuchsia-600 dark:bg-fuchsia-500 dark:ring-fuchsia-500">
                <Hand size={12} aria-hidden="true" />
                <span title="A runbook step waits for a second person's approval">Approval needed</span>
              </Badge>
            )}
            {windowLabel && (
              <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30">
                {windowLabel}
              </Badge>
            )}
            {incident && incident.alertCount > 1 && (
              <button
                type="button"
                onClick={() => onIncident?.(incident.id)}
                className="queue-row-action inline-flex items-center gap-1 rounded-md bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-900 ring-1 ring-amber-600/20 ring-inset hover:bg-amber-100 dark:bg-amber-500/10 dark:text-amber-200 dark:ring-amber-400/30"
                title="Alerts sharing this key are grouped into one incident. Show only this incident."
              >
                <Link2 size={12} aria-hidden="true" />
                {incident.keyValue} · {incident.alertCount}
              </button>
            )}
          </div>
        )}
        <div className="console-muted mt-1.5 flex flex-wrap gap-x-2 text-xs">
          <span className="capitalize">{team}</span>
          <span aria-hidden="true">·</span>
          <span className="font-mono break-all">{sourceId}</span>
        </div>
        {fields && fields.length > 0 && (
          <dl className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {fields.slice(0, 3).map((field) => (
              <div key={field.label} className="flex min-w-0 flex-wrap items-center gap-1">
                <dt className="console-muted flex items-center gap-1">
                  {field.sensitive && <LockKeyhole size={12} aria-label="Sensitive field" />}
                  {field.label}
                </dt>
                <dd className="font-mono break-all">{field.value}</dd>
              </div>
            ))}
          </dl>
        )}
      </div>
      <div className="flex flex-col items-start gap-1.5">
        <Badge className={`${tone.badge} capitalize`}><span className={`size-1.5 rounded-full ${tone.dot}`} aria-hidden="true" />{severity}</Badge>
        <Badge className={statusBadge[status]}>{alertStatusLabel[status]}</Badge>
      </div>
      <div className="console-muted text-xs tabular-nums min-[1120px]:text-sm" title="Impact: severity, money at risk and urgency combined (0-100)">
        <span className="min-[1120px]:sr-only">Impact </span>
        <span className="font-mono font-semibold text-[var(--console-ink)]">{alert.impact}</span>
      </div>
      <div className="console-muted text-xs tabular-nums min-[1120px]:text-sm">
        <span className="font-semibold text-[var(--console-ink)]">{count.toLocaleString()}</span>
        <span className="min-[1120px]:sr-only"> {count === 1 ? "event" : "events"}</span>
      </div>
      <time className="console-muted whitespace-nowrap text-xs min-[1120px]:text-sm" dateTime={lastSeen} title={new Date(lastSeen).toLocaleString()}>
        <span className="sr-only">Last seen </span>{timeAgo(lastSeen, now)}
      </time>
      <div className="queue-row-action flex flex-wrap justify-end gap-1.5 min-[1120px]:justify-start">
        {!resolved && (
          <>
            {status === "open" && (
              <Button onClick={onAck} disabled={busy} className="queue-action-button">
                Acknowledge
              </Button>
            )}
            <Button onClick={onResolve} variant={status === "acked" ? "primary" : "secondary"} disabled={busy} className="queue-action-button">
              Resolve
            </Button>
          </>
        )}
      </div>
    </li>
  );
}
