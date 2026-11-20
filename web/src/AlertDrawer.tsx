import { useEffect, useState } from "react";
import { api, type Alert, type Notification } from "./api";
import { Badge, Button, KindBadge, statusBadge, timeAgo, toneFor } from "./ui";

interface Props {
  id: string;
  live?: Alert; // latest version from the feed, so counts update while open
  now: number;
  onClose: () => void;
  onAck: () => void;
  onResolve: () => void;
}

const channelIcon: Record<string, string> = { email: "✉", sms: "💬", phone: "📞", ui: "🖥" };

export function AlertDrawer({ id, live, now, onClose, onAck, onResolve }: Props) {
  const [detail, setDetail] = useState<{ alert: Alert; notifications: Notification[] } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .alert(id)
        .then((d) => !cancelled && setDetail(d))
        .catch((e: Error) => !cancelled && setError(e.message));
    void load();
    const timer = window.setInterval(load, 3000); // delivery status changes without a push event
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [id]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const a = live
    ? { ...detail?.alert, ...live, sample: detail?.alert.sample, sampleRaw: detail?.alert.sampleRaw, sampleFormat: detail?.alert.sampleFormat }
    : detail?.alert;

  return (
    <div className="fixed inset-0 z-40 flex justify-end" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-gray-900/30 backdrop-blur-[1px]" onClick={onClose} />
      <aside className="relative flex h-full w-full max-w-xl flex-col overflow-y-auto bg-white shadow-2xl dark:bg-gray-950">
        {!a ? (
          <div className="p-6 text-sm text-gray-500">{error ?? "Loading…"}</div>
        ) : (
          <>
            <header className={`border-b border-gray-200 p-5 dark:border-gray-800`}>
              <div className="flex items-start justify-between gap-4">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge className={toneFor(a.color).badge}>{a.severity}</Badge>
                  <Badge className={statusBadge[a.status]}>{a.status}</Badge>
                  <span className="text-xs text-gray-500">rule {a.ruleId}</span>
                </div>
                <button onClick={onClose} className="rounded p-1 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200" aria-label="Close">
                  ✕
                </button>
              </div>
              <h2 className="mt-2 text-lg font-semibold text-gray-900 dark:text-gray-50">{a.title}</h2>
              {a.body && <p className="mt-1 whitespace-pre-wrap text-sm text-gray-600 dark:text-gray-300">{a.body}</p>}
              {a.status !== "resolved" && (
                <div className="mt-4 flex gap-2">
                  {a.status === "open" && <Button onClick={onAck}>Acknowledge</Button>}
                  <Button onClick={onResolve} variant="primary">
                    Resolve
                  </Button>
                </div>
              )}
            </header>

            <section className="grid grid-cols-2 gap-x-6 gap-y-3 border-b border-gray-200 p-5 text-sm dark:border-gray-800">
              <Info label="Occurrences" value={a.count.toLocaleString()} />
              <Info label="Team" value={a.team} />
              <Info label="First seen" value={`${new Date(a.firstSeen).toLocaleString()} (${timeAgo(a.firstSeen, now)})`} />
              <Info label="Last seen" value={`${new Date(a.lastSeen).toLocaleString()} (${timeAgo(a.lastSeen, now)})`} />
              <Info label="Source" value={a.sourceId} />
              {a.ackedBy && <Info label="Acknowledged by" value={a.ackedBy} />}
              {a.resolvedBy && <Info label="Resolved by" value={a.resolvedBy} />}
              {(a.fields ?? []).map((f) => (
                <Info key={f.label} label={f.label} value={f.value} mono sensitive={f.sensitive} />
              ))}
            </section>

            <section className="border-b border-gray-200 p-5 dark:border-gray-800">
              <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Deliveries</h3>
              {detail && detail.notifications.length === 0 && (
                <p className="mt-2 text-sm text-gray-500">Shown in the live feed only (no external channels on this rule).</p>
              )}
              <ul className="mt-2 divide-y divide-gray-100 dark:divide-gray-800">
                {detail?.notifications.map((n) => (
                  <li key={n.id} className={`flex items-start justify-between gap-3 py-2 text-sm ${n.fallbackFor ? "pl-5" : ""}`}>
                    <div className="min-w-0">
                      <div className="text-gray-900 dark:text-gray-100">
                        {n.fallbackFor && <span className="mr-1 text-gray-400">↳</span>}
                        <span className="mr-1.5">{channelIcon[n.channel] ?? "•"}</span>
                        {n.channel} → {n.recipient} <span className="text-gray-500">({n.address})</span>
                      </div>
                      {n.reason && <div className="mt-0.5 text-xs text-gray-500">{n.reason}</div>}
                      {n.lastError && n.status !== "sent" && <div className="mt-0.5 truncate text-xs text-red-600 dark:text-red-400">{n.lastError}</div>}
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      <KindBadge kind={n.kind} />
                      {n.attempts > 1 && <span className="text-xs text-gray-500">{n.attempts} attempts</span>}
                      <Badge className={statusBadge[n.status] ?? statusBadge.pending}>{n.status === "standby" ? "fallback (not needed yet)" : n.status}</Badge>
                    </div>
                  </li>
                ))}
              </ul>
            </section>

            {a.sampleRaw && (
              <section className="px-5 pt-5">
                <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">First event as received ({a.sampleFormat})</h3>
                <pre className="mt-2 overflow-x-auto rounded-md bg-gray-50 p-3 font-mono text-xs whitespace-pre-wrap break-all text-gray-800 ring-1 ring-gray-200 dark:bg-gray-900 dark:text-gray-200 dark:ring-gray-800">
                  {a.sampleRaw}
                </pre>
              </section>
            )}
            {a.sample !== undefined && (
              <section className="p-5">
                <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{a.sampleRaw ? "Parsed payload" : "First event payload"}</h3>
                <pre className="mt-2 overflow-x-auto rounded-md bg-gray-50 p-3 font-mono text-xs text-gray-800 ring-1 ring-gray-200 dark:bg-gray-900 dark:text-gray-200 dark:ring-gray-800">
                  {JSON.stringify(a.sample, null, 2)}
                </pre>
              </section>
            )}
          </>
        )}
      </aside>
    </div>
  );
}

function Info({ label, value, mono, sensitive }: { label: string; value: string; mono?: boolean; sensitive?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-gray-500 dark:text-gray-400">
        {sensitive && <span title="Sensitive: masked on SMS, calls and email">🔒 </span>}
        {label}
      </div>
      <div className={`truncate text-gray-900 dark:text-gray-100 ${mono ? "font-mono text-xs" : ""}`} title={value}>
        {value}
      </div>
    </div>
  );
}
