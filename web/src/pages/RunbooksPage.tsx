import { useEffect, useState } from "react";
import { api, type Runbook } from "../api";
import { Link, navigate } from "../router";
import { Badge, Button, Card, Notice, PageHeader } from "../ui";

export const formatLabel: Record<string, string> = {
  yaml: "YAML",
  markdown: "Markdown",
  checklist: "Checklist",
  link: "Wiki link",
  text: "Free text",
};

const chip = "bg-gray-50 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700";

/** "type = WORKFLOW_FAILED and error.code = TIMEOUT" */
export function describeCondition(c: unknown): string {
  if (!c || typeof c !== "object") return "";
  const o = c as { all?: unknown[]; any?: unknown[]; not?: unknown; field?: string; op?: string; value?: unknown };
  if (o.field) {
    const op = { eq: "=", neq: "≠", in: "in", glob: "~", gt: ">", lt: "<", gte: "≥", lte: "≤" }[o.op ?? ""] ?? o.op;
    return `${o.field} ${op} ${Array.isArray(o.value) ? o.value.join(", ") : String(o.value)}`;
  }
  if (o.all) return o.all.map(describeCondition).join(" and ");
  if (o.any) return o.any.map(describeCondition).join(" or ");
  if (o.not) return `not (${describeCondition(o.not)})`;
  return "";
}

export function RunbooksPage() {
  const [data, setData] = useState<{ runbooks: Runbook[]; errors: string[] } | null>(null);
  useEffect(() => {
    api
      .runbooks()
      .then(setData)
      .catch(() => setData({ runbooks: [], errors: [] }));
  }, []);

  return (
    <>
      <PageHeader
        title="Runbooks"
        subtitle="What to do when an alert fires. Paste them in any shape: structured YAML, a Markdown page, a checklist, a wiki link or plain text from a chat. Kardan reads the steps, fills in values from each alert, and can run the actions (risky ones after a second person approves)."
        actions={
          <Button variant="primary" onClick={() => navigate("/runbooks/new")}>
            New runbook
          </Button>
        }
      />
      {data && data.errors.length > 0 && (
        <div className="mb-4">
          <Notice tone="warn">
            <ul className="list-disc pl-5 font-mono text-xs">
              {data.errors.map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </Notice>
        </div>
      )}
      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {!data && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {data?.runbooks.length === 0 && <div className="p-4 text-sm text-gray-500">No runbooks yet.</div>}
        {data?.runbooks.map((rb) => {
          const actions = [...new Set(rb.steps.map((s) => s.action).filter(Boolean))] as string[];
          const attached = [
            ...(rb.rules ?? []).map((r) => `rule ${r}`),
            ...(rb.schemas ?? []).map((s) => `schema ${s}`),
            ...(rb.matches ? [describeCondition(rb.matches)] : []),
          ];
          return (
            <Link key={rb.id} to={`/runbooks/${encodeURIComponent(rb.id)}`} className="flex items-start gap-3 p-4 hover:bg-gray-50 dark:hover:bg-gray-800/50">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-gray-900 dark:text-gray-50">{rb.name}</span>
                  <Badge className={chip}>{formatLabel[rb.format] ?? rb.format}</Badge>
                  {rb.auto && (
                    <Badge className="bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30">
                      runs by itself
                    </Badge>
                  )}
                  <Badge className={chip}>{rb.origin === "api" ? "created in UI" : (rb.origin ?? "").replace("file:", "config/runbooks/")}</Badge>
                </div>
                <div className="mt-0.5 font-mono text-xs text-gray-500">{rb.id}</div>
                <div className="mt-1 text-xs text-gray-600 dark:text-gray-400">
                  {rb.steps.length} step{rb.steps.length === 1 ? "" : "s"}
                  {attached.length > 0 && <> · for {attached.join("; ")}</>}
                </div>
                {actions.length > 0 && (
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {actions.map((a) => (
                      <code key={a} className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-gray-800 dark:text-gray-300">
                        {a}
                      </code>
                    ))}
                  </div>
                )}
              </div>
              <div className="shrink-0 text-right text-sm text-gray-700 dark:text-gray-300">{rb.team}</div>
            </Link>
          );
        })}
      </Card>
    </>
  );
}
