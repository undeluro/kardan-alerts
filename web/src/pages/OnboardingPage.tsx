import { ArrowLeft, ArrowRight, Check, CircleCheck, Plus, Send, Sparkles, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Document, isMap, visit } from "yaml";
import { api, type BacktestResult, type FieldInfo, type Inspection, type Person, type Team, type TestResult } from "../api";
import { ConditionBuilder, type Cond } from "../ConditionBuilder";
import { TIMEZONES } from "../DirectoryEditors";
import { Link, navigate } from "../router";
import { Badge, Button, ChannelIcon, FinanceChip, Notice, PageHeader, Select, channelLabel, inputClass } from "../ui";

const STEPS = ["Team", "People", "Sample event", "What to catch", "Who and how", "Review"] as const;

const slug = (s: string) => s.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);

interface NewPerson {
  name: string;
  email: string;
  phone: string;
  timezone: string;
}

const EXAMPLE = `{
  "eventType": "PAYMENT_REJECTED",
  "system": "fx-settlement",
  "level": "ERROR",
  "payment": { "id": "PAY-20931", "amount": 2500000, "currency": "EUR", "iban": "DE89370400440532013000" },
  "counterparty": "ACME-CAPITAL",
  "reason": "AC04 closed account"
}`;

const severityOptions = ["critical", "error", "warning", "info"];

/** A condition people would usually want for a field, from its example value. */
function conditionFor(f: FieldInfo): Cond {
  if (f.type === "number") return { field: f.path, op: "gte", value: Number(f.example ?? 0) };
  if (f.type === "bool") return { field: f.path, op: "eq", value: f.example === "true" };
  return { field: f.path, op: "eq", value: f.example ?? "" };
}

