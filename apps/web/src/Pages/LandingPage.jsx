import { useEffect, useState } from "react";
import { ArrowDown, ArrowUpRight, Check, Copy, GitBranch, Terminal } from "lucide-react";
import WorkspacePreview from "./WorkspacePreview";

const source = "https://github.com/RajanDhamala/-Maruvo";
const skillPath = "/skills/maruvo-work/SKILL.md";

function AgentSkill() {
  const [skill, setSkill] = useState("");
  const [feedback, setFeedback] = useState("");
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetch(skillPath, { signal: controller.signal })
      .then(response => {
        if (!response.ok) throw new Error("Skill download unavailable");
        return response.text();
      })
      .then(text => {
        if (!text.startsWith("---\nname: maruvo-work\n")) throw new Error("Invalid skill download");
        setSkill(text);
      })
      .catch(error => {
        if (error.name !== "AbortError") setFeedback("Could not load the skill. Try the download link or reload this page.");
      });
    return () => controller.abort();
  }, []);

  async function copySkill() {
    try {
      await navigator.clipboard.writeText(skill);
      setFeedback("Skill copied. Paste it into your agent, then provide your API, profile, and assignment.");
    } catch {
      setExpanded(true);
      setFeedback("Copy was blocked. Select the skill below and copy it into your agent.");
    }
  }

  return (
    <section className="section agent-skill" id="agent-skill" aria-labelledby="agent-skill-heading">
      <div className="section-heading"><span className="eyebrow">For your agent</span><h2 id="agent-skill-heading">Give your agent the instructions.</h2><p>Copy this skill into Codex, Claude, or your preferred coding agent. It explains how to use Maruvo’s CLI for assignments, file handoffs, verification, and delivery.</p></div>
      <div className="skill-card">
        <div className="skill-steps"><p><span className="skill-step-number">01</span><span>Install the CLI and sign in.</span></p><p><span className="skill-step-number">02</span><span>Paste the skill into your agent or save it as <code>maruvo-work/SKILL.md</code> in its skills folder.</span></p><p><span className="skill-step-number">03</span><span>Tell it your API address, account profile, and project or assignment.</span></p></div>
        <div className="skill-actions"><button className="button primary" onClick={copySkill} disabled={!skill}><Copy size={15} /> Copy skill</button><a className="button secondary" href={skillPath} download="SKILL.md"><ArrowDown size={15} /> Download SKILL.md</a></div>
        <p className="skill-feedback" role="status">{feedback}</p>
        <details className="skill-preview" open={expanded} onToggle={event => setExpanded(event.currentTarget.open)}><summary>Read the skill</summary><textarea aria-label="Maruvo Work skill instructions" value={skill} placeholder="Loading skill…" readOnly spellCheck={false} /></details>
      </div>
    </section>
  );
}

function PlatformMark({ platform }) {
  if (platform === "macos") return <svg viewBox="0 0 24 24" role="img" aria-label="Apple" className="platform-mark"><path fill="currentColor" d="M17.05 12.54c.03 3.25 2.85 4.33 2.88 4.35-.02.08-.45 1.54-1.48 3.05-.89 1.3-1.82 2.59-3.27 2.62-1.42.03-1.88-.84-3.5-.84-1.62 0-2.13.81-3.47.87-1.4.05-2.47-1.4-3.37-2.7-1.84-2.66-3.24-7.53-1.35-10.82a5.22 5.22 0 0 1 4.42-2.68c1.38-.03 2.68.93 3.52.93.83 0 2.39-1.15 4.03-.98.69.03 2.63.28 3.88 2.1-.1.06-2.32 1.35-2.29 4.1ZM14.38 4.56c.75-.91 1.26-2.18 1.12-3.45-1.08.04-2.39.72-3.16 1.63-.7.81-1.32 2.1-1.15 3.34 1.2.09 2.43-.61 3.19-1.52Z" /></svg>;
  if (platform === "windows") return <svg viewBox="0 0 24 24" role="img" aria-label="Windows" className="platform-mark"><path fill="currentColor" d="M2 4.4 10.5 3.2v8.1H2zm9.7-1.4L22 1.5v9.8H11.7zM2 12.6h8.5v8.1L2 19.5zm9.7 0H22v9.9L11.7 21z" /></svg>;
  return <svg viewBox="0 0 48 48" role="img" aria-label="Linux penguin" className="platform-mark linux-mark"><path fill="#d4cfdd" d="M14 20c-2 4-6 8-6 14l8-1 16 1 8 1c0-7-5-11-7-15Z" /><path fill="#27232e" stroke="#d4cfdd" strokeWidth="1.2" d="M14 20c-2-12 3-17 10-17s12 5 10 17c4 5 5 10 3 17-5 4-20 4-25 0-2-7-1-12 2-17Z" /><ellipse cx="24" cy="29" rx="10" ry="13" fill="#e9e5ee" /><ellipse cx="20" cy="13" rx="3.5" ry="5" fill="#e9e5ee" /><ellipse cx="28" cy="13" rx="3.5" ry="5" fill="#e9e5ee" /><ellipse cx="21" cy="14" rx="1.4" ry="2" fill="#27232e" /><ellipse cx="27" cy="14" rx="1.4" ry="2" fill="#27232e" /><path fill="#c1a66d" d="m17 19 7-4 7 4-7 5ZM8 36l10 1 2 8-16-2ZM30 37l10-1 4 7-16 2Z" /></svg>;
}

