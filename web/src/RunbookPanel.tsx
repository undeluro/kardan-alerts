import { useConsoleSession } from "./ConsoleSession";
import { ArrowUpRight } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, getActor, type AuditEntry, type Run, type RunbookView, type StepView } from "./api";
import { Badge, Button, humanEvery, Notice, timeAgo } from "./ui";

const formatLabel: Record<string, string> = {
  yaml: "YAML",
  markdown: "Markdown",
  checklist: "Checklist",
  link: "Wiki link",
  text: "Free text",
};

const runTone: Record<string, string> = {
  awaiting_approval: "bg-fuchsia-50 text-fuchsia-700 ring-fuchsia-600/20 dark:bg-fuchsia-500/10 dark:text-fuchsia-300 dark:ring-fuchsia-400/30",
  queued: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  running: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  succeeded: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  done: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  failed: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30",
  rejected: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30",
  skipped: "bg-gray-50 text-gray-500 ring-gray-300 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700",
};

const dotTone: Record<string, string> = {
  awaiting_approval: "bg-fuchsia-500 text-white",
  queued: "bg-sky-500 text-white",
  running: "bg-sky-500 text-white animate-pulse",
  succeeded: "bg-emerald-500 text-white",
  done: "bg-emerald-500 text-white",
  failed: "bg-red-500 text-white",
  rejected: "bg-red-500 text-white",
  skipped: "bg-gray-300 text-gray-600 dark:bg-gray-700 dark:text-gray-300",
};

const statusText: Record<string, string> = {
  awaiting_approval: "waiting for approval",
  queued: "queued",
  running: "running",
  succeeded: "succeeded",
  done: "done",
  failed: "failed",
  rejected: "rejected",
  skipped: "skipped",
};

const riskTone: Record<string, string> = {
  high: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30",
  medium: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
  low: "bg-gray-50 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700",
};

const chip = "bg-gray-50 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700";
const final = (r?: Run) => !r || ["succeeded", "failed", "rejected", "done", "skipped"].includes(r.status);

