import * as Dialog from "@radix-ui/react-dialog";
import { Check, ChevronDown, ChevronRight, GitPullRequest, Radar, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, getActor, type Proposal } from "../api";
import { DiffView, SourceBadge } from "../Assist";
import { Link } from "../router";
import { Badge, Button, Notice, PageHeader, inputClass, timeAgo } from "../ui";

const filters = [["open", "Open"], ["accepted", "Accepted"], ["rejected", "Rejected"], ["", "All"]] as const;

export function ProposalsPage({ now, onToast }: { now: number; onToast: (m: string) => void }) {
  const [status, setStatus] = useState<string>("open");
  const [list, setList] = useState<Proposal[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [analyzing, setAnalyzing] = useState(false);

  const load = useCallback(() => {
    api.proposals(status).then((l) => { setList(l); setError(null); }).catch((e: Error) => setError(e.message));
  }, [status]);
  useEffect(load, [load]);

  const analyze = async () => {
    setAnalyzing(true);
    try {
      const r = await api.analyzeFatigue(7);
      onToast(r.created.length
        ? `${r.created.length} new ${r.created.length === 1 ? "suggestion" : "suggestions"} from ${r.rulesAnalyzed} rules`
        : `Looked at ${r.rulesAnalyzed} rules over ${r.days} days: nothing new to suggest${r.alreadyOpen ? ` (${r.alreadyOpen} already open)` : ""}`);
      setStatus("open");
      load();
    } catch (e) {
      onToast((e as Error).message);
    } finally {
      setAnalyzing(false);
    }
  };

  return (
    <>
      <PageHeader title="Proposals"
        subtitle="Suggested changes to rules and runbooks, from people, from Kardan's fatigue analysis, or drafted by Claude. Nothing changes until someone other than the proposer accepts."
        actions={<Button onClick={() => void analyze()} disabled={analyzing}><Radar size={14} aria-hidden="true" /> {analyzing ? "Analysing…" : "Analyse alert fatigue"}</Button>} />
      <div className="mb-3 flex flex-wrap gap-1" role="group" aria-label="Proposal status">
        {filters.map(([v, label]) => (
          <button key={label} type="button" className="console-toggle" aria-pressed={status === v} onClick={() => setStatus(v)}>{label}</button>
        ))}
      </div>
      {error && <Notice tone="error">{error}</Notice>}
      {!list ? <p role="status" className="console-muted text-sm">Loading…</p> : list.length === 0 ? (
        <div className="console-panel console-muted grid justify-items-center gap-2 px-4 py-10 text-center text-sm">
          <GitPullRequest size={26} aria-hidden="true" />
          <p>{status === "open" ? "No open proposals. Propose a fix from an alert, or run the fatigue analysis." : "Nothing here yet."}</p>
        </div>
      ) : (
        <ul className="space-y-2">
          {list.map((p) => (
            <ProposalCard key={p.id} p={p} now={now} expanded={open === p.id} onToggle={() => setOpen(open === p.id ? null : p.id)}
              onDecided={(msg) => { onToast(msg); load(); }} />
          ))}
        </ul>
      )}
    </>
  );
}

function ProposalCard({ p, now, expanded, onToggle, onDecided }: {
  p: Proposal; now: number; expanded: boolean; onToggle: () => void; onDecided: (msg: string) => void;
}) {
  const [current, setCurrent] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (expanded && current === null) api.proposal(p.id).then((d) => setCurrent(d.current)).catch(() => setCurrent(""));
  }, [expanded, current, p.id]);
  const me = getActor();
  const decide = async (accept: boolean) => {
    setBusy(true);
    setError(null);
    try {
      await api.decideProposal(p.id, accept, note);
      onDecided(accept ? `Accepted: ${p.title}. ${p.kind === "rule" ? "Rule" : "Runbook"} ${p.target} updated.` : `Rejected: ${p.title}`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const targetLink = p.kind === "rule" ? `/rules/${encodeURIComponent(p.target)}` : `/runbooks/${encodeURIComponent(p.target)}`;
  return (
    <li className="console-panel">
      <button type="button" onClick={onToggle} aria-expanded={expanded} className="flex w-full items-start gap-3 p-3 text-left sm:p-4">
        {expanded ? <ChevronDown size={17} className="console-muted mt-0.5 shrink-0" aria-hidden="true" /> : <ChevronRight size={17} className="console-muted mt-0.5 shrink-0" aria-hidden="true" />}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-semibold">{p.title}</span>
            <SourceBadge source={p.source} />
            {p.status !== "open" && (
              <Badge className={p.status === "accepted" ? "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300" : "bg-gray-100 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300"}>
                {p.status}
              </Badge>
            )}
          </div>
          <div className="console-muted mt-0.5 text-xs">
            {p.kind} <code className="font-mono">{p.target}</code> · by {p.createdBy} · {timeAgo(p.createdAt, now)}
            {p.decidedBy && <> · {p.status} by {p.decidedBy}</>}
          </div>
        </div>
      </button>
      {expanded && (
        <div className="space-y-3 border-t border-[var(--console-line)] p-3 sm:p-4">
          {p.rationale && <p className="text-sm whitespace-pre-wrap">{p.rationale}</p>}
          <div className="flex flex-wrap gap-3 text-sm">
            <Link to={targetLink} className="font-medium text-[var(--console-accent)] hover:underline">Open {p.kind}</Link>
            {p.alertId && <Link to={`/alerts/${p.alertId}`} className="font-medium text-[var(--console-accent)] hover:underline">Alert that prompted it</Link>}
          </div>
          {current === null ? <p className="console-muted text-sm">Loading diff…</p> : <DiffView before={current} after={p.body} />}
          {p.decisionNote && <p className="text-sm"><b>Note:</b> {p.decisionNote}</p>}
          {p.status === "open" && (
            <div className="space-y-2">
              {error && <Notice tone="error">{error}</Notice>}
              <input aria-label="Review note" className={inputClass} placeholder="Note for the record (optional)" value={note} onChange={(e) => setNote(e.target.value)} />
              <div className="flex flex-wrap items-center justify-end gap-2">
                {me === p.createdBy && <span className="console-muted text-xs">You proposed this; someone else has to accept it.</span>}
                <Button disabled={busy} onClick={() => void decide(false)}><X size={14} aria-hidden="true" /> Reject</Button>
                <Button variant="primary" disabled={busy || me === p.createdBy || me === "anonymous"} onClick={() => void decide(true)}>
                  <Check size={14} aria-hidden="true" /> Accept and apply
                </Button>
              </div>
              {me === "anonymous" && <p className="console-muted text-right text-xs">Set your name in "Acting as" to review.</p>}
            </div>
          )}
        </div>
      )}
    </li>
  );
}

/** Lets anyone propose a change to a rule or runbook for review. */
export function ProposeSheet({ kind, target, body, alertId, onClose, onDone }: {
  kind: "rule" | "runbook";
  target: string;
  body: string;
  alertId?: string;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [title, setTitle] = useState("");
  const [rationale, setRationale] = useState("");
  const [text, setText] = useState(body);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setText(body), [body]);
  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.propose({ kind, target, title, rationale, body: text, alertId });
      onDone("Proposal sent for review");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[60] bg-black/60" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-[70] flex w-full max-w-2xl flex-col border-l border-[var(--console-line)] bg-[var(--console-surface)] text-[var(--console-ink)] shadow-2xl">
          <header className="border-b border-[var(--console-line)] p-4 sm:p-5">
            <Dialog.Title className="text-lg font-semibold">Propose a change to {kind} {target}</Dialog.Title>
            <Dialog.Description className="console-muted mt-0.5 text-sm">Someone else reviews the diff and applies it. Nothing changes until then.</Dialog.Description>
          </header>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4 sm:p-5">
            {error && <Notice tone="error">{error}</Notice>}
            <label className="block text-sm"><span className="font-medium">What should change</span>
              <input className={`${inputClass} mt-1`} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Raise the threshold to 10 rejections" autoFocus />
            </label>
            <label className="block text-sm"><span className="font-medium">Why</span>
              <textarea className={`${inputClass} mt-1`} rows={3} value={rationale} onChange={(e) => setRationale(e.target.value)} placeholder="What happened on this alert, and how the change helps" />
            </label>
            <label className="block text-sm"><span className="font-medium">Proposed {kind === "rule" ? "rule YAML" : "runbook"}</span>
              <textarea className="mt-1 block w-full rounded-md bg-gray-950 p-3 font-mono text-[13px] text-gray-100 ring-1 ring-gray-800" rows={16} spellCheck={false}
                value={text} onChange={(e) => setText(e.target.value)} />
            </label>
            <DiffView before={body} after={text} />
          </div>
          <footer className="flex justify-end gap-2 border-t border-[var(--console-line)] p-4 sm:p-5">
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" disabled={busy || !title.trim() || text === body} onClick={() => void submit()}>{busy ? "Sending…" : "Send for review"}</Button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
