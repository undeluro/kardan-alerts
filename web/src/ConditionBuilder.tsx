import { Ban, Plus, Trash2 } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Button, Select } from "./ui";

/** One node of a rule's `match` tree, as written in YAML. */
export interface Cond {
  all?: Cond[];
  any?: Cond[];
  not?: Cond;
  field?: string;
  op?: string;
  value?: unknown;
}

const opLabels: Record<string, string> = {
  eq: "equals", neq: "does not equal", in: "is one of", nin: "is none of", gt: ">", gte: "≥", lt: "<", lte: "≤",
  contains: "contains", startsWith: "starts with", endsWith: "ends with", glob: "matches glob", regex: "matches regex",
  exists: "exists", severityAtLeast: "severity at least",
};
const numericOps = new Set(["gt", "gte", "lt", "lte"]);
const listOps = new Set(["in", "nin"]);
const severities = ["debug", "info", "warning", "error", "critical"];

const isGroup = (c: Cond) => !!(c.all || c.any);

/** The builder always edits a group at the root; a bare leaf becomes "all of: [leaf]". */
export function toRootGroup(c: Cond | undefined | null): Cond {
  if (!c || typeof c !== "object") return { all: [] };
  if (isGroup(c)) return c;
  return { all: [c] };
}

/** Can the builder show this tree faithfully? (expr conditions or odd shapes stay YAML-only) */
export function builderSupports(c: unknown): boolean {
  if (c === undefined || c === null) return true;
  if (typeof c !== "object" || Array.isArray(c)) return false;
  const n = c as Record<string, unknown>;
  const keys = Object.keys(n);
  if (keys.some((k) => !["all", "any", "not", "field", "op", "value"].includes(k))) return false;
  if (n.all) return Array.isArray(n.all) && n.all.every(builderSupports);
  if (n.any) return Array.isArray(n.any) && n.any.every(builderSupports);
  if (n.not) return builderSupports(n.not);
  return typeof n.field === "string";
}

function parseValue(op: string, text: string): unknown {
  const scalar = (s: string): unknown => {
    const t = s.trim();
    if (t !== "" && !Number.isNaN(Number(t)) && /^-?[\d.]+(e\d+)?$/i.test(t)) return Number(t);
    if (t === "true") return true;
    if (t === "false") return false;
    return t;
  };
  if (listOps.has(op)) return text.split(",").map(scalar).filter((v) => v !== "");
  if (numericOps.has(op)) {
    const n = Number(text);
    return text.trim() === "" || Number.isNaN(n) ? text : n;
  }
  if (op === "regex" || op === "glob" || op === "contains" || op === "startsWith" || op === "endsWith") return text;
  return scalar(text);
}

function showValue(v: unknown): string {
  if (Array.isArray(v)) return v.join(", ");
  if (v === undefined || v === null) return "";
  return String(v);
}

interface Props {
  value: Cond;
  onChange: (c: Cond) => void;
  fields: string[];
  ops: string[];
  disabled?: boolean;
}

export function ConditionBuilder({ value, onChange, fields, ops, disabled }: Props) {
  const listId = useId();
  return (
    <div>
      <datalist id={listId}>
        {fields.map((f) => <option key={f} value={f} />)}
      </datalist>
      <Group cond={value} onChange={onChange} onRemove={undefined} listId={listId} ops={ops} depth={0} disabled={disabled} />
    </div>
  );
}

