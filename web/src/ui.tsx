import { ChevronDown, LockKeyhole, Mail, MessageSquare, Monitor, Phone, Radio } from "lucide-react";
import type { ComponentPropsWithoutRef, ReactNode } from "react";

const channelIcons = new Map([
  ["email", Mail], ["sms", MessageSquare], ["phone", Phone], ["ui", Monitor],
]);
const channelLabels = new Map([
  ["email", "Email"], ["sms", "SMS"], ["phone", "Phone"], ["ui", "In-app"],
]);

export function channelLabel(channel: string) {
  return channelLabels.get(channel) ?? channel;
}

export function ChannelIcon({ channel, size = 16, className }: {
  channel: string;
  size?: number;
  className?: string;
}) {
  const Icon = channelIcons.get(channel) ?? Radio;
  return <Icon size={size} className={className} aria-hidden="true" />;
}

export function Select({ className = "", ...props }: ComponentPropsWithoutRef<"select">) {
  return (
    <span className={`console-select ${className}`}>
      <select {...props} className="console-select-input" />
      <ChevronDown size={15} className="console-select-chevron" aria-hidden="true" />
    </span>
  );
}

// Full class strings so Tailwind can see them at build time.
export const tone: Record<string, { stripe: string; badge: string; dot: string }> = {
  red: {
    stripe: "bg-red-500",
    badge: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30",
    dot: "bg-red-500",
  },
  orange: {
    stripe: "bg-orange-500",
    badge: "bg-orange-50 text-orange-700 ring-orange-600/20 dark:bg-orange-500/10 dark:text-orange-300 dark:ring-orange-400/30",
    dot: "bg-orange-500",
  },
  amber: {
    stripe: "bg-amber-500",
    badge: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
    dot: "bg-amber-500",
  },
  blue: {
    stripe: "bg-blue-500",
    badge: "bg-blue-50 text-blue-700 ring-blue-600/20 dark:bg-blue-500/10 dark:text-blue-300 dark:ring-blue-400/30",
    dot: "bg-blue-500",
  },
  green: {
    stripe: "bg-green-500",
    badge: "bg-green-50 text-green-700 ring-green-600/20 dark:bg-green-500/10 dark:text-green-300 dark:ring-green-400/30",
    dot: "bg-green-500",
  },
  gray: {
    stripe: "bg-gray-400",
    badge: "bg-gray-50 text-gray-700 ring-gray-600/20 dark:bg-gray-500/10 dark:text-gray-300 dark:ring-gray-400/30",
    dot: "bg-gray-400",
  },
};

export const toneFor = (color: string) => tone[color] ?? tone.gray;

export function Badge({ className = "", children }: { className?: string; children: ReactNode }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium ring-1 ring-inset ${className}`}>
      {children}
    </span>
  );
}

export const alertStatusLabel = { open: "Open", acked: "Acknowledged", resolved: "Resolved" } as const;

export const statusBadge: Record<string, string> = {
  open: "bg-white text-gray-900 ring-gray-300 dark:bg-gray-900 dark:text-gray-100 dark:ring-gray-600",
  acked: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  resolved: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  sent: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  pending: "bg-gray-50 text-gray-700 ring-gray-500/20 dark:bg-gray-500/10 dark:text-gray-300 dark:ring-gray-400/30",
  sending: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30",
  retry: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/30",
  dead: "bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-400/30",
  standby: "bg-gray-50 text-gray-500 ring-gray-300 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700",
  cancelled: "bg-gray-50 text-gray-400 ring-gray-200 line-through dark:bg-gray-900 dark:text-gray-500 dark:ring-gray-800",
};

export function Button({
  onClick,
  children,
  variant = "secondary",
  disabled,
}: {
  onClick: () => void;
  children: ReactNode;
  variant?: "primary" | "secondary";
  disabled?: boolean;
}) {
  const styles =
    variant === "primary"
      ? "console-button-primary"
      : "console-button-secondary";
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={`inline-flex min-h-8 items-center justify-center gap-2 rounded-sm px-3 py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${styles}`}
    >
      {children}
    </button>
  );
}

export function timeAgo(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h ago`;
  return `${Math.round(h / 24)}d ago`;
}

export const formatCount = (n: number) => (n >= 10_000 ? `${Math.round(n / 1000)}k` : n.toLocaleString());

// Finance field kinds detected in schemas.
const financeTone: Record<string, string> = {
  isin: "bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30",
  instrument: "bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-400/30",
  lei: "bg-indigo-50 text-indigo-700 ring-indigo-600/20 dark:bg-indigo-500/10 dark:text-indigo-300 dark:ring-indigo-400/30",
  counterparty: "bg-indigo-50 text-indigo-700 ring-indigo-600/20 dark:bg-indigo-500/10 dark:text-indigo-300 dark:ring-indigo-400/30",
  amount: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  quantity: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  price: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/30",
  currency: "bg-teal-50 text-teal-700 ring-teal-600/20 dark:bg-teal-500/10 dark:text-teal-300 dark:ring-teal-400/30",
  iban: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/30",
  account: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/30",
  partyName: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/30",
  contact: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/30",
};

export function FinanceChip({ kind, sensitive }: { kind: string; sensitive?: boolean }) {
  return (
    <Badge className={financeTone[kind] ?? "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/30"}>
      {sensitive && <span role="img" aria-label="Sensitive field" title="Sensitive: masked on SMS and email"><LockKeyhole size={12} aria-hidden="true" /></span>}
      {kind}
    </Badge>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 max-w-3xl text-sm console-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`console-panel ${className}`}>{children}</div>;
}

export function Notice({ tone: t = "info", children }: { tone?: "info" | "warn" | "error"; children: ReactNode }) {
  const cls = {
    info: "bg-sky-50 text-sky-800 ring-sky-200 dark:bg-sky-500/10 dark:text-sky-200 dark:ring-sky-500/30",
    warn: "bg-amber-50 text-amber-900 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-200 dark:ring-amber-500/30",
    error: "bg-red-50 text-red-800 ring-red-200 dark:bg-red-500/10 dark:text-red-200 dark:ring-red-500/30",
  }[t];
  return <div className={`rounded-md p-3 text-sm ring-1 ${cls}`}>{children}</div>;
}

export const inputClass =
  "console-control w-full";
