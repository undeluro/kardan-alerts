import { Pencil, Plus, Rocket, Undo2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, type ClockState, type OnCallNow, type Origin, type Person, type Preference, type Team } from "../api";
import { PersonSheet, TeamSheet } from "../DirectoryEditors";
import { Link } from "../router";
import { Badge, Button, Card, ChannelIcon, Notice, PageHeader, channelLabel } from "../ui";

type Editing = { kind: "person"; value: Person | null } | { kind: "team"; value: Team | null } | null;

export function TeamsPage({ onClockChange, onToast }: { onClockChange: () => void; onToast: (m: string) => void }) {
  const [teams, setTeams] = useState<Team[]>([]);
  const [people, setPeople] = useState<Map<string, Person>>(new Map());
  const [defaults, setDefaults] = useState<Preference[]>([]);
  const [onCall, setOnCall] = useState<OnCallNow[]>([]);
  const [clock, setClock] = useState<ClockState | null>(null);
  const [editable, setEditable] = useState(false);
  const [editing, setEditing] = useState<Editing>(null);

  const load = useCallback(() => {
    api.teams().then((d) => {
      setTeams(d.teams);
      setPeople(new Map(d.people.map((p) => [p.id, p])));
      setDefaults(d.defaultPreferences ?? []);
      setEditable(!!d.editable);
    });
    api.onCall().then((d) => setOnCall(d.onCall));
    api.clock().then(setClock);
  }, []);

  useEffect(() => {
    load();
    const t = window.setInterval(load, 10_000);
    return () => window.clearInterval(t);
  }, [load]);

  const shift = async (minutes: number | null) => {
    const next = minutes === null ? 0 : (clock?.offsetMinutes ?? 0) + minutes;
    setClock(await api.setClock(next));
    onClockChange();
    load();
  };

  const revert = async (kind: "person" | "team", id: string, origin?: Origin) => {
    const what = origin === "api" ? `Delete ${kind} ${id}? It was created in the UI.` : `Discard UI edits to ${kind} ${id} and use teams.yaml again?`;
    if (!window.confirm(what)) return;
    try {
      await (kind === "team" ? api.revertTeam(id) : api.revertPerson(id));
      onToast(origin === "api" ? `${kind} ${id} deleted` : `${kind} ${id} restored from teams.yaml`);
      load();
    } catch (e) {
      onToast((e as Error).message);
    }
  };
  const saved = (msg: string) => {
    setEditing(null);
    onToast(msg);
    load();
  };

  const oc = new Map(onCall.map((o) => [o.team, o]));
  const now = clock ? new Date(clock.now) : new Date();

  return (
    <>
      <PageHeader
        title="Teams & on-call"
        subtitle="Who gets what, when. Rules route to a team, to whoever is on call (rotation or follow-the-sun), or to a person's own preferences: severity and working hours in their time zone. Edit here or in config/teams.yaml; changes apply live."
        actions={editable && (
          <>
            <Link to="/onboarding" className="console-button-secondary inline-flex min-h-8 items-center gap-2 rounded-sm px-3 py-1.5 text-sm font-medium">
              <Rocket size={14} aria-hidden="true" /> Onboard a team
            </Link>
            <Button onClick={() => setEditing({ kind: "person", value: null })}><Plus size={14} aria-hidden="true" /> Person</Button>
            <Button variant="primary" onClick={() => setEditing({ kind: "team", value: null })}><Plus size={14} aria-hidden="true" /> Team</Button>
          </>
        )}
      />

      {clock?.demoMode && (
        <Card className="mb-4 p-4">
          <div className="flex flex-wrap items-center gap-3">
            <div>
              <div className="text-xs font-medium tracking-wide text-gray-500 uppercase">Business clock (demo mode)</div>
              <div className="text-lg font-semibold tabular-nums">
                {now.toLocaleString([], { weekday: "short", day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit" })}
                {clock.offsetMinutes !== 0 && (
                  <span className="ml-2 text-sm font-normal text-amber-600">
                    ({clock.offsetMinutes > 0 ? "+" : ""}
                    {(clock.offsetMinutes / 60).toFixed(1)} h simulated)
                  </span>
                )}
              </div>
            </div>
            <div className="ml-auto flex flex-wrap gap-1.5">
              <Button onClick={() => void shift(-60)}>−1 h</Button>
              <Button onClick={() => void shift(60)}>+1 h</Button>
              <Button onClick={() => void shift(6 * 60)}>+6 h</Button>
              <Button onClick={() => void shift(24 * 60)}>+1 day</Button>
              <Button onClick={() => void shift(7 * 24 * 60)}>+1 week</Button>
              <Button variant="primary" onClick={() => void shift(null)} disabled={clock.offsetMinutes === 0}>
                Back to real time
              </Button>
            </div>
          </div>
          <p className="mt-2 text-xs text-gray-500">
            Shifts the time used for working-hours preferences and on-call schedules on every replica, so you can see who would be
            paged at 21:00 or next week without waiting.
          </p>
        </Card>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        {teams.map((t) => {
          const current = oc.get(t.id);
          return (
            <Card key={t.id} className="p-4">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="font-semibold">{t.name || t.id}</h2>
                <code className="font-mono text-xs text-gray-500">{t.id}</code>
                <OriginBadge origin={t.origin} />
                {editable && (
                  <span className="ml-auto flex gap-1">
                    {t.origin && t.origin !== "file" && (
                      <Button onClick={() => void revert("team", t.id, t.origin)}><Undo2 size={13} aria-hidden="true" /> {t.origin === "api" ? "Delete" : "Revert"}</Button>
                    )}
                    <Button onClick={() => setEditing({ kind: "team", value: t })}><Pencil size={13} aria-hidden="true" /> Edit</Button>
                  </span>
                )}
              </div>
              {current && (
                <div className="mt-2 rounded-md bg-emerald-50 p-2.5 text-sm ring-1 ring-emerald-200 dark:bg-emerald-500/10 dark:ring-emerald-500/30">
                  <span className="font-medium">On call now: {current.person.name}</span>
                  <div className="text-xs text-gray-600 dark:text-gray-300">{current.reason}</div>
                </div>
              )}
              <Schedule team={t} people={people} />
              <div className="mt-3 text-xs font-medium tracking-wide text-gray-500 uppercase">Members</div>
              <ul className="mt-1 divide-y divide-gray-100 dark:divide-gray-800">
                {t.members.map((id) => {
                  const p = people.get(id);
                  if (!p) return null;
                  return (
                    <li key={id} className="py-2 text-sm">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{p.name}</span>
                        <span className="text-xs text-gray-500">{p.timezone}</span>
                        <span className="text-xs text-gray-500">· local {localTime(now, p.timezone)}</span>
                        <OriginBadge origin={p.origin} />
                        {editable && (
                          <span className="ml-auto flex gap-1">
                            {p.origin && p.origin !== "file" && p.origin !== "api" && (
                              <button type="button" className="console-muted text-xs hover:underline" onClick={() => void revert("person", p.id, p.origin)}>Revert</button>
                            )}
                            <button type="button" className="inline-flex items-center gap-1 text-xs font-medium text-[var(--console-accent)] hover:underline"
                              onClick={() => setEditing({ kind: "person", value: p })}>
                              <Pencil size={12} aria-hidden="true" /> Edit
                            </button>
                          </span>
                        )}
                      </div>
                      <Prefs prefs={p.preferences} fallback={t.preferences?.length ? "team" : "default"} />
                    </li>
                  );
                })}
              </ul>
              {t.preferences && t.preferences.length > 0 && (
                <>
                  <div className="mt-2 text-xs font-medium tracking-wide text-gray-500 uppercase">Team preferences</div>
                  <Prefs prefs={t.preferences} />
                </>
              )}
            </Card>
          );
        })}
      </div>

      {editing?.kind === "person" && (
        <PersonSheet person={editing.value} defaults={defaults} onClose={() => setEditing(null)} onSaved={saved} />
      )}
      {editing?.kind === "team" && (
        <TeamSheet team={editing.value} people={[...people.values()]} defaults={defaults} onClose={() => setEditing(null)} onSaved={saved} />
      )}

      {defaults.length > 0 && (
        <div className="mt-4">
          <Notice>
            <b>Default preferences</b> (when neither the person nor the team sets any):{" "}
            {defaults.map((p, i) => (
              <span key={i} className="mr-3">
                {p.severity ?? "any"} → {p.channels.join(" + ")}
              </span>
            ))}
          </Notice>
        </div>
      )}
    </>
  );
}

function localTime(now: Date, tz?: string) {
  try {
    return now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", timeZone: tz || "UTC" });
  } catch {
    return "";
  }
}

function Prefs({ prefs, fallback }: { prefs?: Preference[]; fallback?: string }) {
  if (!prefs || prefs.length === 0) {
    return fallback ? <div className="text-xs text-gray-400">uses {fallback} preferences</div> : null;
  }
  return (
    <ul className="mt-1 space-y-0.5 text-xs">
      {prefs.map((p, i) => (
        <li key={i} className="flex flex-wrap items-center gap-1.5">
          <Badge className="bg-gray-100 text-gray-700 ring-gray-300 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">
            {p.severity ? `${p.severity}+` : "any"}
          </Badge>
          {p.channels.map((channel) => (
            <span key={channel} className="console-muted inline-flex items-center gap-1">
              <ChannelIcon channel={channel} size={13} />{channelLabel(channel)}
            </span>
          ))}
          {p.between && <span className="text-gray-500">between {p.between}</span>}
          {p.days && <span className="text-gray-500">on {p.days}</span>}
        </li>
      ))}
    </ul>
  );
}

function Schedule({ team, people }: { team: Team; people: Map<string, Person> }) {
  const oc = team.onCall;
  if (!oc) return null;
  const name = (id: string) => people.get(id)?.name ?? id;
  return (
    <div className="mt-2 space-y-0.5 text-xs text-gray-600 dark:text-gray-300">
      {oc.shifts && oc.shifts.length > 0 && (
        <div>
          <b>Follow-the-sun:</b>{" "}
          {oc.shifts.map((s, i) => (
            <span key={i} className="mr-2">
              {name(s.person)} {s.between} {s.timezone ?? people.get(s.person)?.timezone}
            </span>
          ))}
        </div>
      )}
      {oc.rotation && (
        <div>
          <b>{oc.rotation.every === "day" ? "Daily" : "Weekly"} rotation:</b> {oc.rotation.people.map(name).join(" → ")} (from{" "}
          {oc.rotation.start} {oc.timezone})
        </div>
      )}
      {oc.overrides?.map((o, i) => (
        <div key={i}>
          <b>Override:</b> {name(o.person)} {new Date(o.from).toLocaleString()} – {new Date(o.to).toLocaleString()}
        </div>
      ))}
    </div>
  );
}

function OriginBadge({ origin }: { origin?: Origin }) {
  if (!origin || origin === "file") return null;
  return (
    <Badge className="bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30">
      {origin === "api" ? "created in UI" : "edited in UI"}
    </Badge>
  );
}
