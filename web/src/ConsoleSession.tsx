import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { api, sessionExpiredEvent, type DemoSession, type SessionUser } from "./api";
import { LoginPage } from "./pages/LoginPage";
import { navigate, useLocation } from "./router";
import { Button, Notice } from "./ui";

interface SessionContext {
  user: SessionUser | null;
  signOut: () => Promise<void>;
}

const Context = createContext<SessionContext | null>(null);
const sessionChangedKey = "kardan.demo-sso.changed";

function broadcastSessionChange() {
  try {
    localStorage.setItem(sessionChangedKey, `${Date.now()}-${Math.random()}`);
  } catch {
    // The current tab still works when browser storage is unavailable.
  }
}

export function useConsoleSession() {
  return useContext(Context);
}

export function canManageAlert(user: SessionUser | null | undefined, team: string) {
  return !user || user.teams.includes(team);
}

export function ConsoleSession({ children }: { children: ReactNode }) {
  const { path } = useLocation();
  const [session, setSession] = useState<DemoSession | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const returnTo = useRef("/");
  const signedOut = session?.enabled && !session.user;

  useEffect(() => {
    let cancelled = false;
    api.session().then((current) => {
      if (!cancelled) setSession(current);
    }).catch((err: Error) => {
      if (!cancelled) setError(err.message);
    });
    return () => { cancelled = true; };
  }, [attempt]);

  useEffect(() => {
    const expire = () => setSession((current) => current?.enabled ? { enabled: true, user: null } : current);
    const syncSession = (event: StorageEvent) => {
      if (event.key !== sessionChangedKey) return;
      setSession(null);
      setError("");
      setAttempt((n) => n + 1);
    };
    window.addEventListener(sessionExpiredEvent, expire);
    window.addEventListener("storage", syncSession);
    return () => {
      window.removeEventListener(sessionExpiredEvent, expire);
      window.removeEventListener("storage", syncSession);
    };
  }, []);

  useEffect(() => {
    if (!session) return;
    if (signedOut && path !== "/login") {
      if (path.startsWith("/") && !path.startsWith("//")) returnTo.current = path + window.location.search;
      navigate("/login", true);
    } else if (!signedOut && path === "/login") {
      navigate(returnTo.current, true);
    }
  }, [path, session, signedOut]);

  if (error) {
    return (
      <main className="mx-auto max-w-lg space-y-4 p-6">
        <Notice tone="error">Could not check your session. {error}</Notice>
        <Button onClick={() => { setError(""); setAttempt((n) => n + 1); }}>Retry</Button>
      </main>
    );
  }
  if (!session) return <main role="status" className="console-muted p-6">Checking your session…</main>;
  if (signedOut) return <LoginPage onSignIn={(current) => {
    broadcastSessionChange();
    setSession(current);
  }} />;
  if (path === "/login") return null;

  return (
    <Context.Provider value={{
      user: session.user,
      signOut: async () => {
        await api.signOut();
        broadcastSessionChange();
        setSession({ enabled: true, user: null });
      },
    }}>
      {children}
    </Context.Provider>
  );
}
