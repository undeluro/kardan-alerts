import { useEffect, useState } from "react";
import { api, type CalendarStatus } from "../api";
import { cutoffUrgency, shortDuration, useBusinessNow } from "../clock";
import { Badge, Card, Notice, PageHeader } from "../ui";

const openTone = "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30";
const closedTone = "bg-gray-50 text-gray-600 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700";
const holidayTone = "bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30";

const leftTone = {
  missed: "text-red-700 dark:text-red-300",
  critical: "text-red-700 dark:text-red-300 font-semibold",
  soon: "text-amber-700 dark:text-amber-300 font-semibold",
  later: "text-gray-600 dark:text-gray-300",
} as const;

/** "Fri 18:00 EST": a cut-off in the market's own time zone. */
const inZone = (iso: string, timeZone?: string, zoneName = true) =>
  new Date(iso).toLocaleString([], {
    timeZone,
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
    ...(zoneName ? { timeZoneName: "short" } : {}),
  });

/** Market and payment-system calendars: open or closed now, holidays, and the next cut-offs. */
export function MarketsPage() {
  const [data, setData] = useState<{ now: string; note?: string; calendars: CalendarStatus[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const bizNow = useBusinessNow(5_000);

  useEffect(() => {
    const load = () =>
      api
        .calendars()
        .then((d) => {
          setData(d);
          setError(null);
        })
        .catch((e: Error) => setError(e.message));
    load();
    const t = window.setInterval(load, 30_000);
    return () => window.clearInterval(t);
  }, []);

  // The soonest cut-offs across every calendar, for the strip at the top.
  const upcoming = (data?.calendars ?? [])
    .flatMap((c) => c.cutoffs.map((d) => ({ ...d, calendarName: c.name, timezone: c.timezone })))
    .filter((d) => Date.parse(d.at) > bizNow)
    .sort((a, b) => Date.parse(a.at) - Date.parse(b.at))
    .slice(0, 4);

  return (
    <>
      <PageHeader
        title="Markets & cut-offs"
        subtitle={
          <>
            Business days, holidays, opening hours and daily cut-offs from <code className="font-mono">config/calendars.yaml</code>{" "}
            (reloaded live). Rules use them for market-hours-only alerts (<code className="font-mono">during</code>), absence deadlines
            and the cut-off countdown that raises an alert's impact and tightens its escalations.
          </>
        }
      />
      {error && <Notice tone="error">{error}</Notice>}
      {data?.note && (
        <div className="mb-4">
          <Notice tone="warn">{data.note}</Notice>
        </div>
      )}

      {upcoming.length > 0 && (
        <div className="mb-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {upcoming.map((d) => {
            const { left, level } = cutoffUrgency(d.at, bizNow);
            return (
              <Card key={d.calendar + d.name} className="p-3">
                <div className="text-xs text-gray-500 dark:text-gray-400">{d.calendarName}</div>
                <div className="mt-0.5 truncate text-sm font-medium" title={d.name}>
                  {d.name}
                </div>
                <div className={`mt-1 text-lg tabular-nums ${leftTone[level]}`}>⏱ {shortDuration(left)}</div>
                <div className="text-xs text-gray-500" title={`Your time: ${inZone(d.at)}`}>
                  {inZone(d.at, d.timezone)}
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <Card className="divide-y divide-gray-100 dark:divide-gray-800">
        {!data && !error && <div className="p-4 text-sm text-gray-500">Loading…</div>}
        {data?.calendars.length === 0 && (
          <div className="p-4 text-sm text-gray-500">No calendars configured. Add some to config/calendars.yaml.</div>
        )}
        {data?.calendars.map((c) => (
          <div key={c.id} className="flex flex-wrap items-start gap-4 p-4">
            <div className="w-56 min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{c.name}</span>
              </div>
              <div className="mt-0.5 font-mono text-xs text-gray-500">{c.id}</div>
            </div>
            <div className="w-44 text-sm">
              <div className="tabular-nums">{c.localTime}</div>
              <div className="text-xs text-gray-500">{c.timezone}</div>
            </div>
            <div className="flex w-52 flex-wrap items-center gap-1.5">
              <Badge className={c.open ? openTone : closedTone}>{c.open ? "open" : "closed"}</Badge>
              {c.holiday && <Badge className={holidayTone}>holiday</Badge>}
              {!c.holiday && !c.businessDay && <Badge className={closedTone}>weekend</Badge>}
              <span className="text-xs text-gray-500">{c.hours ? `hours ${c.hours}` : "all day"}</span>
            </div>
            <ul className="min-w-0 flex-1 space-y-1 text-sm">
              {c.cutoffs.length === 0 && <li className="text-xs text-gray-400">no cut-offs (used for hours and business days)</li>}
              {c.cutoffs.map((d) => {
                const { left, level } = cutoffUrgency(d.at, bizNow);
                return (
                  <li key={d.name} className="flex flex-wrap items-baseline justify-between gap-x-3">
                    <span className="truncate">{d.name}</span>
                    <span className="text-xs tabular-nums">
                      <span className="text-gray-500" title={`Your time: ${inZone(d.at)}`}>
                        {inZone(d.at, c.timezone, false)}
                      </span>{" "}
                      <span className={leftTone[level]}>{level === "missed" ? "passed" : `in ${shortDuration(left)}`}</span>
                    </span>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </Card>
    </>
  );
}
