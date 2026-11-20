import { useEffect, useState } from "react";
import { api, type Schema } from "../api";
import { Link } from "../router";
import { Badge, Card, FinanceChip, PageHeader, timeAgo } from "../ui";

export function SchemasPage({ now }: { now: number }) {
  const [schemas, setSchemas] = useState<Schema[] | null>(null);

  useEffect(() => {
    const load = () => api.schemas().then(setSchemas).catch(() => setSchemas([]));
    load();
    const t = window.setInterval(load, 5000);
    return () => window.clearInterval(t);
  }, []);

  return (
    <>
      <PageHeader
        title="Schemas"
        subtitle="Every payload structure Kardan has seen, detected automatically. Finance fields (ISIN, LEI, IBAN, amounts, currencies…) are recognised by their check digits and names, and mapped to facets rules can use. Nothing has to be declared up front."
      />
      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {schemas === null && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {schemas?.length === 0 && <div className="p-4 text-sm text-gray-500">No events received yet.</div>}
        {schemas?.map((s) => {
          const kinds = new Map<string, boolean>();
          for (const f of s.fields ?? []) if (f.detected) kinds.set(f.detected, (kinds.get(f.detected) ?? false) || !!f.sensitive);
          return (
            <Link key={s.id} to={`/schemas/${s.id}`} className="flex items-start gap-3 p-4 hover:bg-gray-50 dark:hover:bg-gray-800/50">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-gray-900 dark:text-gray-50">{s.name}</span>
                  <Badge className="bg-gray-100 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">{s.format}</Badge>
                  <span className="text-xs text-gray-500">from {s.sources.join(", ")}</span>
                </div>
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {[...kinds].map(([k, sensitive]) => (
                    <FinanceChip key={k} kind={k} sensitive={sensitive} />
                  ))}
                  {kinds.size === 0 && <span className="text-xs text-gray-400">no finance fields detected</span>}
                </div>
              </div>
              <div className="shrink-0 text-right text-xs text-gray-500">
                <div className="text-sm text-gray-700 tabular-nums dark:text-gray-300">{s.count.toLocaleString()} {s.count === 1 ? "event" : "events"}</div>
                <div>{(s.fields ?? []).length} fields</div>
                <div>last {timeAgo(s.lastSeen, now)}</div>
              </div>
            </Link>
          );
        })}
      </Card>
    </>
  );
}
