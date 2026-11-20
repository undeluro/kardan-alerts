import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { api, ApiError, type Info, type RuleDetail, type Team, type TestResult } from "../api";
import { navigate, Link } from "../router";
import { Badge, Button, Card, Notice, formatUSD, toneFor } from "../ui";
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
  "block w-full resize-y rounded-md border-0 bg-gray-950 p-3 font-mono text-[13px] leading-5 text-gray-100 ring-1 ring-gray-800 focus:ring-2 focus:ring-amber-500";

// Two-space indentation on Tab, so YAML stays valid while typing.
function onTab(e: KeyboardEvent<HTMLTextAreaElement>, set: (v: string) => void) {
  if (e.key !== "Tab") return;
  e.preventDefault();
  const t = e.currentTarget;
  const { selectionStart: s, selectionEnd: end, value } = t;
  const next = value.slice(0, s) + "  " + value.slice(end);
  set(next);
  requestAnimationFrame(() => t.setSelectionRange(s + 2, s + 2));
}

export function RuleEditor({ id, teams }: { id: string | null; teams: Team[] }) {
  const isNew = id === null;
  const [detail, setDetail] = useState<RuleDetail | null>(null);
  const [yaml, setYaml] = useState("");
  const [sample, setSample] = useState("");
  const [test, setTest] = useState<TestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ tone: "info" | "error"; text: string } | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [loaded, setLoaded] = useState(false);
  const initialYaml = useRef("");

  useEffect(() => {
    api.info().then(setInfo).catch(() => {});
  }, []);

  // Load the rule (or a draft handed over from the schema page).
  useEffect(() => {
    setLoaded(false);
    setMessage(null);
    if (isNew) {
      let draft: RuleDraft | null = null;
      try {
        draft = JSON.parse(sessionStorage.getItem(DRAFT_KEY) ?? "null") as RuleDraft | null;
        sessionStorage.removeItem(DRAFT_KEY);
      } catch {
        /* ignore */
      }
      const y = draft?.yaml ?? starterYAML(teams[0]?.id ?? "team");
      setYaml(y);
      initialYaml.current = y;
      setSample(draft?.sample ?? "");
      setDetail(null);
      setLoaded(true);
      return;
    }
    api
      .rule(id)
      .then(async (d) => {
        setDetail(d);
        setYaml(d.yaml);
        initialYaml.current = d.yaml;
        setLoaded(true);
        // Prefill the test panel with the latest real event for this rule.
        const recent = (await api.alerts("", "")).find((a) => a.ruleId === id);
        if (recent) {
          const full = await api.alert(recent.id);
          // Replay the message exactly as it arrived (XML, FIX...) so the
          // test goes through the same parser as production.
          setSample(full.alert.sampleRaw || JSON.stringify(full.alert.sample, null, 2));
        }
      })
      .catch((e: Error) => setMessage({ tone: "error", text: e.message }));
    // teams only matters for the starter template
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  // Live dry run, debounced.
  useEffect(() => {
    if (!loaded || !yaml.trim()) return;
    const t = window.setTimeout(() => {
      setTesting(true);
      api
        .testRule(yaml, sample || "{}")
        .then(setTest)
        .catch((e: Error) => setTest({ valid: false, error: e.message }))
        .finally(() => setTesting(false));
    }, 400);
    return () => window.clearTimeout(t);
  }, [yaml, sample, loaded]);

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
      if (isNew) navigate(`/rules/${encodeURIComponent(saved.rule.id)}`, true);
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
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Link to="/rules" className="text-sm text-gray-500 hover:text-gray-800 dark:hover:text-gray-200">
          ← Rules
        </Link>
        <h1 className="text-xl font-semibold">{isNew ? "New rule" : detail?.rule.name || id}</h1>
        {detail && <OriginBadge origin={detail.rule.origin} overrides={detail.rule.overrides} />}
        {detail?.version && (
          <span className="text-xs text-gray-500">
            v{detail.version} · {detail.updatedBy}
          </span>
        )}
        <div className="ml-auto flex gap-2">
          {detail && detail.rule.origin === "api" && (
            <Button onClick={() => void remove()}>{isOverride ? "Revert to file" : "Delete"}</Button>
          )}
          <Button variant="primary" onClick={() => void save()} disabled={saving || (!dirty && !isNew) || test?.valid === false}>
            {saving ? "Saving…" : isFileRule ? "Save as UI override" : "Save"}
          </Button>
        </div>
      </div>

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

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="flex flex-col gap-3">
          <label className="text-sm font-medium">Rule (YAML)</label>
          <textarea
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
            onKeyDown={(e) => onTab(e, setYaml)}
            spellCheck={false}
            rows={24}
            className={editorClass}
            aria-label="Rule YAML"
          />
          <Reference info={info} />
        </div>

        <div className="flex flex-col gap-3">
          <label className="flex items-baseline justify-between text-sm font-medium">
            Test with a sample event
            <span className="text-xs font-normal text-gray-500">JSON, XML, FIX or a log line — parsed like live traffic</span>
          </label>
          <textarea
            value={sample}
            onChange={(e) => setSample(e.target.value)}
            onKeyDown={(e) => onTab(e, setSample)}
            spellCheck={false}
            rows={10}
            placeholder='{"type": "WORKFLOW_FAILED", "severity": "ERROR", ...}'
            className={editorClass}
            aria-label="Sample event"
          />
          <TestPanel test={test} testing={testing} hasSample={sample.trim() !== ""} fieldRows={fieldRows} />
        </div>
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
  if (!test) return <Card className="p-4 text-sm text-gray-500">{testing ? "Checking…" : "Edit the rule to see a live check."}</Card>;
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
      <div className="flex items-center gap-2">
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
        {testing && <span className="ml-auto text-xs text-gray-400">checking…</span>}
      </div>

      {fieldRows.length > 0 && hasSample && (
        <table className="mt-3 w-full text-xs">
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