function Group({ cond, onChange, onRemove, listId, ops, depth, disabled, negated }: {
  cond: Cond;
  onChange: (c: Cond) => void;
  onRemove?: () => void;
  listId: string;
  ops: string[];
  depth: number;
  disabled?: boolean;
  negated?: boolean;
}) {
  const mode: "all" | "any" = cond.any ? "any" : "all";
  const items = (cond[mode] ?? []) as Cond[];
  const set = (next: Cond[]) => onChange({ [mode]: next });
  const update = (i: number, c: Cond) => set(items.map((x, j) => (j === i ? c : x)));
  const remove = (i: number) => set(items.filter((_, j) => j !== i));

  return (
    <div className={`builder-group ${depth > 0 ? "builder-nested" : ""} ${negated ? "builder-negated" : ""}`}>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        {negated && <span className="builder-not-chip"><Ban size={12} aria-hidden="true" /> Not</span>}
        <div className="flex gap-1" role="group" aria-label="Combine conditions with">
          {(["all", "any"] as const).map((m) => (
            <button key={m} type="button" className="console-toggle" aria-pressed={mode === m} disabled={disabled}
              onClick={() => onChange({ [m]: items })}>
              {m === "all" ? "All of (AND)" : "Any of (OR)"}
            </button>
          ))}
        </div>
        {onRemove && (
          <button type="button" onClick={onRemove} disabled={disabled} aria-label="Remove group"
            className="console-muted ml-auto grid size-8 place-items-center rounded hover:text-red-600">
            <Trash2 size={15} aria-hidden="true" />
          </button>
        )}
      </div>
      <ul className="space-y-2">
        {items.map((c, i) => (
          <li key={i}>
            {c.not ? (
              isGroup(c.not) ? (
                <Group cond={c.not} negated onChange={(n) => update(i, { not: n })} onRemove={() => remove(i)}
                  listId={listId} ops={ops} depth={depth + 1} disabled={disabled} />
              ) : (
                <Leaf cond={c.not} negated onChange={(n) => update(i, { not: n })} onRemove={() => remove(i)}
                  onToggleNot={() => update(i, c.not!)} listId={listId} ops={ops} disabled={disabled} />
              )
            ) : isGroup(c) ? (
              <Group cond={c} onChange={(n) => update(i, n)} onRemove={() => remove(i)}
                listId={listId} ops={ops} depth={depth + 1} disabled={disabled} />
            ) : (
              <Leaf cond={c} onChange={(n) => update(i, n)} onRemove={() => remove(i)}
                onToggleNot={() => update(i, { not: c })} listId={listId} ops={ops} disabled={disabled} />
            )}
          </li>
        ))}
        {items.length === 0 && <li className="console-muted text-sm">No conditions yet: every event matches.</li>}
      </ul>
      <div className="mt-2 flex flex-wrap gap-2">
        <Button disabled={disabled} onClick={() => set([...items, { field: "", op: "eq", value: "" }])}>
          <Plus size={14} aria-hidden="true" /> Condition
        </Button>
        {depth < 3 && (
          <Button disabled={disabled} onClick={() => set([...items, { any: [{ field: "", op: "eq", value: "" }] }])}>
            <Plus size={14} aria-hidden="true" /> Group
          </Button>
        )}
      </div>
    </div>
  );
}

function Leaf({ cond, onChange, onRemove, onToggleNot, listId, ops, disabled, negated }: {
  cond: Cond;
  onChange: (c: Cond) => void;
  onRemove: () => void;
  onToggleNot: () => void;
  listId: string;
  ops: string[];
  disabled?: boolean;
  negated?: boolean;
}) {
  const op = cond.op ?? "eq";
  const setOp = (next: string) => {
    const c: Cond = { field: cond.field, op: next };
    if (next !== "exists") c.value = parseValue(next, showValue(cond.value));
    if (next === "severityAtLeast" && !severities.includes(String(cond.value))) c.value = "error";
    onChange(c);
  };
  return (
    <div className={`builder-leaf ${negated ? "builder-negated" : ""}`}>
      <button type="button" className="console-toggle builder-not" aria-pressed={!!negated} onClick={onToggleNot}
        disabled={disabled} title="Negate this condition">
        Not
      </button>
      <input aria-label="Field" list={listId} value={cond.field ?? ""} disabled={disabled} placeholder="field or $facet"
        onChange={(e) => onChange({ ...cond, field: e.target.value })} className="console-control min-w-0 font-mono" />
      <Select aria-label="Operator" value={op} disabled={disabled} onChange={(e) => setOp(e.target.value)}>
        {ops.map((o) => <option key={o} value={o}>{opLabels[o] ?? o}</option>)}
      </Select>
      {op === "exists" ? (
        <span className="console-muted text-xs">field is present</span>
      ) : op === "severityAtLeast" ? (
        <Select aria-label="Severity" value={String(cond.value ?? "error")} disabled={disabled}
          onChange={(e) => onChange({ ...cond, value: e.target.value })}>
          {severities.map((s) => <option key={s} value={s}>{s}</option>)}
        </Select>
      ) : (
        <ValueInput op={op} value={cond.value} disabled={disabled} onChange={(v) => onChange({ ...cond, value: v })} />
      )}
      <button type="button" onClick={onRemove} disabled={disabled} aria-label="Remove condition"
        className="console-muted grid size-8 place-items-center rounded hover:text-red-600">
        <Trash2 size={15} aria-hidden="true" />
      </button>
    </div>
  );
}

/** Keeps the typed text (so "1." or "a, " survive) and reports the parsed value. */
function ValueInput({ op, value, onChange, disabled }: {
  op: string;
  value: unknown;
  onChange: (v: unknown) => void;
  disabled?: boolean;
}) {
  const [text, setText] = useState(showValue(value));
  useEffect(() => {
    // Follow outside changes (YAML edits), but not our own echo.
    if (showValue(parseValue(op, text)) !== showValue(value)) setText(showValue(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, op]);
  return (
    <input aria-label="Value" value={text} disabled={disabled} placeholder={listOps.has(op) ? "a, b, c" : "value"}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parseValue(op, e.target.value));
      }}
      className="console-control min-w-0 font-mono" />
  );
}
