import { useEffect, useState } from "react";
import { api, type Rule, type RuleView } from "../api";
import { Link } from "../router";
import { Badge, Button, Card, Notice, PageHeader, RuleKindChip, toneFor } from "../ui";
import { navigate } from "../router";

const channelIcon: Record<string, string> = { email: "✉", sms: "💬", phone: "📞", ui: "🖥" };

export function RulesPage() {
  const [rules, setRules] = useState<RuleView[] | null>(null);
  const [errors, setErrors] = useState<string[]>([]);
  const [configErrors, setConfigErrors] = useState<string[]>([]);
  const [query, setQuery] = useState("");

  useEffect(() => {
    const load = () => {
      api.rules().then((d) => {
        setRules(d.rules);
        setErrors(d.errors);
      });
      api.info().then((i) => setConfigErrors(i.configErrors.filter((e) => !e.startsWith("rule "))));
    };
    load();
    const t = window.setInterval(load, 5000); // pick up file edits and other people's changes
    return () => window.clearInterval(t);
  }, []);

  const q = query.trim().toLowerCase();
  const shown = (rules ?? []).filter(
    (r) => !q || [r.id, r.name ?? "", r.team, ...r.fields].some((s) => s.toLowerCase().includes(q)),
  );

  return (
    <>
      <PageHeader
        title="Rules"
        subtitle="Rules decide which events reach a team, who is told on which channel, and how the alert looks. Changes apply to every replica within seconds, without restarts."
        actions={
          <Button variant="primary" onClick={() => navigate("/rules/new")}>
            New rule
          </Button>
        }
      />
      {[...configErrors, ...errors].length > 0 && (
        <div className="mb-4">
          <Notice tone="error">
            <div className="font-medium">Configuration problems (these rules are not active):</div>
            <ul className="mt-1 list-disc pl-5 font-mono text-xs">
              {[...configErrors, ...errors].map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </Notice>
        </div>
      )}
      <input
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Filter by name, team or field…"
        className="mb-3 w-full max-w-sm rounded-md border-0 bg-white px-3 py-1.5 text-sm ring-1 ring-gray-300 ring-inset dark:bg-gray-900 dark:ring-gray-700"
      />
      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {rules === null && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {rules !== null && shown.length === 0 && <div className="p-4 text-sm text-gray-500">No rules match.</div>}
        {shown.map((r) => {
          const color = r.presentation?.color || "gray";
          const channels = [...new Set((r.routing ?? []).flatMap((rt) => rt.channels))];
          return (
            <Link key={r.id} to={`/rules/${encodeURIComponent(r.id)}`} className="flex items-start gap-3 p-4 hover:bg-gray-50 dark:hover:bg-gray-800/50">
              <span className={`mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full ${toneFor(color).dot}`} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-gray-900 dark:text-gray-50">{r.name || r.id}</span>
                  {!r.enabled && <Badge className="bg-gray-100 text-gray-600 ring-gray-300">disabled</Badge>}
                  <OriginBadge origin={r.origin} overrides={r.overrides} />
                  <RuleKindChip kind={r.kind} />
                </div>
                <div className="mt-0.5 font-mono text-xs text-gray-500">{r.id}</div>
                <TimeSummary rule={r} />
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {r.fields.map((f) => (
                    <code key={f} className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-gray-800 dark:text-gray-300">
                      {f}
                    </code>
                  ))}
                </div>
              </div>
              <div className="shrink-0 text-right text-sm">
                <div className="text-gray-700 dark:text-gray-300">{r.team}</div>
                <div className="mt-0.5 text-gray-500" title={channels.join(", ")}>
                  {channels.map((c) => channelIcon[c] ?? c).join(" ")}
                </div>
              </div>
            </Link>
          );
        })}
      </Card>
    </>
  );
}

export function OriginBadge({ origin, overrides }: { origin?: string; overrides?: string }) {
  if (!origin) return null;
  if (origin === "api" && overrides) {
    return (
      <Badge className="bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30">
        edited in UI · overrides {overrides.replace("file:", "")}
      </Badge>
    );
  }
  if (origin === "api") {
    return <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30">created in UI</Badge>;
  }
  return (
    <Badge className="bg-gray-50 text-gray-600 ring-gray-500/20 dark:bg-gray-500/10 dark:text-gray-300 dark:ring-gray-400/30">
      {origin.replace("file:", "config/rules/")}
    </Badge>
  );
}

/** One line describing the time-aware parts of a rule. */
export function TimeSummary({ rule: r }: { rule: Rule }) {
  const parts: string[] = [];
  if (r.kind === "aggregate" && r.aggregate) {
    const a = r.aggregate;
    const by = a.by?.length ? ` per ${a.by.join(" + ")}` : "";
    parts.push(
      a.sum
        ? `fires when the sum of ${a.sum}${by} reaches ${a.threshold.toLocaleString()} in ${a.window}`
        : `fires on the ${ordinal(a.threshold)} matching event${by} within ${a.window}`,
    );
  }
  if (r.kind === "absence" && r.absence) {
    const a = r.absence;
    const who = a.by ? ` per ${a.by}${a.expect?.length ? ` (${a.expect.join(", ")})` : ""}` : "";
    parts.push(
      a.deadline
        ? `expects an event${who} by ${a.deadline} every ${a.calendar} business day`
        : `expects an event${who} at least every ${a.every}${a.calendar ? ` while ${a.calendar} is open` : ""}`,
    );
  }
  if (r.during) parts.push(`only during ${r.during.calendar} hours (otherwise ${r.during.otherwise})`);
  if (r.cutoff) parts.push(r.cutoff === "auto" ? "races the currency's cut-off" : `races ${r.cutoff}`);
  if (r.escalation?.length) parts.push(`escalates ${r.escalation.map((e) => `${e.after} → ${e.to}`).join(", ")}`);
  if (!parts.length) return null;
  return <div className="mt-1 text-xs text-gray-600 dark:text-gray-400">⏱ {parts.join(" · ")}</div>;
}

const ordinal = (n: number) => {
  const s = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return n + (s[(v - 20) % 10] || s[v] || s[0]);
};
