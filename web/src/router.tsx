import { useEffect, useState, type ComponentPropsWithoutRef, type MouseEvent } from "react";

// A tiny pushState router: the app has a handful of pages and needs no more.

const EVENT = "kardan:navigate";

export function navigate(
  to: string,
  replace = false,
  { preserveScroll = false, state = null }: { preserveScroll?: boolean; state?: unknown } = {},
) {
  if (replace) window.history.replaceState(state, "", to);
  else window.history.pushState(state, "", to);
  window.dispatchEvent(new Event(EVENT));
  if (!preserveScroll) window.scrollTo({ top: 0 });
}

export function useLocation() {
  const read = () => ({ path: window.location.pathname, query: new URLSearchParams(window.location.search) });
  const [loc, setLoc] = useState(read);
  useEffect(() => {
    const on = () => setLoc(read());
    window.addEventListener("popstate", on);
    window.addEventListener(EVENT, on);
    return () => {
      window.removeEventListener("popstate", on);
      window.removeEventListener(EVENT, on);
    };
  }, []);
  return loc;
}

export function Link({
  to,
  preserveScroll = false,
  ...props
}: Omit<ComponentPropsWithoutRef<"a">, "href"> & { to: string; preserveScroll?: boolean }) {
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    props.onClick?.(e);
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
    e.preventDefault();
    navigate(to, false, { preserveScroll });
  };
  return <a {...props} href={to} onClick={onClick} />;
}