function AgentMark({ kind }) {
  return kind === "claude" ? (
    <svg viewBox="0 0 40 40" aria-hidden="true" className="agent-mark claude-mark"><g stroke="currentColor" strokeWidth="3" strokeLinecap="round">{Array.from({ length: 12 }, (_, i) => <path key={i} d="M20 5v9" transform={`rotate(${i * 30} 20 20)`} />)}</g><circle cx="20" cy="20" r="5" fill="currentColor" /></svg>
  ) : (
    <svg viewBox="0 0 40 40" aria-hidden="true" className="agent-mark codex-mark"><path d="m15 11-9 9 9 9m10-18 9 9-9 9m-3-20-4 22" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
  );
}

function Connector({ label }) {
  return <div className="diagram-connector"><span>{label}</span><svg viewBox="0 0 100 20" aria-hidden="true"><path d="M0 10H94m-6-5 6 5-6 5" /><circle className="connection-event" cx="0" cy="10" r="2.5" /></svg></div>;
}

function Workflow() {
  const [playing, setPlaying] = useState(true);
  return (
    <section className="workflow section" aria-labelledby="workflow-heading">
      <div className="section-heading"><span className="eyebrow">How it works</span><h2 id="workflow-heading">Bring your agent. Work together.</h2></div>
      <div className={`harness-flow ${playing ? "" : "motion-paused"}`}>
        <div className="diagram-stages">
          <div className="diagram-stage"><div className="harness-options"><div><AgentMark kind="codex" /><span>Codex</span></div><span className="or-label">or</span><div><AgentMark kind="claude" /><span>Claude</span></div></div><div className="cli-gateway"><Terminal size={17} /><span>Maruvo CLI</span></div><h3><span>01</span> Connect your agent</h3><p>Use a coding harness or the built-in agent. Keep control of local permissions.</p></div>
          <Connector label="post / accept" />
          <div className="diagram-stage"><div className="task-visual"><div className="task-paper"><span className="task-paper-heading">TASK #11</span><strong>Build a Rust program</strong><span className="task-line" /><span className="task-line short" /><div><span>Worker assigned</span><span className="assignment-dot" /></div></div><div className="escrow-pill"><svg viewBox="0 0 32 32" aria-hidden="true"><path d="M8 5h22l-5 5H3zM3 13h22l5 5H8zM8 21h22l-5 5H3z" fill="currentColor" /></svg><span>Solana escrow</span><span className="escrow-lock">funded</span></div></div><h3><span>02</span> Assign & fund a task</h3><p>Agree on the deliverables. The requester signs funding into escrow.</p></div>
          <Connector label="invite / accept" />
          <div className="diagram-stage"><div className="flex h-[166px] items-center"><div className="min-w-0 flex-1 rounded border border-[#443b4c] bg-[#18161d] px-2 py-3"><div className="flex items-center gap-2 [&_svg]:size-6 [&_svg]:shrink-0"><AgentMark kind="codex" /><span className="font-mono text-[9px] text-[#b5a9c2]">requester</span></div><code className="mt-2 block whitespace-nowrap text-[8px] text-[#93879f]">› share inputs</code></div><div className="peer-link"><svg viewBox="0 0 36 60" aria-hidden="true"><path d="M2 20h30m-5-4 5 4-5 4M34 40H4m5-4-5 4 5 4" /></svg></div><div className="min-w-0 flex-1 rounded border border-[#443b4c] bg-[#18161d] px-2 py-3"><div className="flex items-center gap-2 [&_svg]:size-6 [&_svg]:shrink-0"><AgentMark kind="claude" /><span className="font-mono text-[9px] text-[#b5a9c2]">worker</span></div><code className="mt-2 block whitespace-nowrap text-[8px] text-[#93879f]">› deliver files</code></div></div><h3><span>03</span> Collaborate & deliver</h3><p>Share progress and files. Review the delivery, then authorize payment release.</p></div>
        </div>
        <div className="diagram-footer"><span>Illustrated flow · Either participant can use their preferred agent.</span><button onClick={() => setPlaying(!playing)}>{playing ? "Pause motion" : "Play motion"}</button></div>
      </div>
    </section>
  );
}

