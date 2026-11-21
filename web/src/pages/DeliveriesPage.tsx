import { canManageAlert, useConsoleSession } from "../ConsoleSession";
import { ArrowUpRight } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, type ChannelView, type Delivery, type Info } from "../api";
import { Link } from "../router";
import { Badge, Button, Card, ChannelIcon, KindBadge, Notice, PageHeader, Select, channelLabel, statusBadge, timeAgo, toneFor } from "../ui";

const FILTERS = [
  ["", "All"],
  ["queued", "Queued / retrying"],
  ["dead", "Dead letters"],
  ["sent", "Sent"],
] as const;

export function DeliveriesPage({ now, onToast }: { now: number; onToast: (m: string) => void }) {
  const user = useConsoleSession()?.user;
  const [channels, setChannels] = useState<ChannelView[]>([]);
  const [items, setItems] = useState<Delivery[] | null>(null);
  const [status, setStatus] = useState<string>("");
  const [channel, setChannel] = useState<string>("");
  const [info, setInfo] = useState<Info | null>(null);

  const load = useCallback(() => {
    api.channels().then(setChannels).catch(() => {});
    api.deliveries(status, channel).then(setItems).catch(() => setItems([]));
  }, [status, channel]);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 2000);
    return () => window.clearInterval(t);
  }, [load]);
  useEffect(() => {
    api.info().then(setInfo).catch(() => {});
  }, []);

  const resend = async (d: Delivery) => {
    try {
      await api.resend(d.id);
      onToast(`Re-queued ${d.channel} to ${d.recipient}`);
      load();
    } catch (e) {
      onToast((e as Error).message);
    }
  };

  const failing = channels.filter((c) => c.breaker !== "closed" || c.retrying > 0);

  return (
    <>
      <PageHeader
        title="Deliveries"
        subtitle="Monitor channel health, delivery attempts, and retries."
        actions={
          info?.gatewayUrl && (
            <a
              href={info.gatewayUrl}
              target="_blank"
              rel="noopener"
              className="rounded-md bg-white px-2.5 py-1.5 text-sm font-medium ring-1 ring-gray-300 ring-inset hover:bg-gray-50 dark:bg-gray-800 dark:ring-gray-600"
            >
              Open phone simulator <ArrowUpRight size={13} aria-hidden="true" className="ml-0.5 inline-block" />
            </a>
          )
        }
      />

      {failing.length > 0 && (
        <div className="mb-4">
          <Notice tone="warn">
            {failing.map((c) => (
              <div key={c.channel}>
                <b>{c.channel.toUpperCase()}</b>:{" "}
                {c.breaker === "open"
                  ? "provider unavailable — circuit open, deliveries waiting and fallbacks active"
                  : c.breaker === "half-open"
                    ? "probing the provider"
                    : `${c.retrying} retrying`}
                {c.lastError && <span className="ml-1 font-mono text-xs opacity-80">({c.lastError})</span>}
              </div>
            ))}
          </Notice>
        </div>
      )}

      <div className="mb-4 grid gap-2 sm:grid-cols-3">
        {channels.map((c) => (
          <Card key={c.channel} className="p-3">
            <div className="flex items-center gap-2">
              <ChannelIcon channel={c.channel} className="console-muted shrink-0" />
              <span className="font-semibold capitalize">{channelLabel(c.channel)}</span>
              <BreakerBadge state={c.breaker} />
            </div>
            <div className="mt-0.5 text-xs text-gray-500">{c.provider}</div>
            <dl className="mt-3 grid grid-cols-4 gap-2 text-center text-xs">
              <Metric label="sent 15m" value={c.sent} />
              <Metric label="queued" value={c.queued} />
              <Metric label="retrying" value={c.retrying} warn={c.retrying > 0} />
              <Metric label="dead" value={c.dead} warn={c.dead > 0} />
            </dl>
            {c.sent > 0 && (
              <div className="mt-2 text-xs text-gray-500">
                ingest → delivered: p50 {Math.round(c.p50Ms)} ms · p99 {Math.round(c.p99Ms)} ms
              </div>
            )}
          </Card>
        ))}
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-1 text-sm">
        {FILTERS.map(([v, label]) => (
          <button
            key={label}
            type="button"
            aria-pressed={status === v}
            onClick={() => setStatus(v)}
            className="console-toggle"
          >
            {label}
          </button>
        ))}
        <Select
          value={channel}
          onChange={(e) => setChannel(e.target.value)}
          className="ml-auto min-w-36"
          aria-label="Channel"
        >
          <option value="">All channels</option>
          <option value="email">Email</option>
          <option value="sms">SMS</option>
          <option value="phone">Phone</option>
        </Select>
      </div>

      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {items === null && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {items?.length === 0 && <div className="p-4 text-sm text-gray-500">No deliveries match these filters.</div>}
        {items?.map((d) => (
          <div key={d.id} className="flex flex-wrap items-start gap-3 p-3 text-sm">
            <span className="console-muted mt-0.5"><ChannelIcon channel={d.channel} /><span className="sr-only">{channelLabel(d.channel)}</span></span>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{d.recipient}</span>
                <span className="font-mono text-xs text-gray-500">{d.address}</span>
                <Badge className={statusBadge[d.status] ?? statusBadge.pending}>{d.status}</Badge>
                <KindBadge kind={d.kind} />
                {d.fallbackFor && <Badge className="bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300">fallback</Badge>}
                {d.attempts > 1 && <span className="text-xs text-gray-500">{d.attempts} attempts</span>}
              </div>
              <Link to={`/alerts/${d.alertId}`} className="mt-0.5 block truncate text-gray-700 hover:underline dark:text-gray-300">
                <span className={`mr-1.5 inline-block h-2 w-2 rounded-full ${toneFor(sevColor(d.alertSeverity)).dot}`} />
                {d.alertTitle}
              </Link>
              {d.reason && <div className="text-xs text-gray-500">{d.reason}</div>}
              {d.lastError && d.status !== "sent" && <div className="mt-0.5 truncate font-mono text-xs text-red-600 dark:text-red-400">{d.lastError}</div>}
            </div>
            <div className="flex shrink-0 items-center gap-2 text-xs text-gray-500">
              <span>{timeAgo(d.sentAt ?? d.createdAt, now)}</span>
              {canManageAlert(user, d.alertTeam) && (d.status === "dead" || d.status === "retry") && (
                <Button onClick={() => void resend(d)}>{d.status === "dead" ? "Re-send" : "Retry now"}</Button>
              )}
            </div>
          </div>
        ))}
      </Card>
    </>
  );
}

const sevColor = (s: string) => ({ critical: "red", error: "orange", warning: "amber", info: "blue" })[s] ?? "gray";

function BreakerBadge({ state }: { state: ChannelView["breaker"] }) {
  const cls = {
    closed: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300",
    "half-open": "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300",
    open: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300",
  }[state];
  const label = { closed: "healthy", "half-open": "probing", open: "circuit open" }[state];
  return <Badge className={`ml-auto ${cls}`}>{label}</Badge>;
}

function Metric({ label, value, warn }: { label: string; value: number; warn?: boolean }) {
  return (
    <div>
      <div className={`text-lg font-semibold tabular-nums ${warn ? "text-red-600 dark:text-red-400" : ""}`}>{value}</div>
      <div className="text-gray-500">{label}</div>
    </div>
  );
}
