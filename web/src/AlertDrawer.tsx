import { canManageAlert, useConsoleSession } from "./ConsoleSession";
import * as Dialog from "@radix-ui/react-dialog";
import { Code2, GitPullRequest, LockKeyhole, X } from "lucide-react";
import { useEffect, useState } from "react";
import { alertKind, api, type Alert, type Notification } from "./api";
import { splitCutoffName, useBusinessNow } from "./clock";
import { RunbookPanel } from "./RunbookPanel";
import { ProposeSheet } from "./pages/ProposalsPage";
import {
  Badge, Button, ChannelIcon, CutoffChip, EscalationBadge, KindBadge, Notice, RuleKindChip,
  alertStatusLabel, channelLabel, statusBadge, timeAgo, toneFor,
} from "./ui";

interface Props {
  id: string;
  live?: Alert;
  now: number;
  onClose: () => void;
  onAction: (alert: Alert, action: "ack" | "resolve") => Promise<Alert>;
}

const deliveryLabels: Record<Notification["status"], string> = {
  pending: "Pending", sending: "Sending", sent: "Sent", retry: "Retrying",
  dead: "Failed", standby: "Fallback on standby", cancelled: "Cancelled",
  held: "Held", suppressed: "Suppressed",
};

function latestAlert(...candidates: (Alert | undefined)[]) {
  let latest: Alert | undefined;
  for (const candidate of candidates) {
    if (!candidate) continue;
    if (!latest || Date.parse(candidate.updatedAt) >= Date.parse(latest.updatedAt)) latest = candidate;
  }
  return latest;
}

