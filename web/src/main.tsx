import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import { useLocation } from "./router";

const Console = lazy(() => import("./App"));
const Landing = lazy(() => import("./pages/LandingPage"));

function Root() {
  const { path } = useLocation();
  return (
    <Suspense fallback={null}>
      {path === "/landing" || path === "/landing/" ? <Landing /> : <Console />}
    </Suspense>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
);
