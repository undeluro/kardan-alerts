import { ChevronRight, LockKeyhole } from "lucide-react";
import type { Alert } from "./api";
import { Link } from "./router";
import { Badge, alertStatusLabel, statusBadge, timeAgo, toneFor } from "./ui";

export function AlertCard({ alert, flash, now }: { alert: Alert; flash: boolean; now: number }) {
  const { id, title, severity, status, team, sourceId, body, count, lastSeen, fields } = alert;
  const tone = toneFor(alert.color);
  return (
    <li className={`queue-grid queue-row ${flash ? "animate-flash" : ""}`}>
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
        <div className="console-muted mt-1.5 flex flex-wrap gap-x-2 text-xs">
          <span className="capitalize">{team}</span>
          <span aria-hidden="true">·</span>
          <span className="font-mono break-all">{sourceId}</span>
        </div>
        {fields && fields.length > 0 && (
          <dl className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {fields.slice(0, 2).map((field) => (
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
      <div className="console-muted text-xs tabular-nums min-[1120px]:text-sm">
        <span className="font-semibold text-[var(--console-ink)]">{count.toLocaleString()}</span>
        <span className="min-[1120px]:sr-only"> {count === 1 ? "event" : "events"}</span>
      </div>
      <time className="console-muted whitespace-nowrap text-xs min-[1120px]:text-sm" dateTime={lastSeen} title={new Date(lastSeen).toLocaleString()}>
        <span className="sr-only">Last seen </span>{timeAgo(lastSeen, now)}
      </time>
      <ChevronRight size={17} className="console-muted hidden min-[1120px]:block" aria-hidden="true" />
    </li>
  );
}
