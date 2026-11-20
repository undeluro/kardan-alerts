import { useEffect, useMemo, useState } from "react";
import { api, type FieldInfo, type SchemaDetail as Detail, type Team } from "../api";
import { Link, navigate } from "../router";
import { Badge, Button, Card, FinanceChip, Notice, inputClass, timeAgo } from "../ui";
import { DRAFT_KEY, type RuleDraft } from "./RuleEditor";

const MAPPABLE = ["$severity", "$service", "$type", "$env", "$instrument", "$counterparty", "$currency", "$notional", "$tradeId", "$desk", "$account"];

const facetHelp: Record<string, string> = {
  $severity: "normalised to critical / error / warning / info",
  $service: "the system the event is about",
  $type: "what kind of event this is",
  $env: "prod, uat…",
  $instrument: "ISIN, symbol, ticker",
  $counterparty: "LEI or counterparty name",
  $currency: "ISO 4217 code",
  $notional: "amount at stake",
  $tradeId: "trade / order / payment reference",
  $desk: "desk or book",
  $account: "account / IBAN (masked on SMS)",
};

export function SchemaDetail({ id, teams, now }: { id: string; teams: Team[]; now: number }) {
  const [s, setS] = useState<Detail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [manual, setManual] = useState<Record<string, string>>({});
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [saved, setSaved] = useState<string | null>(null);

  useEffect(() => {
    api
      .schema(id)
      .then((d) => {
        setS(d);
        setName(d.name);
        setManual(d.manualFacets ?? {});
      })
      .catch((e: Error) => setError(e.message));
  }, [id]);

  // Detected finance fields first, then shallow before deeply nested.
  const fields = useMemo(() => {
    const depth = (p: string) => p.split(".").length;
    return [...(s?.fields ?? [])].sort(
      (a, b) => Number(!!b.detected) - Number(!!a.detected) || depth(a.path) - depth(b.path) || a.path.localeCompare(b.path),
    );
  }, [s]);
  const paths = useMemo(() => fields.map((f) => f.path), [fields]);
  const dirty = !!s && (name !== s.name || JSON.stringify(manual) !== JSON.stringify(s.manualFacets ?? {}));

  const save = async () => {
    if (!s) return;
    try {
      const d = await api.updateSchema(s.id, name, manual);
      setS(d);
      setManual(d.manualFacets ?? {});
      setSaved("Saved. Rules on every replica now see the new facet mapping.");
      window.setTimeout(() => setSaved(null), 4000);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const createRule = () => {
    if (!s) return;
    const chosen = fields.filter((f) => selected.has(f.path));
    const draft: RuleDraft = { yaml: draftYAML(s, chosen, teams[0]?.id ?? "team"), sample: sampleText(s) };
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
    navigate("/rules/new");
  };

  if (error) return <Notice tone="error">{error}</Notice>;
  if (!s) return <div className="text-sm text-gray-500">Loading…</div>;

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Link to="/schemas" className="text-sm text-gray-500 hover:text-gray-800 dark:hover:text-gray-200">
          ← Schemas
        </Link>
        <input value={name} onChange={(e) => setName(e.target.value)} className={`${inputClass} max-w-sm text-lg font-semibold`} aria-label="Schema name" />
        <Badge className="bg-gray-100 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">{s.format}</Badge>
        <span className="text-xs text-gray-500">
          {s.count.toLocaleString()} {s.count === 1 ? "event" : "events"} from {s.sources.join(", ")} · first {timeAgo(s.firstSeen, now)} · last {timeAgo(s.lastSeen, now)}
        </span>
        <div className="ml-auto">
          <Button variant="primary" onClick={() => void save()} disabled={!dirty}>
            Save
          </Button>
        </div>
      </div>
      {saved && (
        <div className="mb-3">
          <Notice>{saved}</Notice>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-5">
        <Card className="p-4 lg:col-span-2">
          <h2 className="font-semibold">Facets</h2>
          <p className="mt-0.5 text-xs text-gray-500">
            Canonical names rules can use across schemas. Detected automatically; override a mapping when this schema spells a field
            differently.
          </p>
          <datalist id="schema-paths">
            {paths.map((p) => (
              <option key={p} value={p} />
            ))}
          </datalist>
          <table className="mt-3 w-full text-sm">
            <tbody>
              {MAPPABLE.map((f) => {
                const auto = s.autoFacets?.[f];
                const isManual = f in manual;
                const value = s.facetValues[f];
                return (
                  <tr key={f} className="border-t border-gray-100 align-top dark:border-gray-800">
                    <td className="py-2 pr-2">
                      <code className="font-mono text-xs font-semibold">{f}</code>
                      <div className="text-[11px] text-gray-500">{facetHelp[f]}</div>
                    </td>
                    <td className="py-2">
                      <input
                        list="schema-paths"
                        value={isManual ? manual[f] : ""}
                        placeholder={auto ? `${auto} (auto)` : "built-in guess"}
                        onChange={(e) => {
                          const v = e.target.value;
                          setManual((m) => {
                            const n = { ...m };
                            if (v === "") delete n[f];
                            else n[f] = v;
                            return n;
                          });
                        }}
                        className={`${inputClass} font-mono text-xs`}
                      />
                      <div className="mt-0.5 truncate font-mono text-[11px] text-gray-500" title={String(value ?? "")}>
                        {value === undefined ? "—" : `= ${typeof value === "object" ? JSON.stringify(value) : String(value)}`}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </Card>

        <Card className="p-4 lg:col-span-3">
          <div className="flex items-center gap-2">
            <h2 className="font-semibold">Fields</h2>
            <span className="text-xs text-gray-500">{fields.length}</span>
            <div className="ml-auto">
              <Button variant="primary" onClick={createRule} disabled={selected.size === 0}>
                Create rule from {selected.size || ""} selected
              </Button>
            </div>
          </div>
          <p className="mt-0.5 text-xs text-gray-500">Tick the fields that identify the problem; Kardan drafts a rule and tests it against this sample.</p>
          <div className="mt-3 max-h-[560px] overflow-y-auto">
            <table className="w-full text-xs">
              <thead className="sticky top-0 bg-white text-left text-gray-500 dark:bg-gray-900">
                <tr>
                  <th className="w-6 py-1" />
                  <th className="py-1 font-medium">Path</th>
                  <th className="w-[35%] py-1 font-medium">Example</th>
                </tr>
              </thead>
              <tbody>
                {fields.map((f) => (
                  <tr key={f.path} className="border-t border-gray-100 dark:border-gray-800">
                    <td className="py-1.5">
                      <input
                        type="checkbox"
                        checked={selected.has(f.path)}
                        onChange={(e) =>
                          setSelected((sel) => {
                            const n = new Set(sel);
                            if (e.target.checked) n.add(f.path);
                            else n.delete(f.path);
                            return n;
                          })
                        }
                        aria-label={`select ${f.path}`}
                      />
                    </td>
                    <td className="py-1.5 pr-2">
                      <span className="font-mono break-all">{f.path}</span>
                      {f.detected && (
                        <span className="ml-1.5 inline-block align-middle">
                          <FinanceChip kind={f.detected} sensitive={f.sensitive} />
                        </span>
                      )}
                    </td>
                    <td className="max-w-0 truncate py-1.5 pr-2 font-mono text-gray-600 dark:text-gray-300" title={f.example}>
                      {f.example}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      </div>

      <Card className="mt-4 p-4">
        <h2 className="font-semibold">Sample</h2>
        {s.sampleRaw && (
          <>
            <div className="mt-2 text-xs text-gray-500">As received ({s.format})</div>
            <pre className="mt-1 overflow-x-auto rounded-md bg-gray-50 p-3 font-mono text-xs ring-1 ring-gray-200 dark:bg-gray-950 dark:ring-gray-800">
              {s.sampleRaw}
            </pre>
            <div className="mt-3 text-xs text-gray-500">Parsed</div>
          </>
        )}
        <pre className="mt-1 max-h-96 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs ring-1 ring-gray-200 dark:bg-gray-950 dark:ring-gray-800">
          {JSON.stringify(s.sample, null, 2)}
        </pre>
      </Card>
    </>
  );
}

function sampleText(s: Detail): string {
  return s.sampleRaw || JSON.stringify(s.sample, null, 2);
}

function slug(s: string) {
  return (
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 40) || "schema"
  );
}

function yamlScalar(f: FieldInfo): string {
  if (f.type === "number" || f.type === "bool") return f.example ?? "0";
  return JSON.stringify(f.example ?? "");
}

// draftYAML turns ticked fields into a starting rule: equality on identifiers,
// "at least" on amounts, and a title built from the chosen fields.
function draftYAML(s: Detail, chosen: FieldInfo[], team: string): string {
  const conds = chosen.map((f) => {
    const isAmount = ["amount", "quantity", "price"].includes(f.detected ?? "") && f.type === "number";
    const op = isAmount ? "gte" : "eq";
    return `    - { field: ${JSON.stringify(f.path)}, op: ${op}, value: ${yamlScalar(f)} }`;
  });
  const titleVars = chosen
    .slice(0, 3)
    .map((f) => `{{${f.path}${f.detected === "amount" ? " | number" : ""}}}`)
    .join(" ");
  const fields = chosen.map((f) => `    - ${JSON.stringify(f.path)}`);
  return `id: ${slug(s.name)}-alert
name: ${JSON.stringify(`${s.name} alert`)}
team: ${team}
match:
  all:
${conds.join("\n")}
routing:
  - { to: team:${team}, channels: [ui] }
presentation:
  title: ${JSON.stringify(`${s.name}: ${titleVars}`)}
  fields:
${fields.join("\n")}
`;
}
