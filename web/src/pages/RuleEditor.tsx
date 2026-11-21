import { ArrowLeft, FlaskConical, GitPullRequest, History } from "lucide-react";
import { RuleDraftPanel } from "../Assist";
import { ProposeSheet } from "./ProposalsPage";
import { useEffect, useMemo, useRef, useState } from "react";
import { isMap, parseDocument, visit } from "yaml";
import { api, ApiError, type BacktestResult, type Info, type RuleDetail, type Team, type TestResult } from "../api";
import { builderSupports, ConditionBuilder, toRootGroup, type Cond } from "../ConditionBuilder";
import { navigate, Link } from "../router";
import { Badge, Button, Card, Notice, PageHeader, Select, formatUSD, timeAgo, toneFor } from "../ui";
import { OriginBadge } from "./RulesPage";

export const DRAFT_KEY = "kardan.ruleDraft";

export interface RuleDraft {
  yaml: string;
  sample: string;
}

function starterYAML(team: string) {
  return `id: my-new-rule
name: Describe what this rule catches
team: ${team}
match:
  all:
    - { field: $severity, op: severityAtLeast, value: error }
routing:
  - { to: team:${team}, channels: [ui] }
presentation:
  title: "{{$type}} from {{$service}}"
`;
}

const editorClass =
  "block w-full resize-y rounded-md border-0 bg-gray-950 p-3 font-mono text-[13px] leading-5 text-gray-100 ring-1 ring-gray-800 focus:ring-2 focus:ring-[var(--console-accent)]";

/** Reads `match:` from rule YAML for the builder, or explains why it cannot. */
function readMatch(yaml: string): { match?: Cond; problem?: string } {
  const doc = parseDocument(yaml);
  if (doc.errors.length) return { problem: "Fix the YAML first: " + doc.errors[0].message.split("\n")[0] };
  const js = doc.toJS() as { match?: unknown; rules?: unknown } | null;
  if (js && js.rules) return { problem: "This file holds several rules; edit them in YAML." };
  if (!builderSupports(js?.match)) return { problem: "This rule uses expressions the builder cannot show; edit it in YAML." };
  return { match: toRootGroup(js?.match as Cond | undefined) };
}

/** Writes `match:` back, keeping the rest of the YAML (and its comments) as written. */
function writeMatch(yaml: string, match: Cond): string {
  const doc = parseDocument(yaml);
  const node = doc.createNode(match);
  visit(node, { Map(_, n) { if (isMap(n) && n.has("field")) n.flow = true; } });
  doc.set("match", node);
  return doc.toString({ lineWidth: 0 });
}

const exampleEvent = JSON.stringify({
  type: "WORKFLOW_FAILED",
  workflowId: "WF-00042",
  workflowName: "order-settlement",
  service: "settlements",
  environment: "prod",
  severity: "ERROR",
  error: { code: "TIMEOUT", message: "Counterparty did not respond" },
}, null, 2);

