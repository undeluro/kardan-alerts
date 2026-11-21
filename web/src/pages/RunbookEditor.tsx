import { useEffect, useRef, useState } from "react";
import { api, ApiError, type ParseResult, type RunbookDraft, type Team } from "../api";
import { navigate, Link } from "../router";
import { ArrowUpRight } from "lucide-react";
import { Badge, Button, Card, humanEvery, Notice, Select, inputClass } from "../ui";
import { describeCondition, formatLabel } from "./RunbooksPage";

const chip = "bg-gray-50 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700";
const riskTone: Record<string, string> = {
  high: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300",
  medium: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300",
  low: chip,
};

// Shapes from the brief's Appendix B, to try the parser quickly.
const examples: Record<string, { source: string; name?: string; team?: string; matches?: string }> = {
  "Free text": {
    source:
      "When pricing latency goes over a second, it's usually the cache. Restart the pricing-engine instance named in the alert, wait 2 minutes, and if latency is still high, call the pricing on-call person.",
    name: "Pricing latency (pasted)",
    team: "pricing",
    matches: '$service: "pricing-engine-*"',
  },
  Markdown: {
    source:
      "# HighErrorRate on payments-api\n1. Open the payments dashboard and check if errors are in one region only.\n2. If one region: fail traffic over to the other region (needs approval from the team lead).\n3. If all regions: check the last deployment and roll it back.\n4. Post an update in the #payments-incidents channel every 15 minutes.\n",
    team: "payments",
    matches: "alertname: HighErrorRate",
  },
  Checklist: {
    source: "# Payment stuck in repair\n- [ ] Check the payment in the payments hub\n- [ ] Open a ticket for the payments team\n- [ ] Resubmit the payment (four-eyes)\n",
    team: "payments",
    matches: "status: RJCT",
  },
  Link: { source: "Settlement fails runbook: https://wiki.example.com/ops/settlement-fails", team: "settlements", matches: "type: WORKFLOW_FAILED" },
};