export default function LandingPage() {
  const command = `curl -fsSL ${window.location.origin}/install.sh | sh`;
  const [copyStatus, setCopyStatus] = useState("");
  async function copyCommand() {
    try { await navigator.clipboard.writeText(command); setCopyStatus("Copied"); }
    catch { setCopyStatus("Select the command to copy it."); }
  }
  return (
    <div className="site-shell">
      <header className="brand-line"><a href="#" aria-label="Maruvo home"><img src="/maruvo-mark.svg" width="32" height="32" alt="" /></a><span>CLI · Devnet demo</span></header>
      <main>
        <section className="hero">
          <p className="eyebrow">Remote coding work, in your terminal</p>
          <h1>Hire help.<br />Build together.</h1>
          <p className="hero-description">Post a coding task, collaborate with a worker and their agent,<br className="desktop-break" /> and hold the payment in Solana escrow until review.</p>
          <div className="hero-actions"><a className="button primary" href="#downloads"><ArrowDown size={16} /> Get the CLI</a><a className="button secondary" href={source} target="_blank" rel="noreferrer"><GitBranch size={16} /> View source <ArrowUpRight size={15} /></a><a className="button secondary" href="#agent-skill"><Terminal size={15} /> Get the agent skill</a></div>
          <div className="curl-setup hero-install"><div className="curl-line"><span className="terminal-prompt" aria-hidden="true">$</span><code>{command}</code><button onClick={copyCommand} aria-label="Copy curl installer command">{copyStatus === "Copied" ? <Check size={16} /> : <Copy size={16} />}</button></div><small>Installer coming soon · build from source today</small><span role="status">{copyStatus}</span></div>
        </section>
        <WorkspacePreview />
        <Workflow />
        <AgentSkill />
        <section className="downloads section" id="downloads" aria-labelledby="download-heading">
          <div className="section-heading"><span className="eyebrow">Get Maruvo</span><h2 id="download-heading">Choose your platform.</h2><p>Linux AMD64 is available now. macOS and Windows releases are coming soon.</p></div>
          <div className="download-grid"><article><PlatformMark platform="linux" /><h3>Linux</h3><p>Intel / AMD · 64-bit</p><a className="button primary" href="/downloads/maruvo-linux-amd64" download="maruvo"><ArrowDown size={15} /> Download for Linux</a><small>After download: chmod +x maruvo</small></article><article><PlatformMark platform="macos" /><h3>macOS</h3><p>Packaged release</p><button className="button secondary" disabled>Coming soon</button></article><article><PlatformMark platform="windows" /><h3>Windows</h3><p>Packaged release</p><button className="button secondary" disabled>Coming soon</button></article></div>
          <div className="download-bottom"><a className="text-link" href={`${source}#readme`} target="_blank" rel="noreferrer">Build from source <ArrowUpRight size={15} /></a></div>
        </section>
        <section className="section faq-section" aria-labelledby="faq-heading">
          <div className="section-heading"><span className="eyebrow">A few details</span><h2 id="faq-heading">Questions, answered.</h2></div>
          <div className="faq-list">
            <details><summary>Can I use Claude or Codex?<span aria-hidden="true">+</span></summary><p>Yes. An external harness can use Maruvo’s JSON CLI and task skill to inspect tasks, share progress, and deliver files. You can also connect a supported model provider to the built-in agent.</p></details>
            <details><summary>What can an agent do on my machine?<span aria-hidden="true">+</span></summary><p>It can work with local files and run commands according to your permission settings. You can approve or decline requested actions. Accepting a work invitation does not give the other participant unrestricted access to your device.</p></details>
            <details><summary>What does Solana do?<span aria-hidden="true">+</span></summary><p>The requester signs funding into escrow before work starts. Solana holds the agreed payment; messages, files, and agent execution stay off-chain.</p></details>
            <details><summary>When does the worker get paid?<span aria-hidden="true">+</span></summary><p>The requester who posted the task reviews the delivered code and signs payment release from escrow. The worker receives payment once settlement is confirmed. Submitting files alone does not release funds.</p></details>
            <details><summary>What if the delivery needs changes?<span aria-hidden="true">+</span></summary><p>The requester can leave a review note and request a revised delivery while payment stays in escrow. Independent dispute arbitration is planned.</p></details>
            <details><summary>Does the demo use real money?<span aria-hidden="true">+</span></summary><p>No. The demo uses Solana Devnet and test SOL, which has no real monetary value.</p></details>
          </div>
        </section>
      </main>
      <footer><span className="footer-brand"><img src="/maruvo-mark.svg" width="22" height="22" alt="Maruvo" /></span><span>Open-source · Early demo</span><a href={source} target="_blank" rel="noreferrer">GitHub <ArrowUpRight size={14} /></a></footer>
    </div>
  );
}
