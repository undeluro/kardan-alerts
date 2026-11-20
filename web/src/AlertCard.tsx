import type { Alert } from "./api";
import { Badge, Button, formatCount, statusBadge, timeAgo, toneFor } from "./ui";

interface Props {
  alert: Alert;
  flash: boolean;
  now: number;
  busy: boolean;
  onOpen: () => void;
  onAck: () => void;
  onResolve: () => void;
}

export function AlertCard({ alert: a, flash, now, busy, onOpen, onAck, onResolve }: Props) {
  const t = toneFor(a.color);
  const resolved = a.status === "resolved";
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
            {a.count > 1 && (
              <Badge className="bg-gray-900 text-white ring-gray-900 dark:bg-white dark:text-gray-900 dark:ring-white">
                ×{formatCount(a.count)}
              </Badge>
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
                  <dt className="text-gray-500 dark:text-gray-400">{f.label}</dt>
                  <dd className="truncate font-mono text-gray-800 dark:text-gray-200" title={f.value}>
                    {f.value}
                  </dd>
                </div>
              ))}
            </dl>
          )}
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
