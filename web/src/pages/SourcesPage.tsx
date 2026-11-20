import { useEffect, useState } from "react";
import { api, type SourceStatus } from "../api";
import { Badge, Card, Notice, PageHeader, timeAgo } from "../ui";

const typeLabel = { http: "HTTP webhook", kafka: "Kafka", file: "Log file" } as const;

export function SourcesPage({ now }: { now: number }) {
  const [sources, setSources] = useState<SourceStatus[] | null>(null);

  useEffect(() => {
    const load = () => api.sources().then(setSources).catch(() => setSources([]));
    load();
    const t = window.setInterval(load, 3000);
    return () => window.clearInterval(t);
  }, []);

  return (
    <>
      <PageHeader
        title="Sources"
        subtitle="Where events come from. Declared in config/sources.yaml and applied live. Any HTTP client can also post to /ingest/<source-id> without being declared; the format (JSON, XML / ISO 20022, FIX, logfmt, text) is detected automatically."
      />
      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {sources === null && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {sources?.length === 0 && <div className="p-4 text-sm text-gray-500">No sources declared (HTTP ingest still works).</div>}
        {sources?.map((s) => (
          <div key={s.id} className="flex flex-wrap items-start gap-3 p-4">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{s.id}</span>
                <Badge className="bg-gray-100 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">{typeLabel[s.type]}</Badge>
                <Badge className="bg-gray-50 text-gray-600 ring-gray-200 dark:bg-gray-900 dark:text-gray-400 dark:ring-gray-700">parser: {s.parser || "auto"}</Badge>
                {s.type !== "http" &&
                  (s.running ? (
                    <Badge className="bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300">
                      running{s.type === "file" ? (s.owner ? " · this replica holds the lease" : " · standby") : ""}
                    </Badge>
                  ) : (
                    <Badge className="bg-red-50 text-red-700 ring-red-600/20">stopped</Badge>
                  ))}
              </div>
              {s.description && <div className="mt-0.5 text-sm text-gray-600 dark:text-gray-300">{s.description}</div>}
              <div className="mt-1 font-mono text-xs text-gray-500">
                {s.type === "kafka" && `topics: ${(s.topics ?? []).join(", ")}`}
                {s.type === "file" && `path: ${s.path}`}
                {s.type === "http" && `POST /ingest/${s.id}`}
              </div>
              {s.error && (
                <div className="mt-2">
                  <Notice tone="error">{s.error}</Notice>
                </div>
              )}
            </div>
            {s.type !== "http" && (
              <div className="shrink-0 text-right text-xs text-gray-500">
                <div className="text-sm text-gray-700 tabular-nums dark:text-gray-300">{s.events.toLocaleString()} events</div>
                <div>{s.lastSeen ? `last ${timeAgo(s.lastSeen, now)}` : "none yet"}</div>
              </div>
            )}
          </div>
        ))}
      </Card>
      <details className="mt-4 rounded-md bg-white p-4 text-sm ring-1 ring-gray-200 dark:bg-gray-900 dark:ring-gray-800">
        <summary className="cursor-pointer font-medium">Add a source</summary>
        <pre className="mt-3 overflow-x-auto rounded bg-gray-950 p-3 font-mono text-xs text-gray-100">{`# config/sources.yaml
sources:
  - id: settlement-engine
    type: kafka
    topics: [settlement.events]
    parser: json

  - id: matching-engine-logs
    type: file
    path: /var/log/matching/*.log
    parser: logfmt        # or regex with named groups, fix, text`}</pre>
      </details>
    </>
  );
}