export function RuleEditor({ id, teams }: { id: string | null; teams: Team[] }) {
  const isNew = id === null;
  const [created] = useState(() => {
    const draft = window.history.state?.createdRule as { detail: RuleDetail; sample: string } | undefined;
    return draft?.detail.rule.id === id ? draft : undefined;
  });
  const [detail, setDetail] = useState<RuleDetail | null>(created?.detail ?? null);
  const [yaml, setYaml] = useState(created?.detail.yaml ?? "");
  const [sample, setSample] = useState(created?.sample ?? "");
  const [test, setTest] = useState<TestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ tone: "info" | "error"; text: string } | null>(
    created ? { tone: "info", text: `Rule created (version ${created.detail.version}).` } : null,
  );
  const [info, setInfo] = useState<Info | null>(null);
  const [view, setView] = useState<"builder" | "yaml">("builder");
  const [schemaFields, setSchemaFields] = useState<string[]>([]);
  const [backtest, setBacktest] = useState<BacktestResult | null>(null);
  const [backtesting, setBacktesting] = useState(false);
  const [hours, setHours] = useState(1);
  const [proposing, setProposing] = useState(false);
  const [loaded, setLoaded] = useState(!!created);
  const initialYaml = useRef(created?.detail.yaml ?? "");
  const initialisedDraft = useRef(false);
  const sampleEdited = useRef(false);
  const fallbackTeam = isNew ? teams[0]?.id : undefined;

  useEffect(() => {
    api.info().then(setInfo).catch(() => {});
    api.schemas().then((list) => {
      const paths = new Set<string>();
      for (const sc of list) for (const f of sc.fields ?? []) paths.add(f.path);
      setSchemaFields([...paths].sort());
    }).catch(() => {});
  }, []);

  // Load the rule (or a draft handed over from the schema page).
  useEffect(() => {
    if (isNew) {
      if (initialisedDraft.current) return;
      initialisedDraft.current = true;
      let draft: RuleDraft | null = null;
      try {
        draft = JSON.parse(sessionStorage.getItem(DRAFT_KEY) ?? "null") as RuleDraft | null;
        sessionStorage.removeItem(DRAFT_KEY);
      } catch {
        /* ignore */
      }
      const y = draft?.yaml ?? starterYAML(fallbackTeam ?? "team");
      setYaml(y);
      initialYaml.current = y;
      setSample(draft?.sample ?? "");
      setDetail(null);
      setLoaded(true);
      return;
    }
    if (created) {
      window.history.replaceState(null, "");
      return;
    }
    let cancelled = false;
    api.rule(id).then((rule) => {
      if (cancelled) return;
      setDetail(rule);
      setYaml(rule.yaml);
      initialYaml.current = rule.yaml;
      setLoaded(true);
    }).catch((error: Error) => {
      if (!cancelled) setMessage({ tone: "error", text: error.message });
    });
    api.alerts("", "").then(async (alerts) => {
      const recent = alerts.find((alert) => alert.ruleId === id);
      if (!recent || cancelled || sampleEdited.current) return;
      const { alert } = await api.alert(recent.id);
      if (!cancelled && !sampleEdited.current) {
        setSample(alert.sampleRaw || JSON.stringify(alert.sample ?? {}, null, 2));
      }
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [id, isNew, fallbackTeam, created]);

  useEffect(() => {
    if (!isNew || !fallbackTeam || initialYaml.current !== starterYAML("team")) return;
    const previous = initialYaml.current;
    const next = starterYAML(fallbackTeam);
    setYaml((current) => current === previous ? next : current);
    initialYaml.current = next;
  }, [isNew, fallbackTeam]);

  useEffect(() => {
    if (!loaded || !yaml.trim()) {
      setTest(null);
      setTesting(false);
      return;
    }
    let cancelled = false;
    setTesting(true);
    const timer = window.setTimeout(() => {
      api.testRule(yaml, sample || "{}")
        .then((result) => { if (!cancelled) setTest(result); })
        .catch((error: Error) => { if (!cancelled) setTest({ valid: false, error: error.message }); })
        .finally(() => { if (!cancelled) setTesting(false); });
    }, 400);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [yaml, sample, loaded]);

  // Replay recorded events through the draft once it is valid.
  const validNow = !!test?.valid;
  useEffect(() => {
    if (!loaded || !validNow) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      setBacktesting(true);
      api.backtestRule(yaml, hours)
        .then((r) => { if (!cancelled) setBacktest(r); })
        .catch((e: Error) => { if (!cancelled) setBacktest({ valid: false, error: e.message }); })
        .finally(() => { if (!cancelled) setBacktesting(false); });
    }, 900);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [yaml, hours, loaded, validNow]);

  const parsedMatch = useMemo(() => readMatch(yaml), [yaml]);
  const fieldSuggestions = useMemo(
    () => [...(info?.facets ?? []).filter((f) => !["$id", "$raw", "$receivedAt"].includes(f)), ...schemaFields],
    [info, schemaFields],
  );

  const updateSample = (value: string) => {
    sampleEdited.current = true;
    setSample(value);
  };

  const dirty = yaml !== initialYaml.current;
  const isFileRule = detail?.rule.origin?.startsWith("file:") ?? false;
  const isOverride = detail?.rule.origin === "api" && !!detail?.rule.overrides;

  const save = async () => {
    setSaving(true);
    setMessage(null);
    try {
      const saved = isNew ? await api.createRule(yaml) : await api.updateRule(id, yaml, detail?.version);
      setDetail(saved);
      setYaml(saved.yaml);
      initialYaml.current = saved.yaml;
      setMessage({ tone: "info", text: `Saved (version ${saved.version}). Active on every replica within seconds.` });
      if (isNew) {
        navigate(`/rules/${encodeURIComponent(saved.rule.id)}`, true, {
          state: { createdRule: { detail: saved, sample } },
        });
      }
    } catch (e) {
      const err = e as ApiError;
      setMessage({
        tone: "error",
        text: err.status === 409 ? `${err.message}` : `Not saved: ${err.message}`,
      });
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!detail) return;
    const what = isOverride ? "Discard the UI edits and restore the version from the file?" : "Delete this rule?";
    if (!window.confirm(what)) return;
    try {
      await api.deleteRule(detail.rule.id);
      if (isOverride) {
        setMessage({ tone: "info", text: "UI edits discarded; restoring the file version…" });
        window.setTimeout(() => window.location.reload(), 700); // let replicas apply the change first
      } else {
        navigate("/rules", true);
      }
    } catch (e) {
      setMessage({ tone: "error", text: (e as Error).message });
    }
  };

  const fieldRows = useMemo(() => Object.entries(test?.fields ?? {}), [test]);

  return (
    <>
      <Link to="/rules" className="console-muted mb-3 inline-flex items-center gap-1.5 text-sm hover:underline">
        <ArrowLeft size={15} aria-hidden="true" /> Rules
      </Link>
      <PageHeader title={isNew ? "New rule" : detail?.rule.name || id}
        subtitle={detail ? <><OriginBadge origin={detail.rule.origin} overrides={detail.rule.overrides} /> <span className="ml-1">Version {detail.version}</span></> : undefined}
        actions={
          <>
            {detail?.rule.origin === "api" &&
              <Button onClick={() => void remove()}>{isOverride ? "Revert to file" : "Delete"}</Button>}
            {!isNew && detail && (
              <Button onClick={() => setProposing(true)} disabled={!loaded || !dirty || !test?.valid}>
                <GitPullRequest size={14} aria-hidden="true" /> Propose instead
              </Button>
            )}
            <Button variant="primary" onClick={() => void save()}
              disabled={!loaded || saving || testing || !test?.valid || (!dirty && !isNew)}>
              {saving ? "Saving…" : isNew ? "Create rule" : isFileRule ? "Save as UI override" : "Save changes"}
            </Button>
          </>
        }
      />

      {isFileRule && (
        <div className="mb-3">
          <Notice>
            This rule is defined in <code className="font-mono">{detail?.rule.origin?.replace("file:", "config/rules/")}</code>. Saving here stores an
            override in the database (the file is not changed); “Revert to file” brings the file version back.
          </Notice>
        </div>
      )}
      {message && (
        <div className="mb-3">
          <Notice tone={message.tone}>{message.text}</Notice>
        </div>
      )}

      {isNew && (
        <div className="mb-4">
          <RuleDraftPanel teams={teams} sample={sample} onDraft={(y) => { setYaml(y); setView("builder"); }} />
        </div>
      )}
      {proposing && detail && (
        <ProposeSheet kind="rule" target={detail.rule.id} body={yaml}
          onClose={() => setProposing(false)}
          onDone={(m) => { setProposing(false); setMessage({ tone: "info", text: m + ". Find it under Proposals." }); }} />
      )}

      <div className="grid items-start gap-4 xl:grid-cols-2">
        <section className="console-panel min-w-0 space-y-3 p-4 sm:p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="font-semibold">Rule definition</h2>
            <div className="flex gap-1" role="group" aria-label="Editor view">
              <button type="button" className="console-toggle" aria-pressed={view === "builder"} onClick={() => setView("builder")}>Builder</button>
              <button type="button" className="console-toggle" aria-pressed={view === "yaml"} onClick={() => setView("yaml")}>YAML</button>
            </div>
          </div>
          <p className="console-muted text-sm">
            {view === "builder"
              ? "Choose which events match. Routing, wording and timing live in the YAML view; both stay in sync."
              : "Set what the rule matches, who gets notified, and what the alert says."}
          </p>
          {view === "builder" ? (
            parsedMatch.match ? (
              <ConditionBuilder value={parsedMatch.match} fields={fieldSuggestions} ops={info?.ops ?? ["eq"]} disabled={!loaded}
                onChange={(m) => setYaml((y) => writeMatch(y, m))} />
            ) : (
              <Notice tone="warn">{parsedMatch.problem}</Notice>
            )
          ) : (
            <textarea id="rule-yaml" aria-label="Rule YAML" value={yaml} onChange={(e) => setYaml(e.target.value)}
              spellCheck={false} rows={22} className={editorClass} disabled={!loaded} />
          )}
          <Reference info={info} />
        </section>
        <section className="console-panel min-w-0 space-y-3 p-4 sm:p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <label htmlFor="sample-event" className="flex items-center gap-2 font-semibold">
              <FlaskConical size={17} className="text-[var(--console-accent)]" aria-hidden="true" /> Test with a sample event
            </label>
            {!sample.trim() && <Button onClick={() => updateSample(exampleEvent)}>Load example</Button>}
          </div>
          <p id="test-help" className="console-muted text-sm">Paste JSON, XML, FIX or a log line to preview the result. Testing sends no notifications.</p>
          <textarea id="sample-event" value={sample} onChange={(e) => updateSample(e.target.value)}
            aria-describedby="test-help" spellCheck={false} rows={9} placeholder="Paste the event as received"
            className={editorClass} />
          <div aria-live="polite" aria-busy={testing}>
            <TestPanel test={test} testing={testing} hasSample={sample.trim() !== ""} fieldRows={fieldRows} />
          </div>
          <div className="border-t border-[var(--console-line)] pt-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="flex items-center gap-2 font-semibold">
                <History size={17} className="text-[var(--console-accent)]" aria-hidden="true" /> Backtest on recorded events
              </h2>
              <Select aria-label="Backtest period" value={String(hours)} onChange={(e) => setHours(Number(e.target.value))} className="w-36">
                <option value="1">Last hour</option>
                <option value="6">Last 6 hours</option>
                <option value="24">Last 24 hours</option>
              </Select>
            </div>
            <p className="console-muted mt-1 text-sm">Replays events from the durable log through this draft. Nothing is stored or sent.</p>
            <div aria-live="polite" aria-busy={backtesting} className="mt-3">
              <BacktestPanel result={backtest} running={backtesting} valid={validNow} />
            </div>
          </div>
        </section>
      </div>
    </>
  );
}

function TestPanel({
  test,
  testing,
  hasSample,
  fieldRows,
}: {
  test: TestResult | null;
  testing: boolean;
  hasSample: boolean;
  fieldRows: [string, unknown][];
}) {
  if (testing || !test) {
    return <Card className="p-4 text-sm text-gray-500">{testing ? "Checking rule…" : "Enter a rule to check its syntax."}</Card>;
  }
  if (!test.valid) {
    return (
      <Notice tone="error">
        <div className="font-medium">The rule is not valid</div>
        <div className="mt-1 font-mono text-xs whitespace-pre-wrap">{test.error}</div>
      </Notice>
    );
  }
  const a = test.alert;
  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge className="bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300">valid</Badge>
        {hasSample &&
          (test.matches ? (
            <Badge className="bg-gray-900 text-white ring-gray-900 dark:bg-white dark:text-gray-900">matches this event</Badge>
          ) : (
            <Badge className="bg-gray-100 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300">does not match</Badge>
          ))}
        {test.format && <span className="text-xs text-gray-500">parsed as {test.format}</span>}
        {hasSample && test.matches && test.impact != null && (
          <span className="ml-auto text-xs text-gray-600 dark:text-gray-300">
            impact <b>{test.impact}</b>
            {test.notionalUsd != null && <> · {formatUSD(test.notionalUsd)} at risk</>}
            {test.incidentKey && (
              <>
                {" "}
                · incident key <code className="font-mono">{test.incidentKey}</code>
              </>
            )}
            {test.window && (
              <>
                {" "}
                · window {new Date(test.window.start).toLocaleTimeString()}–{new Date(test.window.end).toLocaleTimeString()}
              </>
            )}
          </span>
        )}
      </div>
      {hasSample && test.matches && (test.timeNote || test.during || test.cutoff) && (
        <div className="mt-2 space-y-1 text-xs text-gray-700 dark:text-gray-300">
          {test.timeNote && <div>{test.timeNote}</div>}
          {test.during && (
            <div>
              {test.during.calendar} is <b>{test.during.open ? "open" : "closed"}</b> now
              {test.during.open
                ? ": the alert goes out as configured."
                : test.during.otherwise === "suppress"
                  ? ": the event would be ignored."
                  : `: the alert would be downgraded to ${test.during.otherwise} and stay in the UI.`}
            </div>
          )}
          {test.cutoff && (
            <div>
              ⏱ races <b>{test.cutoff.name}</b> at{" "}
              {new Date(test.cutoff.at).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit", timeZoneName: "short" })}{" "}
              ({test.cutoff.minutesLeft > 0 ? `${test.cutoff.minutesLeft} min left` : "missed"}; urgency +{test.cutoff.urgency} impact)
            </div>
          )}
        </div>
      )}

      {fieldRows.length > 0 && hasSample && (
        <table className="mt-3 w-full table-fixed text-xs break-all">
          <thead>
            <tr className="text-left text-gray-500">
              <th className="py-1 font-medium">Field used by the rule</th>
              <th className="py-1 font-medium">Value in this event</th>
            </tr>
          </thead>
          <tbody>
            {fieldRows.map(([k, v]) => (
              <tr key={k} className="border-t border-gray-100 dark:border-gray-800">
                <td className="py-1 pr-3 font-mono">{k}</td>
                <td className={`py-1 font-mono ${v === null ? "text-red-600 dark:text-red-400" : ""}`}>
                  {v === null ? "missing" : typeof v === "object" ? JSON.stringify(v) : String(v)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {a && hasSample && (
        <div className="mt-4">
          <div className="mb-1 text-xs font-medium text-gray-500">Alert preview</div>
          <div className="flex overflow-hidden rounded-md ring-1 ring-gray-200 dark:ring-gray-800">
            <div className={`w-1.5 ${toneFor(a.color).stripe}`} />
            <div className="p-3">
              <Badge className={toneFor(a.color).badge}>{a.severity}</Badge>
              <div className="mt-1 font-semibold">{a.title || <span className="text-gray-400">(empty title)</span>}</div>
              {a.body && <div className="text-sm text-gray-600 dark:text-gray-300">{a.body}</div>}
              {a.fields && (
                <dl className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                  {a.fields.map((f) => (
                    <div key={f.label} className="flex gap-1">
                      <dt className="text-gray-500">{f.label}</dt>
                      <dd className="font-mono">{f.value}</dd>
                    </div>
                  ))}
                </dl>
              )}
            </div>
          </div>
        </div>
      )}
    </Card>
  );
}

function Reference({ info }: { info: Info | null }) {
  return (
    <details className="rounded-md bg-white p-3 text-xs ring-1 ring-gray-200 dark:bg-gray-900 dark:ring-gray-800">
      <summary className="cursor-pointer text-sm font-medium">Reference: fields, operators, templates</summary>
      <div className="mt-2 space-y-2 text-gray-600 dark:text-gray-300">
        <p>
          <b>Fields</b> are paths into the event (<code>error.code</code>, <code>payload.notional</code>, <code>labels.severity</code>) or
          canonical facets that work across schemas:
        </p>
        <div className="flex flex-wrap gap-1">
          {(info?.facets ?? []).map((f) => (
            <code key={f} className="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
              {f}
            </code>
          ))}
        </div>
        <p>
          <b>Operators:</b> {(info?.ops ?? []).join(", ")}. Combine with <code>all</code>, <code>any</code>, <code>not</code>.
        </p>
        <p>
          <b>Templates</b> (title, body, dedup key): <code>{"{{payload.notional | number}}"}</code>; filters: number, upper, lower,{" "}
          <code>default "x"</code>, <code>truncate 40</code>, json.
        </p>
        <p>
          <b>Routing:</b> <code>to: team:&lt;id&gt;</code> or <code>user:&lt;id&gt;</code>, channels email, sms, phone, ui.
        </p>
      </div>
    </details>
  );
}

function BacktestPanel({ result, running, valid }: { result: BacktestResult | null; running: boolean; valid: boolean }) {
  if (!valid) return <p className="console-muted text-sm">Runs once the rule is valid.</p>;
  if (!result) return <p className="console-muted text-sm">{running ? "Replaying events…" : "Waiting for changes…"}</p>;
  if (!result.valid || result.error) return <Notice tone="error">{result.error ?? "Backtest failed"}</Notice>;
  const buckets = result.buckets ?? [];
  const peak = Math.max(1, ...buckets.map((b) => b.matched));
  const now = Date.now();
  return (
    <div className={running ? "opacity-60 transition-opacity" : ""}>
      <dl className="grid grid-cols-3 gap-2">
        <Figure label="Events matched" value={result.matched ?? 0} accent />
        <Figure label={result.kind === "aggregate" ? "Threshold crossings" : "Alerts it would open"} value={result.alerts ?? 0} />
        <Figure label="Events replayed" value={result.scanned ?? 0} />
      </dl>
      {buckets.length > 0 && (
        <div className="mt-3" aria-label="Matches over time">
          <div className="backtest-bars" role="img"
            aria-label={`Matched events per ${Math.round((Date.parse(buckets[1]?.at ?? "") - Date.parse(buckets[0].at)) / 60000) || 0} minutes`}>
            {buckets.map((b) => (
              <span key={b.at} style={{ height: `${Math.max(b.matched ? 8 : 2, (b.matched / peak) * 100)}%` }}
                data-empty={b.matched === 0} title={`${new Date(b.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}: ${b.matched}`} />
            ))}
          </div>
          <div className="console-muted mt-1 flex justify-between text-xs">
            <span>{timeAgo(buckets[0].at, now)}</span><span>now</span>
          </div>
        </div>
      )}
      {result.note && <p className="console-muted mt-2 text-xs">{result.note}</p>}
      {result.truncated && <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">Stopped early (event or time budget); numbers are a lower bound.</p>}
      {(result.recoveries ?? 0) > 0 && <p className="console-muted mt-1 text-xs">{result.recoveries} recovery events would have closed alerts.</p>}
      {(result.samples ?? []).length > 0 && (
        <ul className="mt-3 divide-y divide-[var(--console-line)] text-sm">
          {result.samples!.map((m) => (
            <li key={m.id} className="flex items-baseline justify-between gap-3 py-1.5">
              <span className="min-w-0 truncate" title={m.title}>{m.title || "(no title)"}</span>
              <span className="console-muted shrink-0 font-mono text-xs">{m.sourceId} · {timeAgo(m.receivedAt, now)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Figure({ label, value, accent }: { label: string; value: number; accent?: boolean }) {
  return (
    <div className="rounded-md border border-[var(--console-line)] bg-[var(--console-control)] px-2.5 py-2">
      <dt className="console-muted text-xs">{label}</dt>
      <dd className={`mt-0.5 font-mono text-xl font-semibold tabular-nums ${accent ? "text-[var(--console-accent)]" : ""}`}>{value.toLocaleString()}</dd>
    </div>
  );
}
