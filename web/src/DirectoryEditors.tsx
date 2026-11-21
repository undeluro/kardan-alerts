import * as Dialog from "@radix-ui/react-dialog";
import { ArrowDown, ArrowUp, Plus, Trash2, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api, type Person, type Preference, type Team } from "./api";
import { Button, ChannelIcon, Notice, Select, channelLabel, inputClass } from "./ui";

export const CHANNELS = ["email", "sms", "phone", "ui"] as const;
const SEVERITIES = ["critical", "error", "warning", "info"] as const;
const levels: Record<string, number> = { debug: 0, info: 1, warning: 2, error: 3, critical: 4 };

export const TIMEZONES = [
  "UTC", "Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Zurich", "America/New_York", "America/Chicago",
  "America/Sao_Paulo", "Asia/Hong_Kong", "Asia/Singapore", "Asia/Tokyo", "Asia/Kolkata", "Australia/Sydney",
];

/** Minutes since midnight in a time zone, for previewing "between" windows. */
function localMinutes(now: Date, tz?: string) {
  try {
    const [h, m] = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: tz || "UTC" })
      .format(now).split(":").map(Number);
    return (h % 24) * 60 + m;
  } catch {
    return now.getUTCHours() * 60 + now.getUTCMinutes();
  }
}

function inWindow(p: Preference, minutes: number) {
  if (!p.between) return true;
  const m = /^(\d{1,2}):(\d{2})\s*-\s*(\d{1,2}):(\d{2})$/.exec(p.between.trim());
  if (!m) return true;
  const start = Number(m[1]) * 60 + Number(m[2]);
  const end = Number(m[3]) * 60 + Number(m[4]);
  return start <= end ? minutes >= start && minutes < end : minutes >= start || minutes < end;
}

/** First matching preference wins, as on the server (days are not previewed). */
export function channelsFor(prefs: Preference[], severity: string, minutes: number): string[] | null {
  for (const p of prefs) {
    if (p.severity && levels[severity] < (levels[p.severity] ?? 0)) continue;
    if (!inWindow(p, minutes)) continue;
    return p.channels;
  }
  return null;
}

