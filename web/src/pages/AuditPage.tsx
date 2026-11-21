import { useCallback, useEffect, useState } from "react";
import { api, type AuditEntry, type Verification } from "../api";
import { describe } from "../RunbookPanel";
import { Link } from "../router";
import { Badge, Button, Card, Notice, PageHeader } from "../ui";

const prefixes = [
  ["", "Everything"],
  ["alert:", "Alerts"],
  ["run:", "Runbook steps"],
  ["rule:", "Rules"],
  ["runbook:", "Runbooks"],
  ["schema:", "Schemas"],
  ["clock", "Clock"],
] as const;

/** The hash-chained audit trail and its verification. */
export function AuditPage() {
  const [prefix, setPrefix] = useState("");
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [verify, setVerify] = useState<Verification | null>(null);
  const [checking, setChecking] = useState(false);

  const load = useCallback(() => {
    api
      .audit(prefix, 300)
      .then(setEntries)
      .catch(() => setEntries([]));
  }, [prefix]);

  const check = useCallback(() => {
    setChecking(true);
    api
      .verifyAudit()
      .then(setVerify)
      .catch(() => setVerify(null))
      .finally(() => setChecking(false));
  }, []);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 5000);
    return () => window.clearInterval(t);
  }, [load]);
  useEffect(check, [check]);

  const highlight = Number(window.location.hash.slice(1)) || 0;

  return (
    <>
      <PageHeader
        title="Audit trail"
        subtitle="Every acknowledgement, approval, runbook action and configuration change, append-only. Each entry stores the hash of the one before it, so changing or deleting any entry breaks the chain from that point."
        actions={
          <Button onClick={check} disabled={checking}>
            {checking ? "Verifying…" : "Verify chain"}
          </Button>
        }
      />
      {verify && (
        <div className="mb-4">
          {verify.ok ? (
            <Notice tone="info">
              ✔ Chain intact: {verify.entries} entries verified.
              {verify.head && (
                <>
                  {" "}
                  Head hash <code className="font-mono text-xs break-all">{verify.head}</code>
                </>
              )}
            </Notice>
          ) : (
            <Notice tone="error">
              ✖ Chain broken at entry <b>#{verify.brokenAt}</b>: {verify.reason}.
            </Notice>
          )}
        </div>
      )}
      <div className="mb-3 flex flex-wrap gap-1" role="group" aria-label="Audit category">
        {prefixes.map(([p, label]) => (
          <button
            key={p}
            type="button"
            aria-pressed={prefix === p}
            onClick={() => setPrefix(p)}
            className="console-toggle"
          >
            {label}
          </button>
        ))}
      </div>
      <Card className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-gray-500">
            <tr>
              <th className="px-3 py-2 font-medium">#</th>
              <th className="px-3 py-2 font-medium">Time</th>
              <th className="px-3 py-2 font-medium">Who</th>
              <th className="px-3 py-2 font-medium">What</th>
              <th className="px-3 py-2 font-medium">Subject</th>
              <th className="px-3 py-2 font-medium">Hash</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
            {entries === null && (
              <tr>
                <td colSpan={6} className="px-3 py-3 text-gray-500">
                  Loading…
                </td>
              </tr>
            )}
            {entries?.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-3 text-gray-500">
                  Nothing recorded yet.
                </td>
              </tr>
            )}
            {entries?.map((e) => {
              const broken = verify && !verify.ok && verify.brokenAt === e.seq;
              return (
                <tr key={e.seq} id={String(e.seq)} className={broken ? "bg-red-50 dark:bg-red-500/10" : highlight === e.seq ? "bg-amber-50 dark:bg-amber-500/10" : ""}>
                  <td className="px-3 py-2 font-mono text-xs text-gray-500">{e.seq}</td>
                  <td className="px-3 py-2 text-xs whitespace-nowrap text-gray-500">{new Date(e.at).toLocaleString()}</td>
                  <td className="px-3 py-2 whitespace-nowrap">{e.actor}</td>
                  <td className="px-3 py-2">
                    <Badge className="bg-gray-50 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">{e.action}</Badge>{" "}
                    <span className="text-gray-600 dark:text-gray-300">{describe(e) !== e.action ? describe(e) : ""}</span>
                  </td>
                  <td className="px-3 py-2 font-mono text-xs">
                    <SubjectLink subject={e.subject} detail={e.detail} />
                  </td>
                  <td className="px-3 py-2 font-mono text-[11px] text-gray-500" title={`hash ${e.hash}\nprevious ${e.prevHash}`}>
                    {e.hash.slice(0, 12)}…
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>
    </>
  );
}

function SubjectLink({ subject, detail }: { subject: string; detail: Record<string, unknown> }) {
  const [kind, id] = subject.split(":");
  const alert = typeof detail?.alert === "string" ? detail.alert : kind === "alert" ? id : "";
  if (alert) return <Link to={`/alerts/${alert}`} className="text-amber-700 hover:underline dark:text-amber-400">{subject}</Link>;
  if (kind === "rule") return <Link to={`/rules/${encodeURIComponent(id)}`} className="text-amber-700 hover:underline dark:text-amber-400">{subject}</Link>;
  if (kind === "runbook") return <Link to={`/runbooks/${encodeURIComponent(id)}`} className="text-amber-700 hover:underline dark:text-amber-400">{subject}</Link>;
  return <>{subject}</>;
}
