import { useEffect, useState, useSyncExternalStore } from "react";

// Business time = browser time + the server's demo offset (and any skew
// between the two clocks). App keeps it fresh from GET /api/clock.
let offsetMs = 0;
const subscribers = new Set<() => void>();

export function setBusinessClock(serverNowIso: string) {
  const next = Date.parse(serverNowIso) - Date.now();
  // Ignore jitter from request latency; react to real shifts.
  if (Math.abs(next - offsetMs) < 2_000) return;
  offsetMs = next;
  subscribers.forEach((fn) => fn());
}

const subscribe = (fn: () => void) => {
  subscribers.add(fn);
  return () => subscribers.delete(fn);
};

/** Business "now" in ms, re-rendering every `everyMs` and when the demo clock shifts. */
export function useBusinessNow(everyMs = 15_000): number {
  const offset = useSyncExternalStore(subscribe, () => offsetMs);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), everyMs);
    return () => window.clearInterval(t);
  }, [everyMs]);
  return now + offset;
}

/** "45 s", "12 min", "2 h 5 min", "3 d 4 h" */
export function shortDuration(ms: number): string {
  const s = Math.round(Math.abs(ms) / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}

export type Urgency = "missed" | "critical" | "soon" | "later";

export function cutoffUrgency(cutoffAt: string, bizNow: number): { left: number; level: Urgency } {
  const left = Date.parse(cutoffAt) - bizNow;
  const level: Urgency = left <= 0 ? "missed" : left < 30 * 60_000 ? "critical" : left < 2 * 3600_000 ? "soon" : "later";
  return { left, level };
}

/** "Fedwire customer transfers (FEDWIRE)" -> ["Fedwire customer transfers", "FEDWIRE"] */
export function splitCutoffName(name: string): [string, string] {
  const m = /^(.*) \(([^)]+)\)$/.exec(name);
  return m ? [m[1], m[2]] : [name, ""];
}
