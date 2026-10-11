# Maruvo website

Single-page React + JavaScript landing page for the Maruvo CLI. Dark, responsive layout with a geometric M logo, an original TUI screenshot, concise product explanation, animated SVG collaboration workflow, Solana escrow explanation, platform downloads, and source setup instructions. Mobile screens use the compact TUI screenshot; clicking either image opens the full original. No login, signup, or task-management routes; actual task work runs in the CLI. Public crawler rules are at `/robots.txt`, and agent guidance is at `/agents.txt` with a link to the full CLI skill.

From the repository root:

```sh
make web
make build-web
pnpm -C apps/web lint
```

Use Node >=22.22.0 and pnpm 10.25.0. `make web` installs the frozen lockfile and starts Vite; `make build-web` writes assets to `apps/web/dist`. CI builds the site only after a PR merges into main.

The terminal screenshot shows a real task awaiting review. The Linux AMD64 download is a static CLI binary at `public/downloads/maruvo-linux-amd64`; the page also links source setup. macOS and Windows downloads remain unavailable. Rebuild this binary when CLI behavior changes, then rebuild the site to copy it into `dist`. Local assets do not establish that the public site has been updated.
