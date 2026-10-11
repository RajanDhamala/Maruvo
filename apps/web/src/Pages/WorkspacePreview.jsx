export default function WorkspacePreview() {
  return (
    <section className="product-preview section" aria-labelledby="preview-heading">
      <div className="section-caption">
        <h2 id="preview-heading">Inside the Maruvo TUI.</h2>
        <a className="preview-real-link" href="/screenshots/task-delivery.png" target="_blank" rel="noreferrer">Open full-size image ↗</a>
      </div>
      <figure>
        <a className="tui-preview-image" href="/screenshots/task-delivery.png" target="_blank" rel="noreferrer" aria-label="Open the Maruvo TUI screenshot at full size">
          <picture>
            <source media="(max-width: 560px)" srcSet="/screenshots/task-delivery-mobile.png" width="892" height="567" />
            <img
              src="/screenshots/task-delivery.png"
              width="1919"
              height="1079"
              alt="Maruvo TUI showing a submitted Rust and RISC-V task, delivered files, worker messages, and review controls."
              decoding="async"
            />
          </picture>
        </a>
        <figcaption>
          <span>A task delivery in the Maruvo terminal.</span>
          <span>Solana Devnet · test SOL</span>
        </figcaption>
      </figure>
    </section>
  );
}