export function OnboardingPage({ onToast }: { onToast: (m: string) => void }) {
  const [step, setStep] = useState(0);
  const [teams, setTeams] = useState<Team[]>([]);
  const [people, setPeople] = useState<Person[]>([]);

  // Step 1-2
  const [teamName, setTeamName] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [newPeople, setNewPeople] = useState<NewPerson[]>([]);
  // Step 3
  const [sample, setSample] = useState("");
  const [inspection, setInspection] = useState<Inspection | null>(null);
  const [inspectError, setInspectError] = useState<string | null>(null);
  // Step 4
  const [match, setMatch] = useState<Cond>({ all: [] });
  // Step 5
  const [route, setRoute] = useState<"team" | "oncall">("team");
  const [channels, setChannels] = useState<string[]>(["preferences"]);
  const [severity, setSeverity] = useState("error");
  const [title, setTitle] = useState("");
  const [alertFields, setAlertFields] = useState<string[]>([]);
  // Step 6
  const [yamlText, setYamlText] = useState("");
  const [yamlEdited, setYamlEdited] = useState(false);
  const [test, setTest] = useState<TestResult | null>(null);
  const [backtest, setBacktest] = useState<BacktestResult | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ ruleId: string; teamId: string } | null>(null);
  const [sent, setSent] = useState(false);

  useEffect(() => {
    api.teams().then((d) => { setTeams(d.teams); setPeople(d.people); }).catch(() => {});
  }, []);

  const teamId = slug(teamName);
  const teamExists = teams.some((t) => t.id === teamId);
  const newIds = newPeople.map((p) => slug(p.name)).filter(Boolean);
  const allMembers = [...members, ...newIds];
  const ops = ["eq", "neq", "in", "nin", "gt", "gte", "lt", "lte", "contains", "startsWith", "endsWith", "glob", "regex", "exists", "severityAtLeast"];
  const fields = inspection?.fields ?? [];
  const suggestions = useMemo(() => [
    ...Object.keys(inspection?.facets ?? {}), ...fields.map((f) => f.path),
  ], [inspection, fields]);

  const inspect = async () => {
    setInspectError(null);
    try {
      const r = await api.inspectEvent(sample);
      setInspection(r);
      // Sensible first guesses: severity and type facets when the event has them.
      const start: Cond[] = [];
      if (r.facets.$severity !== undefined) start.push({ field: "$severity", op: "severityAtLeast", value: "error" });
      if (r.facets.$type !== undefined) start.push({ field: "$type", op: "eq", value: String(r.facets.$type) });
      setMatch({ all: start });
      const picked = r.fields.filter((f) => f.detected).slice(0, 4).map((f) => f.path);
      setAlertFields(picked.length ? picked : r.fields.slice(0, 3).map((f) => f.path));
      const typeLabel = r.facets.$type !== undefined ? "{{$type}}" : "Event";
      const svc = r.facets.$service !== undefined ? " from {{$service}}" : r.facets.$counterparty !== undefined ? " for {{$counterparty}}" : "";
      setTitle(`${typeLabel}${svc}`);
      if (typeof r.facets.$severity === "string") setSeverity(["critical", "error", "warning", "info"].includes(r.facets.$severity) ? r.facets.$severity : "error");
    } catch (e) {
      setInspectError((e as Error).message);
    }
  };

  const ruleId = `${teamId || "team"}-${slug(String(inspection?.facets.$type ?? "events")) || "events"}`;
  const generated = useMemo(() => {
    const rule: Record<string, unknown> = {
      id: ruleId,
      name: `${teamName || "New team"}: ${String(inspection?.facets.$type ?? "events")}`,
      team: teamId || "team",
      match,
      routing: [{ to: `${route}:${teamId || "team"}`, channels }],
      presentation: {
        title: title || "{{$type}}",
        severity,
        ...(alertFields.length ? { fields: alertFields } : {}),
      },
    };
    const doc = new Document(rule);
    visit(doc, { Map(_, n) { if (isMap(n) && (n.has("field") || n.has("to"))) n.flow = true; } });
    return `# Created by the onboarding wizard\n${doc.toString({ lineWidth: 0 })}`;
  }, [ruleId, teamName, teamId, inspection, match, route, channels, title, severity, alertFields]);

  useEffect(() => {
    if (!yamlEdited) setYamlText(generated);
  }, [generated, yamlEdited]);

  // Try the rule on the sample and on recorded traffic when reviewing.
  useEffect(() => {
    if (step !== 5 || !yamlText) return;
    let cancelled = false;
    const t = window.setTimeout(() => {
      api.testRule(yamlText, sample).then((r) => !cancelled && setTest(r)).catch(() => {});
      api.backtestRule(yamlText, 24).then((r) => !cancelled && setBacktest(r)).catch(() => {});
    }, 400);
    return () => { cancelled = true; window.clearTimeout(t); };
  }, [step, yamlText, sample]);

  const canNext = [
    teamId.length > 0,
    allMembers.length > 0 && newPeople.every((p) => p.name.trim()),
    !!inspection,
    true,
    channels.length > 0 && title.trim().length > 0,
    !!test?.valid,
  ][step];

  const create = async () => {
    setCreating(true);
    setError(null);
    try {
      for (const p of newPeople) {
        await api.savePerson({ id: slug(p.name), name: p.name.trim(), email: p.email || undefined, phone: p.phone || undefined, timezone: p.timezone });
      }
      const existing = teams.find((t) => t.id === teamId);
      await api.saveTeam({
        ...(existing ?? { preferences: [] }), id: teamId, name: teamName.trim(), origin: undefined,
        members: [...new Set([...(existing?.members ?? []), ...allMembers])],
      });
      const saved = await api.createRule(yamlText);
      setDone({ ruleId: saved.rule.id, teamId });
      onToast(`Team ${teamName} is live`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCreating(false);
    }
  };

  const sendSample = async () => {
    try {
      await api.sendEvent(`${teamId}-onboarding`, sample);
      setSent(true);
      onToast("Sample sent through ingest; watch the alert queue");
    } catch (e) {
      onToast((e as Error).message);
    }
  };

  if (done) {
    return (
      <>
        <PageHeader title="Team onboarded" />
        <section className="console-panel max-w-2xl space-y-4 p-5">
          <div className="flex items-center gap-3">
            <CircleCheck size={28} className="text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
            <div>
              <h2 className="font-semibold">{teamName} receives alerts now</h2>
              <p className="console-muted text-sm">Rule <code className="font-mono">{done.ruleId}</code> is active on every replica.</p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" onClick={() => void sendSample()} disabled={sent}>
              <Send size={14} aria-hidden="true" /> {sent ? "Sample sent" : "Send the sample as a live event"}
            </Button>
            <Button onClick={() => navigate("/")}>Open the alert queue</Button>
            <Button onClick={() => navigate(`/rules/${encodeURIComponent(done.ruleId)}`)}>Fine-tune the rule</Button>
            <Button onClick={() => navigate("/teams")}>Set preferences</Button>
          </div>
        </section>
      </>
    );
  }

  return (
    <>
      <Link to="/teams" className="console-muted mb-3 inline-flex items-center gap-1.5 text-sm hover:underline">
        <ArrowLeft size={15} aria-hidden="true" /> Teams
      </Link>
      <PageHeader title="Onboard a team" subtitle="From a sample event to live alerts in about two minutes. Nothing is saved until the last step." />
      <ol className="wizard-steps mb-4" aria-label="Steps">
        {STEPS.map((label, i) => (
          <li key={label} aria-current={i === step ? "step" : undefined} data-done={i < step}>
            <button type="button" disabled={i > step} onClick={() => setStep(i)}>
              <span className="wizard-dot">{i < step ? <Check size={12} aria-hidden="true" /> : i + 1}</span>
              <span>{label}</span>
            </button>
          </li>
        ))}
      </ol>

      <section className="console-panel space-y-4 p-4 sm:p-5">
        {step === 0 && (
          <>
            <h2 className="font-semibold">Which team?</h2>
            <label className="block max-w-md text-sm">
              <span className="font-medium">Team name</span>
              <input className={`${inputClass} mt-1`} value={teamName} onChange={(e) => setTeamName(e.target.value)} placeholder="FX Settlements" autoFocus list="existing-teams" />
              <datalist id="existing-teams">{teams.map((t) => <option key={t.id} value={t.name} />)}</datalist>
              <span className="console-muted mt-1 block text-xs">
                ID <code className="font-mono">{teamId || "…"}</code>{teamExists ? " exists: new rules and members are added to it." : " (new team)"}
              </span>
            </label>
          </>
        )}

        {step === 1 && (
          <>
            <h2 className="font-semibold">Who is in it?</h2>
            <ul className="grid gap-1 sm:grid-cols-3">
              {people.map((p) => (
                <li key={p.id}>
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={members.includes(p.id)}
                      onChange={() => setMembers(members.includes(p.id) ? members.filter((m) => m !== p.id) : [...members, p.id])} />
                    {p.name} <span className="console-muted text-xs">{p.timezone}</span>
                  </label>
                </li>
              ))}
            </ul>
            <div className="space-y-2">
              {newPeople.map((p, i) => (
                <div key={i} className="grid gap-2 sm:grid-cols-[1fr_1fr_1fr_11rem_auto]">
                  {(["name", "email", "phone"] as const).map((k) => (
                    <input key={k} aria-label={`New person ${i + 1} ${k}`} className={inputClass} placeholder={k} value={p[k]}
                      onChange={(e) => setNewPeople(newPeople.map((x, j) => (j === i ? { ...x, [k]: e.target.value } : x)))} />
                  ))}
                  <Select aria-label={`New person ${i + 1} time zone`} value={p.timezone}
                    onChange={(e) => setNewPeople(newPeople.map((x, j) => (j === i ? { ...x, timezone: e.target.value } : x)))}>
                    {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
                  </Select>
                  <button type="button" aria-label="Remove" className="console-muted grid size-9 place-items-center hover:text-red-600"
                    onClick={() => setNewPeople(newPeople.filter((_, j) => j !== i))}><Trash2 size={15} aria-hidden="true" /></button>
                </div>
              ))}
              <Button onClick={() => setNewPeople([...newPeople, { name: "", email: "", phone: "", timezone: "Europe/London" }])}>
                <Plus size={14} aria-hidden="true" /> New person
              </Button>
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="font-semibold">Paste an event the team cares about</h2>
              {!sample && <Button onClick={() => setSample(EXAMPLE)}>Use an example</Button>}
            </div>
            <p className="console-muted text-sm">Any shape: JSON, XML (ISO 20022), FIX or a log line. Kardan reads it the way ingest will.</p>
            <textarea aria-label="Sample event" value={sample} onChange={(e) => { setSample(e.target.value); setInspection(null); }} rows={10} spellCheck={false}
              className="block w-full rounded-md bg-gray-950 p-3 font-mono text-[13px] text-gray-100 ring-1 ring-gray-800" />
            <Button variant="primary" disabled={!sample.trim()} onClick={() => void inspect()}>Read the event</Button>
            {inspectError && <Notice tone="error">{inspectError}</Notice>}
            {inspection && <InspectionView inspection={inspection} />}
          </>
        )}

        {step === 3 && (
          <>
            <h2 className="font-semibold">Which events should raise an alert?</h2>
            <p className="console-muted text-sm">Click a field to add it, then adjust. Facets like <code className="font-mono">$severity</code> work across every schema.</p>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(inspection?.facets ?? {}).filter(([k]) => !["$source", "$format"].includes(k)).map(([k, v]) => (
                <button key={k} type="button" className="wizard-chip"
                  onClick={() => setMatch({ all: [...(match.all ?? match.any ?? []), k === "$severity" ? { field: k, op: "severityAtLeast", value: "error" } : { field: k, op: "eq", value: v as string }] })}>
                  <Plus size={12} aria-hidden="true" /> {k} = {String(v)}
                </button>
              ))}
              {fields.slice(0, 24).map((f) => (
                <button key={f.path} type="button" className="wizard-chip" onClick={() => setMatch({ all: [...(match.all ?? match.any ?? []), conditionFor(f)] })}>
                  <Plus size={12} aria-hidden="true" /> {f.path}{f.detected ? ` · ${f.detected}` : ""}
                </button>
              ))}
            </div>
            <ConditionBuilder value={match} onChange={setMatch} fields={suggestions} ops={ops} />
          </>
        )}

        {step === 4 && (
          <>
            <h2 className="font-semibold">Who gets it, and how?</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <fieldset>
                <legend className="text-sm font-medium">Send to</legend>
                <div className="mt-1 flex gap-1" role="group">
                  <button type="button" className="console-toggle" aria-pressed={route === "team"} onClick={() => setRoute("team")}>Everyone in the team</button>
                  <button type="button" className="console-toggle" aria-pressed={route === "oncall"} onClick={() => setRoute("oncall")}>Whoever is on call</button>
                </div>
              </fieldset>
              <fieldset>
                <legend className="text-sm font-medium">Channels</legend>
                <div className="mt-1 flex flex-wrap gap-3 text-sm">
                  <label className="flex items-center gap-1.5">
                    <input type="checkbox" checked={channels.includes("preferences")} onChange={(e) => setChannels(e.target.checked ? ["preferences"] : ["ui"])} />
                    Each person's preferences
                  </label>
                  {!channels.includes("preferences") && ["email", "sms", "phone", "ui"].map((ch) => (
                    <label key={ch} className="flex items-center gap-1.5">
                      <input type="checkbox" checked={channels.includes(ch)}
                        onChange={() => setChannels(channels.includes(ch) ? channels.filter((c) => c !== ch) : [...channels, ch])} />
                      <ChannelIcon channel={ch} size={14} /> {channelLabel(ch)}
                    </label>
                  ))}
                </div>
              </fieldset>
              <label className="block text-sm">
                <span className="font-medium">Alert title</span>
                <input className={`${inputClass} mt-1 font-mono`} value={title} onChange={(e) => setTitle(e.target.value)} />
                <span className="console-muted mt-1 block text-xs">Use {"{{field.path}}"} or {"{{$facet}}"} to fill in values.</span>
              </label>
              <label className="block text-sm">
                <span className="font-medium">Severity</span>
                <Select value={severity} onChange={(e) => setSeverity(e.target.value)} className="mt-1">
                  {severityOptions.map((s) => <option key={s} value={s}>{s}</option>)}
                </Select>
              </label>
            </div>
            <fieldset>
              <legend className="text-sm font-medium">Show on the alert</legend>
              <div className="mt-1 flex flex-wrap gap-1.5">
                {fields.map((f) => (
                  <label key={f.path} className="wizard-chip" data-on={alertFields.includes(f.path)}>
                    <input type="checkbox" className="sr-only" checked={alertFields.includes(f.path)}
                      onChange={() => setAlertFields(alertFields.includes(f.path) ? alertFields.filter((p) => p !== f.path) : [...alertFields, f.path])} />
                    {f.path}{f.sensitive ? " (masked)" : ""}
                  </label>
                ))}
              </div>
            </fieldset>
          </>
        )}

        {step === 5 && (
          <>
            <h2 className="font-semibold">Review</h2>
            <div className="grid gap-4 xl:grid-cols-2">
              <div>
                <label htmlFor="wizard-yaml" className="text-sm font-medium">Rule</label>
                <textarea id="wizard-yaml" value={yamlText} onChange={(e) => { setYamlText(e.target.value); setYamlEdited(true); }} rows={18} spellCheck={false}
                  className="mt-1 block w-full rounded-md bg-gray-950 p-3 font-mono text-[13px] text-gray-100 ring-1 ring-gray-800" />
                {yamlEdited && <button type="button" className="console-muted mt-1 text-xs hover:underline" onClick={() => setYamlEdited(false)}>Regenerate from the steps</button>}
              </div>
              <div className="space-y-3 text-sm">
                <div>
                  <h3 className="font-medium">Team</h3>
                  <p>{teamName} <code className="console-muted font-mono text-xs">{teamId}</code> · {allMembers.length} {allMembers.length === 1 ? "member" : "members"}{newPeople.length ? ` (${newPeople.length} new)` : ""}</p>
                </div>
                <div>
                  <h3 className="font-medium">On the sample</h3>
                  {!test ? <p className="console-muted">Checking…</p> : !test.valid ? <Notice tone="error">{test.error}</Notice> : (
                    <div className="space-y-1">
                      {test.matches
                        ? <Badge className="bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300">fires</Badge>
                        : <Notice tone="warn">The sample does not match these conditions.</Notice>}
                      {test.matches && test.alert && <p className="font-semibold">{test.alert.title}</p>}
                    </div>
                  )}
                </div>
                <div>
                  <h3 className="font-medium">On the last 24 hours of traffic</h3>
                  {!backtest ? <p className="console-muted">Replaying…</p> : backtest.error ? <p className="console-muted">{backtest.error}</p> : (
                    <p>{backtest.matched?.toLocaleString()} of {backtest.scanned?.toLocaleString()} events match → about {backtest.alerts?.toLocaleString()} alerts.</p>
                  )}
                </div>
                {error && <Notice tone="error">{error}</Notice>}
              </div>
            </div>
          </>
        )}

        <div className="flex flex-wrap justify-between gap-2 border-t border-[var(--console-line)] pt-4">
          <Button onClick={() => setStep(step - 1)} disabled={step === 0}><ArrowLeft size={14} aria-hidden="true" /> Back</Button>
          {step < STEPS.length - 1 ? (
            <Button variant="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>Next <ArrowRight size={14} aria-hidden="true" /></Button>
          ) : (
            <Button variant="primary" disabled={!canNext || creating} onClick={() => void create()}>
              <Sparkles size={14} aria-hidden="true" /> {creating ? "Creating…" : "Create team and rule"}
            </Button>
          )}
        </div>
      </section>
    </>
  );
}

function InspectionView({ inspection }: { inspection: Inspection }) {
  return (
    <div className="space-y-2">
      <p className="text-sm">Read as <b>{inspection.format}</b> with {inspection.fields.length} fields.</p>
      {Object.keys(inspection.facets).length > 0 && (
        <div className="flex flex-wrap gap-1.5 text-xs">
          {Object.entries(inspection.facets).map(([k, v]) => (
            <span key={k} className="rounded border border-[var(--console-line)] px-1.5 py-0.5 font-mono">{k} = {String(v)}</span>
          ))}
        </div>
      )}
      <ul className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-2">
        {inspection.fields.slice(0, 30).map((f) => (
          <li key={f.path} className="flex min-w-0 flex-wrap items-center gap-1.5">
            <code className="font-mono text-xs">{f.path}</code>
            {f.detected && <FinanceChip kind={f.detected} sensitive={f.sensitive} />}
            <span className="console-muted truncate text-xs">{f.example}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