export function RunbookEditor({ id, teams }: { id: string | null; teams: Team[] }) {
  const [draft, setDraft] = useState<RunbookDraft>({ source: "" });
  const [editable, setEditable] = useState(id === null);
  const [version, setVersion] = useState<number | undefined>();
  const [preview, setPreview] = useState<ParseResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [loaded, setLoaded] = useState(id === null);
  const [override, setOverride] = useState(false); // editing a file runbook in the UI
  const [origin, setOrigin] = useState<string | undefined>();
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (id === null) return;
    api
      .runbook(id)
      .then((d) => {
        const rb = d.runbook;
        setDraft({
          source: rb.source,
          id: rb.id,
          name: rb.name !== rb.id ? rb.name : "",
          team: rb.team ?? "",
          matches: "",
          rules: rb.rules ?? [],
          auto: rb.auto,
        });
        setEditable(d.editable);
        setVersion(d.version);
        setOrigin(rb.origin);
        setLoaded(true);
      })
      .catch((e: Error) => setError(e.message));
  }, [id]);

  // Live preview of how Kardan reads the text.
  useEffect(() => {
    if (!loaded) return;
    window.clearTimeout(timer.current);
    if (!draft.source.trim()) {
      setPreview(null);
      return;
    }
    timer.current = window.setTimeout(() => {
      api
        .parseRunbook(draft)
        .then(setPreview)
        .catch((e: Error) => setPreview({ valid: false, error: e.message }));
    }, 350);
    return () => window.clearTimeout(timer.current);
  }, [draft, loaded]);

  const set = (patch: Partial<RunbookDraft>) => setDraft((d) => ({ ...d, ...patch }));

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const body = { ...draft, version };
      const res = id && !override ? await api.updateRunbook(id, body) : await api.createRunbook(body);
      setOverride(false);
      setVersion(res.version);
      setEditable(true);
      navigate(`/runbooks/${encodeURIComponent(res.runbook!.id)}`, true);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    const overrides = (preview?.runbook?.notes ?? []).some((n) => n.startsWith("overrides"));
    const msg = overrides ? `Remove the UI version of ${id}? The file version applies again.` : `Delete runbook ${id}? Alerts stop showing it.`;
    if (!id || !window.confirm(msg)) return;
    try {
      await api.deleteRunbook(id);
      navigate("/runbooks");
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const rb = preview?.runbook;
  return (
    <>
      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <div>
          <Link to="/runbooks" className="text-sm text-gray-500 hover:text-gray-800 dark:hover:text-gray-200">
            ← Runbooks
          </Link>
          <h1 className="mt-1 text-xl font-semibold">{id ? (rb?.name ?? id) : "New runbook"}</h1>
          {!editable && id && (
            <p className="mt-1 text-sm text-gray-500">
              This runbook is the file <code className="font-mono">{(origin ?? "").replace("file:", "config/runbooks/")}</code>. Edit the file, or
              edit it here: the UI version then overrides the file until you remove it.
            </p>
          )}
        </div>
        <div className="flex gap-2">
          {editable && id && <Button onClick={() => void remove()}>Delete</Button>}
          {!editable && id && (
            <Button
              onClick={() => {
                setEditable(true);
                setOverride(true);
                setVersion(undefined);
              }}
            >
              Edit here
            </Button>
          )}
          {(editable || id === null) && (
            <Button variant="primary" onClick={() => void save()} disabled={saving || !preview?.valid}>
              {saving ? "Saving…" : id ? "Save" : "Create runbook"}
            </Button>
          )}
        </div>
      </div>
      {error && (
        <div className="mb-4">
          <Notice tone="error">{error}</Notice>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-3">
          {id === null && (
            <div className="flex flex-wrap items-center gap-2 text-xs text-gray-500">
              Try a shape:
              {Object.entries(examples).map(([k, ex]) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => set({ source: ex.source, name: ex.name ?? "", team: ex.team ?? "", matches: ex.matches ?? "" })}
                  className="rounded-md px-2 py-1 ring-1 ring-gray-300 hover:bg-gray-50 dark:ring-gray-700 dark:hover:bg-gray-800"
                >
                  {k}
                </button>
              ))}
            </div>
          )}
          <textarea
            value={draft.source}
            onChange={(e) => set({ source: e.target.value })}
            readOnly={!editable}
            spellCheck={false}
            placeholder={"Paste a runbook: YAML steps, a Markdown page, a checklist, a wiki link, or plain sentences such as\n\"Restart the service named in the alert, wait 2 minutes, and if it is still failing, call the on-call person.\""}
            className={`${inputClass} h-72 font-mono text-xs leading-relaxed`}
          />
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="text-sm">
              <span className="text-gray-600 dark:text-gray-300">Name</span>
              <input value={draft.name ?? ""} onChange={(e) => set({ name: e.target.value })} readOnly={!editable} className={inputClass} placeholder="from the title if empty" />
            </label>
            <label className="text-sm">
              <span className="text-gray-600 dark:text-gray-300">Team</span>
              <Select value={draft.team ?? ""} onChange={(e) => set({ team: e.target.value })} disabled={!editable}>
                <option value="">—</option>
                {teams.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </Select>
            </label>
            <label className="text-sm sm:col-span-2">
              <span className="text-gray-600 dark:text-gray-300">Show it on alerts whose event matches (field: value, one per line)</span>
              <textarea
                value={draft.matches ?? ""}
                onChange={(e) => set({ matches: e.target.value })}
                readOnly={!editable}
                spellCheck={false}
                placeholder={'type: WORKFLOW_FAILED\n"error.code": TIMEOUT'}
                className={`${inputClass} h-16 font-mono text-xs`}
              />
            </label>
            <label className="text-sm">
              <span className="text-gray-600 dark:text-gray-300">…or on alerts of these rules</span>
              <input
                value={(draft.rules ?? []).join(", ")}
                onChange={(e) => set({ rules: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })}
                readOnly={!editable}
                className={inputClass}
                placeholder="rule ids, comma-separated"
              />
            </label>
            <label className="flex items-center gap-2 self-end text-sm">
              <input type="checkbox" checked={!!draft.auto} onChange={(e) => set({ auto: e.target.checked })} disabled={!editable} />
              Run steps by themselves when an alert opens
            </label>
          </div>
        </div>

        <Card className="p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">How Kardan reads it</h2>
            {editable && preview?.valid && preview.yaml && rb?.format !== "yaml" && (
              <Button onClick={() => set({ source: preview.yaml ?? draft.source, matches: "" })}>Convert to structured YAML</Button>
            )}
          </div>
          {!preview && <p className="mt-3 text-sm text-gray-500">Paste or type a runbook to see its steps.</p>}
          {preview && !preview.valid && (
            <div className="mt-3">
              <Notice tone="error">{preview.error}</Notice>
            </div>
          )}
          {rb && (
            <div className="mt-3 space-y-3 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <Badge className={chip}>{formatLabel[rb.format] ?? rb.format}</Badge>
                <span className="font-mono text-xs text-gray-500">id {rb.id}</span>
                {rb.team && <span className="text-xs text-gray-500">team {rb.team}</span>}
                {rb.auto && <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20">runs by itself</Badge>}
              </div>
              {(rb.matches || (rb.rules ?? []).length > 0) && (
                <div className="text-xs text-gray-600 dark:text-gray-300">
                  Shown on alerts {rb.matches ? <>where {describeCondition(rb.matches)}</> : null}
                  {rb.matches && (rb.rules ?? []).length > 0 ? " or " : ""}
                  {(rb.rules ?? []).length > 0 && <>of rule {(rb.rules ?? []).join(", ")}</>}
                </div>
              )}
              {!rb.matches && !(rb.rules ?? []).length && (
                <Notice tone="warn">Not attached to any alert yet: add a match or a rule.</Notice>
              )}
              {rb.summary && <p className="text-gray-600 italic dark:text-gray-300">Context: {rb.summary}</p>}
              {rb.url && (
                <a href={rb.url} target="_blank" rel="noreferrer" className="block truncate text-amber-700 hover:underline">
                  {rb.url} <ArrowUpRight size={13} aria-hidden="true" className="inline-block" />
                </a>
              )}
              <ol className="space-y-2">
                {rb.steps.map((s, i) => {
                  const c = preview?.checks?.[i];
                  return (
                    <li key={i} className="flex gap-2.5">
                      <span className="mt-0.5 grid h-5 w-5 shrink-0 place-items-center rounded-full bg-gray-100 text-[11px] font-semibold ring-1 ring-gray-300 dark:bg-gray-800 dark:ring-gray-700">
                        {i + 1}
                      </span>
                      <div className="min-w-0 flex-1">
                        <div>
                          {s.if && <span className="mr-1 rounded bg-amber-50 px-1 text-xs text-amber-800 dark:bg-amber-500/10 dark:text-amber-300">if {s.if}</span>}
                          {s.text || c?.label || s.action}
                        </div>
                        <div className="mt-1 flex flex-wrap items-center gap-1.5">
                          <Badge className={chip}>{s.kind}</Badge>
                          {s.action && <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] dark:bg-gray-800">{s.action}</code>}
                          {c?.risk && <Badge className={riskTone[c.risk] ?? chip}>{c.risk} risk</Badge>}
                          {c?.needs === "approval" && (
                            <Badge className="bg-fuchsia-50 text-fuchsia-700 ring-fuchsia-600/20 dark:bg-fuchsia-500/10 dark:text-fuchsia-300">
                              four-eyes approval
                            </Badge>
                          )}
                          {s.inferred && <Badge className={chip}>read from text</Badge>}
                          {s.every && <Badge className={chip}>every {humanEvery(s.every)}</Badge>}
                          {s.then === "resolve" && <Badge className={chip}>then resolves the alert</Badge>}
                          {s.params &&
                            Object.entries(s.params).map(([k, v]) => (
                              <span key={k} className="font-mono text-[11px] text-gray-500">
                                {k}={v}
                              </span>
                            ))}
                        </div>
                        {c?.problem && <div className="mt-1 text-xs text-red-600 dark:text-red-400">{c.problem}</div>}
                      </div>
                    </li>
                  );
                })}
              </ol>
              {(rb.notes ?? []).length > 0 && (
                <ul className="list-disc pl-5 text-xs text-gray-500">
                  {rb.notes!.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              )}
              <p className="text-xs text-gray-500">
                Values in <code className="font-mono">{"{{ }}"}</code> come from each alert ($service, $instrument, workflowId…). High-risk actions always need a
                second person's approval.
              </p>
            </div>
          )}
        </Card>
      </div>
    </>
  );
}