export function PreferencesEditor({ value, onChange, timezone, fallback }: {
  value: Preference[];
  onChange: (p: Preference[]) => void;
  timezone?: string;
  fallback: Preference[];
}) {
  const set = (i: number, p: Partial<Preference>) => onChange(value.map((x, j) => (j === i ? clean({ ...x, ...p }) : x)));
  const move = (i: number, d: -1 | 1) => {
    const next = [...value];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    onChange(next);
  };
  const toggle = (i: number, ch: string) => {
    const has = value[i].channels.includes(ch);
    set(i, { channels: has ? value[i].channels.filter((c) => c !== ch) : [...value[i].channels, ch] });
  };
  const minutes = localMinutes(new Date(), timezone);
  const effective = value.length ? value : fallback;

  return (
    <div>
      <div className="overflow-x-auto">
        <table className="pref-matrix w-full text-sm">
          <thead>
            <tr>
              <th scope="col">From severity</th>
              {CHANNELS.map((ch) => (
                <th key={ch} scope="col" className="text-center">
                  <span className="inline-flex items-center gap-1"><ChannelIcon channel={ch} size={13} />{channelLabel(ch)}</span>
                </th>
              ))}
              <th scope="col">Hours (local)</th>
              <th scope="col">Days</th>
              <th scope="col"><span className="sr-only">Order</span></th>
            </tr>
          </thead>
          <tbody>
            {value.map((p, i) => (
              <tr key={i}>
                <td>
                  <Select aria-label={`Rule ${i + 1} severity`} value={p.severity ?? ""} onChange={(e) => set(i, { severity: e.target.value || undefined })}>
                    <option value="">any</option>
                    {SEVERITIES.map((s) => <option key={s} value={s}>{s}+</option>)}
                  </Select>
                </td>
                {CHANNELS.map((ch) => (
                  <td key={ch} className="text-center">
                    <input type="checkbox" className="pref-check" checked={p.channels.includes(ch)} onChange={() => toggle(i, ch)}
                      aria-label={`Rule ${i + 1}: ${channelLabel(ch)}`} />
                  </td>
                ))}
                <td>
                  <input className={`${inputClass} font-mono`} value={p.between ?? ""} placeholder="always" aria-label={`Rule ${i + 1} hours`}
                    onChange={(e) => set(i, { between: e.target.value || undefined })} />
                </td>
                <td>
                  <input className={inputClass} value={p.days ?? ""} placeholder="every day" aria-label={`Rule ${i + 1} days`}
                    onChange={(e) => set(i, { days: e.target.value || undefined })} />
                </td>
                <td>
                  <div className="flex gap-0.5">
                    <IconButton label="Move up" disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={14} /></IconButton>
                    <IconButton label="Move down" disabled={i === value.length - 1} onClick={() => move(i, 1)}><ArrowDown size={14} /></IconButton>
                    <IconButton label="Remove" onClick={() => onChange(value.filter((_, j) => j !== i))}><Trash2 size={14} /></IconButton>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {value.length === 0 && <p className="console-muted mt-2 text-sm">No own preferences: the fallback below applies.</p>}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <Button onClick={() => onChange([...value, { severity: "critical", channels: ["sms"] }])}>
          <Plus size={14} aria-hidden="true" /> Add preference
        </Button>
        <span className="console-muted text-xs">Checked top to bottom; the first row that fits the severity and time wins. Hours like 08:00-20:00, days like Mon-Fri.</span>
      </div>
      <div className="mt-3 rounded-md border border-[var(--console-line)] bg-[var(--console-control)] p-2.5">
        <div className="console-muted text-xs font-medium">Right now{timezone ? ` (${timezone})` : ""} this means</div>
        <ul className="mt-1 grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-4">
          {SEVERITIES.map((s) => {
            const ch = channelsFor(effective, s, minutes);
            return (
              <li key={s} className="flex flex-wrap items-center gap-1.5">
                <span className="capitalize">{s}</span>
                <span aria-hidden="true">→</span>
                {ch ? ch.map((c) => <ChannelIcon key={c} channel={c} size={14} />) : <span className="console-muted">nothing</span>}
                <span className="sr-only">{ch ? ch.map(channelLabel).join(", ") : "nothing"}</span>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}

function clean(p: Preference): Preference {
  const out: Preference = { channels: p.channels };
  if (p.severity) out.severity = p.severity;
  if (p.between) out.between = p.between;
  if (p.days) out.days = p.days;
  return out;
}

function IconButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} aria-label={label} title={label}
      className="console-muted grid size-7 place-items-center rounded hover:text-[var(--console-ink)] disabled:opacity-30">
      {children}
    </button>
  );
}

function Sheet({ title, description, onClose, children, footer }: {
  title: string;
  description: string;
  onClose: () => void;
  children: ReactNode;
  footer: ReactNode;
}) {
  return (
    <Dialog.Root open onOpenChange={(open) => { if (!open) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col border-l border-[var(--console-line)] bg-[var(--console-surface)] text-[var(--console-ink)] shadow-2xl">
          <header className="flex items-start justify-between gap-3 border-b border-[var(--console-line)] p-4 sm:p-5">
            <div>
              <Dialog.Title className="text-lg font-semibold">{title}</Dialog.Title>
              <Dialog.Description className="console-muted mt-0.5 text-sm">{description}</Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button type="button" aria-label="Close" className="console-muted grid size-9 place-items-center rounded-md hover:bg-[var(--console-canvas)]">
                <X size={18} aria-hidden="true" />
              </button>
            </Dialog.Close>
          </header>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-5">{children}</div>
          <footer className="flex flex-wrap justify-end gap-2 border-t border-[var(--console-line)] p-4 sm:p-5">{footer}</footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block text-sm">
      <span className="font-medium">{label}</span>
      <div className="mt-1">{children}</div>
      {hint && <span className="console-muted mt-1 block text-xs">{hint}</span>}
    </label>
  );
}

const slug = (s: string) => s.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);

export function PersonSheet({ person, defaults, onClose, onSaved }: {
  person: Person | null; // null: new person
  defaults: Preference[];
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [draft, setDraft] = useState<Person>(person ?? { id: "", name: "", timezone: "Europe/London", preferences: [] });
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const isNew = !person;
  const id = isNew ? slug(draft.id || draft.name) : draft.id;
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await api.savePerson({ ...draft, id, origin: undefined });
      onSaved(`${draft.name} saved. Routing uses it within seconds.`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Sheet title={isNew ? "Add person" : `Edit ${person.name}`} onClose={onClose}
      description="Contact details and when to reach this person. Saved in the database on top of teams.yaml."
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={saving || !draft.name.trim() || !id} onClick={() => void save()}>{saving ? "Saving…" : "Save person"}</Button>
      </>}>
      {error && <Notice tone="error">{error}</Notice>}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Name"><input className={inputClass} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></Field>
        <Field label="ID" hint={isNew ? "Used in rules: user:" + (id || "…") : undefined}>
          <input className={`${inputClass} font-mono`} value={isNew ? draft.id || id : draft.id} disabled={!isNew}
            onChange={(e) => setDraft({ ...draft, id: slug(e.target.value) })} />
        </Field>
        <Field label="Email"><input className={inputClass} type="email" value={draft.email ?? ""} onChange={(e) => setDraft({ ...draft, email: e.target.value || undefined })} /></Field>
        <Field label="Phone" hint="Used for SMS, calls and APPROVE replies">
          <input className={inputClass} value={draft.phone ?? ""} onChange={(e) => setDraft({ ...draft, phone: e.target.value || undefined })} />
        </Field>
        <Field label="Time zone">
          <Select value={draft.timezone ?? "UTC"} onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}>
            {[...new Set([draft.timezone ?? "UTC", ...TIMEZONES])].map((tz) => <option key={tz} value={tz}>{tz}</option>)}
          </Select>
        </Field>
      </div>
      <section>
        <h3 className="mb-2 font-semibold">Notification preferences</h3>
        <PreferencesEditor value={draft.preferences ?? []} onChange={(p) => setDraft({ ...draft, preferences: p })}
          timezone={draft.timezone} fallback={defaults} />
      </section>
    </Sheet>
  );
}

export function TeamSheet({ team, people, defaults, onClose, onSaved }: {
  team: Team | null;
  people: Person[];
  defaults: Preference[];
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [draft, setDraft] = useState<Team>(team ?? { id: "", name: "", members: [], preferences: [] });
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const isNew = !team;
  const id = isNew ? slug(draft.id || draft.name) : draft.id;
  const rotation = draft.onCall?.rotation;
  const setRotation = (r: typeof rotation) =>
    setDraft({ ...draft, onCall: r ? { ...draft.onCall, timezone: draft.onCall?.timezone ?? "Europe/London", rotation: r } : { ...draft.onCall, rotation: undefined } });
  const toggleMember = (pid: string) =>
    setDraft({ ...draft, members: draft.members.includes(pid) ? draft.members.filter((m) => m !== pid) : [...draft.members, pid] });
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const onCall = draft.onCall && (draft.onCall.rotation || draft.onCall.shifts?.length || draft.onCall.overrides?.length) ? draft.onCall : undefined;
      await api.saveTeam({ ...draft, id, onCall, origin: undefined });
      onSaved(`${draft.name || id} saved.`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Sheet title={isNew ? "Add team" : `Edit ${team.name}`} onClose={onClose}
      description="Members, team-wide preferences and the on-call rotation. Rules route with team:, oncall: or user: targets."
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={saving || !id} onClick={() => void save()}>{saving ? "Saving…" : "Save team"}</Button>
      </>}>
      {error && <Notice tone="error">{error}</Notice>}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Name"><input className={inputClass} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></Field>
        <Field label="ID" hint={isNew ? "Used in rules: team:" + (id || "…") : undefined}>
          <input className={`${inputClass} font-mono`} value={isNew ? draft.id || id : draft.id} disabled={!isNew}
            onChange={(e) => setDraft({ ...draft, id: slug(e.target.value) })} />
        </Field>
      </div>
      <section>
        <h3 className="mb-2 font-semibold">Members</h3>
        <ul className="grid gap-1 sm:grid-cols-2">
          {people.map((p) => (
            <li key={p.id}>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={draft.members.includes(p.id)} onChange={() => toggleMember(p.id)} />
                {p.name} <span className="console-muted text-xs">{p.timezone}</span>
              </label>
            </li>
          ))}
        </ul>
      </section>
      <section>
        <h3 className="mb-2 font-semibold">On-call rotation</h3>
        {draft.onCall?.shifts?.length ? <p className="console-muted mb-2 text-xs">Follow-the-sun shifts and overrides from teams.yaml are kept as they are.</p> : null}
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={!!rotation}
            onChange={(e) => setRotation(e.target.checked ? { every: "week", start: new Date().toISOString().slice(0, 10) + "T09:00", people: [...draft.members] } : undefined)} />
          Hand over on a schedule
        </label>
        {rotation && (
          <div className="mt-2 grid gap-3 sm:grid-cols-3">
            <Field label="Every">
              <Select value={rotation.every} onChange={(e) => setRotation({ ...rotation, every: e.target.value })}>
                <option value="day">day</option>
                <option value="week">week</option>
              </Select>
            </Field>
            <Field label="First hand-over">
              <input className={inputClass} type="datetime-local" value={rotation.start} onChange={(e) => setRotation({ ...rotation, start: e.target.value })} />
            </Field>
            <Field label="Time zone">
              <Select value={draft.onCall?.timezone ?? "Europe/London"}
                onChange={(e) => setDraft({ ...draft, onCall: { ...draft.onCall, timezone: e.target.value } })}>
                {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
              </Select>
            </Field>
            <div className="sm:col-span-3">
              <span className="text-sm font-medium">Order</span>
              <ol className="mt-1 flex flex-wrap gap-1.5">
                {rotation.people.map((pid, i) => (
                  <li key={pid} className="inline-flex items-center gap-1 rounded border border-[var(--console-line)] px-2 py-0.5 text-sm">
                    {i + 1}. {people.find((p) => p.id === pid)?.name ?? pid}
                    <IconButton label="Earlier" disabled={i === 0} onClick={() => {
                      const next = [...rotation.people];
                      [next[i - 1], next[i]] = [next[i], next[i - 1]];
                      setRotation({ ...rotation, people: next });
                    }}><ArrowUp size={12} /></IconButton>
                    <IconButton label="Remove from rotation" onClick={() => setRotation({ ...rotation, people: rotation.people.filter((x) => x !== pid) })}><X size={12} /></IconButton>
                  </li>
                ))}
                {draft.members.filter((m) => !rotation.people.includes(m)).map((m) => (
                  <li key={m}>
                    <Button onClick={() => setRotation({ ...rotation, people: [...rotation.people, m] })}>
                      <Plus size={12} aria-hidden="true" /> {people.find((p) => p.id === m)?.name ?? m}
                    </Button>
                  </li>
                ))}
              </ol>
            </div>
          </div>
        )}
      </section>
      <section>
        <h3 className="mb-1 font-semibold">Team preferences</h3>
        <p className="console-muted mb-2 text-xs">Used for members who set none of their own.</p>
        <PreferencesEditor value={draft.preferences ?? []} onChange={(p) => setDraft({ ...draft, preferences: p })}
          timezone={draft.onCall?.timezone} fallback={defaults} />
      </section>
    </Sheet>
  );
}
