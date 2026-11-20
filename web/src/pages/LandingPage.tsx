import {
  Activity, BellRing, Check, CircleAlert, Code2, Copy, Database, GitBranch, Layers3, Monitor,
  Radio, Route, Send, ShieldCheck, Terminal, Webhook,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "../router";
import alertsImage from "../assets/landing-alerts.webp";
import mobileAlertsImage from "../assets/landing-mobile.webp";
import rulesImage from "../assets/landing-rule.webp";
import deliveriesImage from "../assets/landing-deliveries.webp";
import "./landing.css";

const repository = "https://github.com/undeluro/kardan-alerts";

const previews = [
  {
    title: "Alert queue",
    image: alertsImage,
    height: 714,
    alt: "Kardan alert queue showing a settlement timeout, a payments error, and a rejected trade",
    caption: "See affected teams, severity, and occurrence counts in one live queue.",
  },
  {
    title: "Rule preview",
    image: rulesImage,
    height: 850,
    alt: "Kardan rule editor with a workflow-failure sample and an alert preview",
    caption: "Test a rule against a sample payload before turning it on.",
  },
  {
    title: "Delivery health",
    image: deliveriesImage,
    height: 620,
    alt: "Kardan Deliveries screen showing the health and retry counts for email, SMS, and phone",
    caption: "See which channels are healthy and which deliveries are waiting for a retry.",
  },
] as const;

const capabilities = [
  {
    icon: Webhook,
    title: "Connect the systems you already run",
    description: "Receive HTTP webhooks, consume Kafka topics, and tail log files. Rules can reference fields in JSON, FIX, XML, and other parsed formats.",
  },
  {
    icon: GitBranch,
    title: "Adapt rules as events change",
    description: "Filter on payload fields, combine conditions, and configure recipients and alert templates. Preview a sample event and save changes from the UI.",
  },
  {
    icon: Route,
    title: "Reach the person on call",
    description: "Route by team, severity, schedule, and channel preference. Retries and fallback routing help when a provider is unavailable.",
  },
  {
    icon: Monitor,
    title: "Work an alert in a live queue",
    description: "See events arrive in real time, review the source payload and delivery attempts, and acknowledge or resolve the alert.",
  },
  {
    icon: Layers3,
    title: "Keep repeat failures together",
    description: "Use a rule-defined deduplication key to collect repeated events in one open alert with an occurrence count.",
  },
  {
    icon: ShieldCheck,
    title: "Scale the workers independently",
    description: "Run processors and delivery workers independently. Postgres stores alerts and queued deliveries together, and failed messages stay available for review.",
  },
] as const;

const flow = [
  { icon: Database, title: "Ingest", text: "The settlement engine emits a workflow failure to Kafka." },
  { icon: GitBranch, title: "Match", text: "A rule matches a production timeout and groups repeats by step and error code." },
  { icon: Send, title: "Notify", text: "The on-call team gets the alert and can track delivery and resolve it from the queue." },
] as const;

export default function LandingPage() {
  const [preview, setPreview] = useState(0);
  const [copyStatus, setCopyStatus] = useState<"idle" | "copied" | "manual">("idle");
  const active = previews[preview];

  useEffect(() => {
    const previous = document.title;
    document.title = "Kardan · Operational event alerting";
    return () => { document.title = previous; };
  }, []);

  const copyCommand = async () => {
    try {
      await navigator.clipboard.writeText("make demo");
      setCopyStatus("copied");
    } catch {
      setCopyStatus("manual");
    }
  };

  return (
    <div className="landing">
      <a href="#landing-main" className="skip-link">Skip to content</a>
      <header className="landing-header">
        <div className="landing-width landing-header-inner">
          <a href="/landing" className="landing-wordmark"><Radio size={21} aria-hidden="true" /> Kardan</a>
          <nav aria-label="Landing navigation" className="landing-nav">
            <a href="#workflow">Workflow</a>
            <a href="#coverage">Requirements</a>
            <a href="#quickstart">Quick start</a>
          </nav>
          <Link to="/" className="landing-button landing-secondary landing-header-cta">Open console</Link>
        </div>
      </header>

      <main id="landing-main" tabIndex={-1}>
        <section className="landing-hero">
          <div className="landing-width landing-hero-grid">
            <div className="landing-intro">
              <h1>From operational events<br className="landing-hero-break" /> to <span>actionable alerts.</span></h1>
              <p>Kardan takes events from webhooks, Kafka, and logs, groups repeated failures, and gets the alert to the team that can act.</p>
              <div className="landing-actions">
                <Link to="/" className="landing-button landing-primary">Open demo</Link>
                <a href={repository} className="landing-button landing-secondary"><Code2 size={17} aria-hidden="true" /> View source</a>
              </div>
            </div>
            <div className="landing-example">
              <div className="landing-example-heading">
                <Activity size={17} aria-hidden="true" />
                <h2>Settlement timeout</h2>
                <span>Example</span>
              </div>
              <ol className="landing-trace">
                <li><span className="landing-trace-glyph"><Database size={16} aria-hidden="true" /></span><div><b>Event received</b><code>workflow.events</code></div></li>
                <li><span className="landing-trace-glyph"><GitBranch size={16} aria-hidden="true" /></span><div><b>Rule matched</b><code>prod · order-settlement · TIMEOUT</code></div></li>
                <li><span className="landing-trace-glyph"><BellRing size={16} aria-hidden="true" /></span><div><b>Alert routed</b><code>Settlements on-call · SMS + phone</code></div></li>
              </ol>
            </div>
          </div>
        </section>

        <section className="landing-width landing-product" aria-labelledby="product-title">
          <div className="landing-product-head">
            <div>
              <h2 id="product-title">See the console in action</h2>
              <p aria-live="polite">{active.caption}</p>
            </div>
            <div className="landing-preview-buttons" role="group" aria-label="Product previews">
              {previews.map((item, index) => (
                <button key={item.title} type="button" aria-pressed={index === preview} onClick={() => setPreview(index)}>
                  {item.title}
                </button>
              ))}
            </div>
          </div>
          <figure className="landing-product-frame">
            <picture>
              {preview === 0 && <source media="(max-width: 540px)" srcSet={mobileAlertsImage} width={390} height={844} />}
              <img src={active.image} alt={active.alt} width={1440} height={active.height} fetchPriority={preview === 0 ? "high" : "auto"} />
            </picture>
            <figcaption>Product screenshots with sample data</figcaption>
          </figure>
        </section>

        <section id="workflow" className="landing-width landing-section" aria-labelledby="workflow-title">
          <div className="landing-section-heading">
            <h2 id="workflow-title">A settlement failure, end to end.</h2>
            <p>Follow one incident from a source event to the team responsible for it.</p>
          </div>
          <ol className="landing-flow">
            {flow.map(({ icon: Icon, title, text }) => (
              <li key={title}>
                <Icon size={20} aria-hidden="true" />
                <h3>{title}</h3>
                <p>{text}</p>
              </li>
            ))}
          </ol>
        </section>

        <section id="coverage" className="landing-width landing-section" aria-labelledby="coverage-title">
          <div className="landing-section-heading">
            <h2 id="coverage-title">What the demo covers.</h2>
            <p>The hackathon requirements, mapped to what you can try in the console.</p>
          </div>
          <div className="landing-coverage">
            {capabilities.map(({ icon: Icon, title, description }) => (
              <article key={title}>
                <Icon size={20} aria-hidden="true" />
                <h3>{title}</h3>
                <p>{description}</p>
              </article>
            ))}
          </div>
          <div className="landing-scope">
            <CircleAlert size={19} aria-hidden="true" />
            <div>
              <h3>Demo scope</h3>
              <p>Email is captured by Mailpit. SMS and phone use a simulator with failure injection. Runbook execution, throttling, and formal load-test results are on the <a href={`${repository}#roadmap`}>project roadmap</a>.</p>
            </div>
          </div>
        </section>

        <section id="quickstart" className="landing-width landing-section landing-start" aria-labelledby="start-title">
          <div>
            <h2 id="start-title">Try it on your machine.</h2>
            <p>Clone the repository, start Docker, and run the demo command from the project root. It starts the stack and sends sample events.</p>
            <Link to="/" className="landing-button landing-primary">Open console</Link>
          </div>
          <div>
            <div className="landing-command">
              <Terminal size={18} aria-hidden="true" />
              <code>make demo</code>
              <button type="button" onClick={() => void copyCommand()} aria-label="Copy demo command">
                {copyStatus === "copied" ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
                {copyStatus === "copied" ? "Copied" : "Copy"}
              </button>
            </div>
            <p role="status" className="landing-copy-status">
              {copyStatus === "copied" ? "Demo command copied." : copyStatus === "manual" ? "Select the command above to copy it." : ""}
            </p>
          </div>
        </section>
      </main>

      <footer className="landing-footer">
        <div className="landing-width">
          <a href="/landing" className="landing-wordmark"><Radio size={18} aria-hidden="true" /> Kardan</a>
          <a href={repository}><Code2 size={16} aria-hidden="true" /> GitHub</a>
        </div>
      </footer>
    </div>
  );
}