/** The alert's runbooks with live step state, and the audit timeline. */
export function RunbookPanel({ alertId, resolved, now, canAct }: { alertId: string; resolved: boolean; now: number; canAct: boolean }) {
  const [views, setViews] = useState<RunbookView[] | null>(null);
  const [timeline, setTimeline] = useState<AuditEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const user = useConsoleSession()?.user;
  const actor = user?.name ?? getActor();
  const anonymous = actor === "anonymous";

  const load = useCallback(() => {
    api
      .alertRunbooks(alertId)
      .then(setViews)
      .catch(() => setViews([]));
    api
      .timeline(alertId)
      .then(setTimeline)
      .catch(() => {});
  }, [alertId]);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 2000);
    return () => window.clearInterval(t);
  }, [load]);

  const act = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    setError(null);
    try {
      await fn();
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <section id="runbook" className="border-b border-gray-200 p-5 dark:border-gray-800">
        <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">What to do next</h3>
        {views === null && <p className="mt-2 text-sm text-gray-500">Loading…</p>}
        {views?.length === 0 && (
          <p className="mt-2 text-sm text-gray-500">
            No runbook is attached to this alert.
            {(!user || user.admin) && <> Add one on the <a href="/runbooks" className="text-amber-600 hover:underline">Runbooks</a> page.</>}
          </p>
        )}
        {anonymous && views && views.length > 0 && !resolved && (
          <div className="mt-2">
            <Notice tone="info">Choose who you are in "Acting as" (top right) to run steps or approve them.</Notice>
          </div>
        )}
        {error && (
          <div className="mt-2">
            <Notice tone="error">{error}</Notice>
          </div>
        )}
        {views?.map((v) => (
          <div key={v.id} className="mt-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-gray-900 dark:text-gray-100">{v.name}</span>
              <Badge className={chip}>{formatLabel[v.format] ?? v.format}</Badge>
              {v.auto && <Badge className={runTone.queued}>runs by itself</Badge>}
              <span className="text-xs text-gray-500">{v.why}</span>
            </div>
            {v.summary && <p className="mt-1 text-sm text-gray-600 italic dark:text-gray-300">{v.summary}</p>}
            {v.url && (
              <a href={v.url} target="_blank" rel="noreferrer" className="mt-1 block truncate text-sm text-amber-700 hover:underline dark:text-amber-400">
                {v.url} <ArrowUpRight size={13} aria-hidden="true" className="inline-block" />
              </a>
            )}
            <ol className="mt-2 space-y-2">
              {v.steps.map((s) => (
                <Step
                  key={s.index}
                  s={s}
                  now={now}
                  resolved={resolved}
                  canAct={canAct}
                  busy={busy}
                  actor={actor}
                  actorId={user?.id}
                  onRun={() => act(`run-${v.id}-${s.index}`, () => api.runStep(alertId, v.id, s.index, actor))}
                  onApprove={(id) => act(`ok-${id}`, () => api.approve(id, actor))}
                  onReject={(id) => act(`no-${id}`, () => api.reject(id, actor))}
                  keyPrefix={`${v.id}-${s.index}`}
                />
              ))}
            </ol>
          </div>
        ))}
      </section>

      <section className="border-b border-gray-200 p-5 dark:border-gray-800">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Who did what</h3>
          <a href={api.reportURL(alertId)} className="text-xs font-medium text-amber-700 hover:underline dark:text-amber-400" download>
            Export incident report (.md)
          </a>
        </div>
        {timeline.length === 0 ? (
          <p className="mt-2 text-sm text-gray-500">Nothing recorded yet. Acknowledgements, approvals and runbook actions appear here.</p>
        ) : (
          <ul className="mt-2 space-y-1.5 text-sm">
            {timeline.map((e) => (
              <li key={e.seq} className="flex gap-2">
                <span className="w-16 shrink-0 text-xs text-gray-500 tabular-nums" title={new Date(e.at).toLocaleString()}>
                  {new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" })}
                </span>
                <span className="min-w-0 flex-1">
                  <b className="font-medium">{e.actor}</b> <span className="text-gray-600 dark:text-gray-300">{describe(e)}</span>
                </span>
                <a href={`/audit#${e.seq}`} className="shrink-0 font-mono text-[10px] text-gray-400 hover:text-gray-600" title={`audit entry ${e.seq}, hash ${e.hash}`}>
                  #{e.seq}
                </a>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}

function Step({
  s,
  now,
  resolved,
  canAct,
  busy,
  actor,
  actorId,
  onRun,
  onApprove,
  onReject,
  keyPrefix,
}: {
  s: StepView;
  now: number;
  resolved: boolean;
  canAct: boolean;
  busy: string | null;
  actor: string;
  actorId?: string;
  onRun: () => void;
  onApprove: (id: string) => void;
  onReject: (id: string) => void;
  keyPrefix: string;
}) {
  const r = s.run;
  const manual = !s.action;
  const active = r && !final(r);
  const isRequester = r && (r.requestedById && actorId ? r.requestedById === actorId : sameName(r.requestedBy, actor));
  return (
    <li className="flex gap-3">
      <span
        className={`mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full text-xs font-semibold ${
          r ? dotTone[r.status] : "bg-gray-100 text-gray-600 ring-1 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700"
        }`}
      >
        {r && (r.status === "succeeded" || r.status === "done") ? "✓" : s.index + 1}
      </span>
      <div className="min-w-0 flex-1">
        <div className={`text-sm ${r?.status === "skipped" ? "text-gray-400 line-through" : "text-gray-900 dark:text-gray-100"}`}>
          {s.if && <span className="mr-1 rounded bg-amber-50 px-1 text-xs text-amber-800 dark:bg-amber-500/10 dark:text-amber-300">if {s.if}</span>}
          {s.label || s.text}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-1.5">
          {s.action && <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-gray-800 dark:text-gray-300">{s.action}</code>}
          {!s.action && s.kind === "check" && <Badge className={chip}>check</Badge>}
          {s.risk && s.action && <Badge className={riskTone[s.risk] ?? chip}>{s.risk} risk</Badge>}
          {s.needs === "approval" && <Badge className={runTone.awaiting_approval}>four-eyes approval</Badge>}
          {s.inferred && (
            <Badge className={chip}>
              <span title="Kardan read this action from the text. Check it on the Runbooks page.">read from text</span>
            </Badge>
          )}
          {s.every && <Badge className={chip}>repeats every {humanEvery(s.every)}</Badge>}
          {s.then === "resolve" && <Badge className={runTone.succeeded}>resolves the alert</Badge>}
          {s.params &&
            Object.entries(s.params).map(([k, v]) => (
              <span key={k} className="font-mono text-[11px] text-gray-500 dark:text-gray-400">
                {k}={v || "∅"}
              </span>
            ))}
        </div>
        {s.problem && !r && <div className="mt-1 text-xs text-red-600 dark:text-red-400">Cannot run: {s.problem}</div>}
        {r && (
          <div className="mt-1.5 rounded-md bg-gray-50 px-2.5 py-1.5 text-xs ring-1 ring-gray-200 dark:bg-gray-900 dark:ring-gray-800">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <Badge className={runTone[r.status]}>{statusText[r.status]}</Badge>
              <span className="text-gray-500">
                {r.status === "done" ? "by" : "requested by"} {r.requestedBy}
                {r.approvedBy && <> · approved by {r.approvedBy}</>}
                {r.decidedBy && r.status === "rejected" && <> · rejected by {r.decidedBy}</>}
                {r.maxAttempts > 1 && <> · attempt {r.attempts}/{r.maxAttempts}</>} · {timeAgo(r.createdAt, now)}
              </span>
            </div>
            {r.status === "awaiting_approval" && (
              <div className="mt-1.5 flex flex-wrap items-center gap-2">
                <span className="text-gray-600 dark:text-gray-300">
                  A second person must approve (SMS: <b className="font-mono">APPROVE {r.code}</b>).
                </span>
                {canAct && !resolved && (
                  <>
                    <Button onClick={() => onApprove(r.id)} variant="primary" disabled={busy !== null || !!isRequester}>
                      Approve
                    </Button>
                    <Button onClick={() => onReject(r.id)} disabled={busy !== null || !!isRequester}>
                      Reject
                    </Button>
                    {isRequester && <span className="text-gray-500">You requested it, so someone else approves.</span>}
                  </>
                )}
              </div>
            )}
            {(r.output || r.reason) && <div className="mt-1 font-mono break-words text-gray-700 dark:text-gray-300">{r.output || r.reason}</div>}
          </div>
        )}
        {canAct && !resolved && !active && (
          <div className="mt-1.5">
            {manual && r?.status !== "done" && (
              <Button onClick={onRun} disabled={busy === `run-${keyPrefix}` || actor === "anonymous"}>
                Mark done
              </Button>
            )}
            {!manual && s.runnable && (
              <Button onClick={onRun} disabled={busy === `run-${keyPrefix}` || actor === "anonymous"} variant={r ? "secondary" : "primary"}>
                {s.needs === "approval" ? (r ? "Request again" : "Request approval") : r ? "Run again" : "Run"}
              </Button>
            )}
          </div>
        )}
      </div>
    </li>
  );
}

function sameName(a: string, b: string) {
  const n = (x: string) => x.trim().toLowerCase();
  return n(a) === n(b) || n(a).split(" ")[0] === n(b);
}

/** Human description of an audit entry. */
export function describe(e: AuditEntry): string {
  const d = e.detail ?? {};
  const label = typeof d.label === "string" ? d.label : "";
  const out = typeof d.output === "string" ? d.output : "";
  const via = typeof d.via === "string" ? ` (${d.via})` : "";
  switch (e.action) {
    case "alert.ack":
      return `acknowledged the alert${via}`;
    case "alert.resolve":
      return `resolved the alert${via}${typeof d.reason === "string" ? `: ${d.reason}` : ""}`;
    case "runbook.step.requested":
      return `ran step ${d.step}: ${label}`;
    case "runbook.step.approval_requested":
      return `asked for approval of step ${d.step}: ${label}`;
    case "runbook.step.done":
      return `marked step ${d.step} done: ${label}`;
    case "runbook.step.skipped":
      return `skipped step ${d.step}${typeof d.reason === "string" ? ` (${d.reason})` : ""}`;
    case "action.approved":
      return `approved: ${label}${via}`;
    case "action.rejected":
      return `rejected: ${label}${via}`;
    case "action.approval_refused":
      return `could not approve${via}: ${String(d.reason ?? "")}`;
    case "action.succeeded":
      return `${label} → ${out}`;
    case "action.failed":
      return `${label} failed → ${out}`;
    case "action.retry_scheduled":
      return `${label} failed (attempt ${d.attempt}), retrying → ${out}`;
    case "action.skipped":
      return `${label} not run: ${out}`;
    default:
      return e.action;
  }
}
