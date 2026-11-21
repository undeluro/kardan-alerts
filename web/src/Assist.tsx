import { Cpu, Sparkles, Wand2 } from "lucide-react";
import { useEffect, useState } from "react";
import { api, type AIStatus, type RuleDraftResult, type Team } from "./api";
import { Badge, Button, Notice, Select } from "./ui";

/** Shows whether a draft came from Claude or Kardan's built-in reader. */
export function SourceBadge({ source }: { source: "claude" | "heuristic" | "ai" | "person" | "system" }) {
  if (source === "claude" || source === "ai")
    return (
      <Badge className="bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30">
        <Sparkles size={12} aria-hidden="true" /> Claude draft
      </Badge>
    );
  if (source === "system")
    return (
      <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30">
        <Cpu size={12} aria-hidden="true" /> Kardan analysis
      </Badge>
    );
  if (source === "heuristic")
    return (
      <Badge className="bg-gray-50 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">
        <Cpu size={12} aria-hidden="true" /> Built-in reader
      </Badge>
    );
  return null;
}

let statusCache: Promise<AIStatus> | null = null;
export function useAIStatus() {
  const [status, setStatus] = useState<AIStatus | null>(null);
  useEffect(() => {
    statusCache ??= api.aiStatus().catch(() => ({ configured: false, model: "", teams: [] }));
    void statusCache.then(setStatus);
  }, []);
  return status;
}

/** "Describe the rule in plain English" → a draft rule in the editor. */
export function RuleDraftPanel({ teams, sample, onDraft }: {
  teams: Team[];
  sample: string;
  onDraft: (yaml: string) => void;
}) {
  const status = useAIStatus();
  const [sentence, setSentence] = useState("");
  const [team, setTeam] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<RuleDraftResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const chosen = team || teams[0]?.id || "";
  const claudeForTeam = !!status?.configured && !!status.teams.includes(chosen);

  const draft = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.aiRuleDraft(sentence, chosen, sample);
      setResult(r);
      onDraft(r.yaml);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="console-panel space-y-3 p-4 sm:p-5" aria-labelledby="draft-heading">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="draft-heading" className="flex items-center gap-2 font-semibold">
          <Wand2 size={17} className="text-[var(--console-accent)]" aria-hidden="true" /> Describe the rule
        </h2>
        <span className="console-muted text-xs">
          {claudeForTeam ? `Claude (${status?.model}) drafts it; you review before saving.` : "Kardan's built-in reader drafts it; you review before saving."}
        </span>
      </div>
      <div className="grid gap-2 sm:grid-cols-[1fr_12rem]">
        <textarea aria-label="Describe the rule" value={sentence} onChange={(e) => setSentence(e.target.value)} rows={2}
          placeholder="Page the on-call when 5 trades for one counterparty are rejected within 10 minutes"
          className="console-control w-full resize-y" />
        <div className="flex flex-col gap-2">
          <Select aria-label="Team" value={chosen} onChange={(e) => setTeam(e.target.value)}>
            {teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
          <Button variant="primary" disabled={busy || !sentence.trim() || !chosen} onClick={() => void draft()}>
            <Sparkles size={14} aria-hidden="true" /> {busy ? "Drafting…" : "Draft rule"}
          </Button>
        </div>
      </div>
      {error && <Notice tone="error">{error}</Notice>}
      {result && (
        <div className="space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <SourceBadge source={result.source} />
            {result.matchesSample !== undefined && (
              <span className="console-muted text-xs">{result.matchesSample ? "matches the sample event" : "does not match the sample event"}</span>
            )}
          </div>
          {result.explanation && <p>{result.explanation}</p>}
          {result.fallback && <p className="console-muted text-xs">{result.fallback}</p>}
          {!result.valid && <Notice tone="warn">Needs a fix before saving: {result.error}</Notice>}
          {result.warnings.map((w) => <p key={w} className="text-xs text-amber-700 dark:text-amber-300">{w}</p>)}
        </div>
      )}
    </section>
  );
}

/** Line diff (longest common subsequence), enough for rule and runbook YAML. */
export function lineDiff(a: string, b: string): { kind: " " | "+" | "-"; text: string }[] {
  const x = a ? a.replace(/\n$/, "").split("\n") : [];
  const y = b ? b.replace(/\n$/, "").split("\n") : [];
  const n = x.length, m = y.length;
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--) lcs[i][j] = x[i] === y[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  const out: { kind: " " | "+" | "-"; text: string }[] = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) { out.push({ kind: " ", text: x[i] }); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) out.push({ kind: "-", text: x[i++] });
    else out.push({ kind: "+", text: y[j++] });
  }
  while (i < n) out.push({ kind: "-", text: x[i++] });
  while (j < m) out.push({ kind: "+", text: y[j++] });
  return out;
}

export function DiffView({ before, after }: { before: string; after: string }) {
  const lines = lineDiff(before, after);
  const changed = lines.filter((l) => l.kind !== " ").length;
  return (
    <div>
      <p className="console-muted mb-1 text-xs">{before ? `${changed} changed ${changed === 1 ? "line" : "lines"}` : "New"}</p>
      <pre className="diff-view" aria-label="Proposed change">
        {lines.map((l, i) => (
          <span key={i} data-kind={l.kind}>
            <span aria-hidden="true">{l.kind === " " ? " " : l.kind}</span>
            <span className="sr-only">{l.kind === "+" ? "added: " : l.kind === "-" ? "removed: " : ""}</span>
            {l.text || " "}
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}