export function AlertDrawer({ id, live, now, onClose, onAction }: Props) {
  const bizNow = useBusinessNow(10_000);
  const [detail, setDetail] = useState<{ alert: Alert; notifications: Notification[] } | null>(null);
  const [acted, setActed] = useState<Alert>();
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"ack" | "resolve" | null>(null);
  const [proposing, setProposing] = useState<string | null>(null);
  const [proposeError, setProposeError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let timer: number;
    const load = async () => {
      try {
        const result = await api.alert(id);
        if (!cancelled) {
          setDetail(result);
          setLoadError(null);
        }
      } catch (error) {
        if (!cancelled) setLoadError((error as Error).message);
      } finally {
        if (!cancelled) timer = window.setTimeout(load, 3000);
      }
    };
    void load();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [id]);

  const current = latestAlert(detail?.alert, live, acted);
  // A streaming count update does not change the alert's status timestamp.
  const alert = current && live ? {
    ...current,
    count: Math.max(current.count, live.count),
    lastSeen: Date.parse(live.lastSeen) >= Date.parse(current.lastSeen) ? live.lastSeen : current.lastSeen,
  } : current;
  const user = useConsoleSession()?.user;
  const canAct = alert != null && canManageAlert(user, alert.team);
  const sample = detail?.alert ?? alert;

  const perform = async (action: "ack" | "resolve") => {
    if (!alert || busy) return;
    setBusy(action);
    setActionError(null);
    try {
      setActed(await onAction(alert, action));
    } catch (error) {
      setActionError((error as Error).message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog.Root open onOpenChange={(open) => { if (!open) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content
          className="fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col overflow-hidden border-l border-[var(--console-line)] bg-[var(--console-surface)] text-[var(--console-ink)] shadow-2xl"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const trigger = document.querySelector<HTMLElement>(`[data-alert-link="${CSS.escape(id)}"]`);
            (trigger ?? document.getElementById("alert-queue-title"))?.focus({ preventScroll: true });
          }}
        >
          <header className="shrink-0 px-4 pt-4 sm:px-5 sm:pt-5">
            <div className="flex min-h-9 items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2">
                {alert && <>
                  <Badge className={`${toneFor(alert.color).badge} capitalize`}>{alert.severity}</Badge>
                  <Badge className={statusBadge[alert.status]}>{alertStatusLabel[alert.status]}</Badge>
                  <RuleKindChip kind={alertKind(alert)} />
                  {alert.cutoffName && alert.cutoffAt && (
                    <CutoffChip name={alert.cutoffName} at={alert.cutoffAt} now={bizNow} resolved={alert.status === "resolved"} />
                  )}
                  <EscalationBadge level={alert.escalationLevel ?? 0} />
                </>}
              </div>
              <Dialog.Close asChild>
                <button type="button" aria-label="Close alert details" className="console-muted grid size-9 shrink-0 place-items-center rounded-md hover:bg-[var(--console-canvas)]">
                  <X size={19} aria-hidden="true" />
                </button>
              </Dialog.Close>
            </div>
          </header>

          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
            <div className="border-b border-[var(--console-line)] px-4 pt-2 pb-4 sm:px-5 sm:pb-5">
              <Dialog.Title className="text-xl font-semibold tracking-tight break-words">{alert?.title || "Alert details"}</Dialog.Title>
              <Dialog.Description className="console-muted mt-1 text-sm whitespace-pre-wrap break-words">
                {alert?.body || "Review the alert, its event, and notification deliveries."}
              </Dialog.Description>
            </div>
            {loadError && <div className="p-4"><Notice tone="error">Could not refresh alert details. {loadError}</Notice></div>}
            {!alert && !loadError && <p role="status" className="console-muted p-5 text-sm">Loading alert…</p>}
            {alert && (
              <>
                <section className="border-b border-[var(--console-line)] p-4 sm:p-5" aria-labelledby="alert-details-heading">
                  <h3 id="alert-details-heading" className="mb-3 text-sm font-semibold">Event details</h3>
                  <dl className="grid grid-cols-2 gap-x-5 gap-y-3 text-sm">
                    <Info
                      label={alertKind(alert) === "aggregate" ? "Peak events in window" : "Occurrences"}
                      value={alert.count.toLocaleString()}
                    />
                    <Info label="Impact (0-100)" value={String(alert.impact)} />
                    <Info label="Team" value={alert.team} />
                    <Info label="First seen" value={`${new Date(alert.firstSeen).toLocaleString()} (${timeAgo(alert.firstSeen, now)})`} />
                    <Info label="Last seen" value={`${new Date(alert.lastSeen).toLocaleString()} (${timeAgo(alert.lastSeen, now)})`} />
                    <Info label="Source" value={alert.sourceId === "kardan.absence" ? "kardan (expected event missing)" : alert.sourceId} mono />
                    <Info label="Rule" value={alert.ruleId} mono />
                    {alert.cutoffName && alert.cutoffAt && (
                      <Info
                        label="Cut-off"
                        value={`${splitCutoffName(alert.cutoffName)[0]} · ${new Date(alert.cutoffAt).toLocaleString([], {
                          weekday: "short", hour: "2-digit", minute: "2-digit", timeZoneName: "short",
                        })}`}
                      />
                    )}
                    {alert.escalatedAt && <Info label="Escalated" value={`Level ${alert.escalationLevel}, ${timeAgo(alert.escalatedAt, now)}`} />}
                    {alert.ackedBy && <Info label="Acknowledged by" value={alert.ackedBy} />}
                    {alert.resolvedBy && <Info label="Resolved by" value={alert.resolvedBy} />}
                    {(alert.fields ?? []).map((field) => <Info key={field.label} label={field.label} value={field.value} mono sensitive={field.sensitive} />)}
                  </dl>
                </section>
                <RunbookPanel alertId={alert.id} canAct={canAct} resolved={alert.status === "resolved"} now={now} />
                <section className="border-b border-[var(--console-line)] p-4 sm:p-5" aria-labelledby="alert-delivery-heading">
                  <h3 id="alert-delivery-heading" className="text-sm font-semibold">Notification deliveries</h3>
                  {detail?.notifications.length === 0 && <p className="console-muted mt-2 text-sm">Shown in the live feed only (no external channels on this rule).</p>}
                  {!detail && !loadError && <p role="status" className="console-muted mt-2 text-sm">Loading deliveries…</p>}
                  <ul className="mt-1 divide-y divide-[var(--console-line)]">
                    {detail?.notifications.map((notification) => <Delivery key={notification.id} notification={notification} />)}
                  </ul>
                </section>
                {(sample?.sampleRaw || sample?.sample !== undefined) && (
                  <details className="group p-4 sm:p-5">
                    <summary className="cursor-pointer rounded text-sm font-semibold">
                      <Code2 size={16} className="console-muted mx-1 inline-block" aria-hidden="true" /> First event payload
                    </summary>
                    {sample.sampleRaw && <Payload label={`Original · ${sample.sampleFormat || "text"}`} value={sample.sampleRaw} />}
                    {sample.sample !== undefined && (
                      <Payload
                        label={
                          sample.sourceId === "kardan.absence"
                            ? "What was expected (no event arrived)"
                            : alertKind(sample) === "aggregate"
                              ? "Event that crossed the threshold"
                              : "Parsed data"
                        }
                        value={JSON.stringify(sample.sample, null, 2)}
                      />
                    )}
                  </details>
                )}
              </>
            )}
          </div>

          {alert && (
            <footer className="space-y-2 border-t border-[var(--console-line)] p-4 sm:p-5">
              {actionError && <div role="alert"><Notice tone="error">{actionError}</Notice></div>}
              <div className="flex flex-wrap items-center justify-end gap-2">
                <button type="button" className="mr-auto inline-flex items-center gap-1.5 text-sm font-medium text-[var(--console-accent)] hover:underline"
                  onClick={() => {
                    setProposeError(null);
                    api.rule(alert.ruleId).then((d) => setProposing(d.yaml)).catch((e: Error) => setProposeError(e.message));
                  }}>
                  <GitPullRequest size={15} aria-hidden="true" /> Propose a fix to this rule
                </button>
                {canAct && alert.status === "open" && <Button variant="primary" disabled={busy !== null} onClick={() => void perform("ack")}>{busy === "ack" ? "Acknowledging…" : "Acknowledge"}</Button>}
                {canAct && alert.status !== "resolved" && <Button variant={alert.status === "acked" ? "primary" : "secondary"} disabled={busy !== null} onClick={() => void perform("resolve")}>{busy === "resolve" ? "Resolving…" : "Resolve alert"}</Button>}
              </div>
              {proposeError && <Notice tone="error">{proposeError}</Notice>}
            </footer>
          )}
          {proposing !== null && alert && (
            <ProposeSheet kind="rule" target={alert.ruleId} body={proposing} alertId={alert.id}
              onClose={() => setProposing(null)} onDone={() => { setProposing(null); setProposeError(null); }} />
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Delivery({ notification: n }: { notification: Notification }) {
  return (
    <li className={`flex flex-wrap items-start justify-between gap-2 py-2.5 text-sm ${n.fallbackFor ? "pl-4" : ""}`}>
      <div className="min-w-0 flex-1">
        <div className="flex items-start gap-2 break-words">
          <ChannelIcon channel={n.channel} className="console-muted mt-0.5 shrink-0" />
          <div className="min-w-0">
            <span className="font-medium capitalize">{channelLabel(n.channel)}</span> · {n.recipient}
            {n.address && <div className="console-muted break-all text-xs">{n.address}</div>}
          </div>
        </div>
        {n.reason && <div className="console-muted mt-1 text-xs">{n.reason}</div>}
        {n.lastError && n.status !== "sent" && <div className="mt-1 text-xs break-words text-red-700 dark:text-red-300">{n.lastError}</div>}
      </div>
      <div className="flex items-center gap-2">
        <KindBadge kind={n.kind} />
        {n.attempts > 1 && <span className="console-muted text-xs">{n.attempts} attempts</span>}
        <Badge className={statusBadge[n.status] ?? statusBadge.pending}>{deliveryLabels[n.status] ?? n.status}</Badge>
      </div>
    </li>
  );
}

function Payload({ label, value }: { label: string; value: string }) {
  return (
    <div className="mt-3">
      <h4 className="console-muted text-xs font-medium">{label}</h4>
      <pre className="mt-1.5 max-h-72 overflow-auto rounded-md bg-[var(--console-canvas)] p-3 font-mono text-xs">{value}</pre>
    </div>
  );
}

function Info({ label, value, mono, sensitive }: { label: string; value: string; mono?: boolean; sensitive?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="console-muted flex items-center gap-1 text-xs">
        {sensitive && <LockKeyhole size={12} aria-label="Sensitive field" />}{label}
      </dt>
      <dd className={`mt-0.5 break-words ${mono ? "font-mono text-xs" : ""}`}>{value}</dd>
    </div>
  );
}
