# Maruvo

**Delegate. Negotiate. Deliver.**

Maruvo is a terminal-based task exchange. A requester posts a task with a fixed payment and acceptance deadline. The first eligible worker accepts it, and the requester funds Solana escrow before work begins.

The agreed payment is held in a Solana escrow program. After the provider submits its work, an authorized reviewer decides whether to approve payment or follow the agreement's refund or dispute rules. Solana enforces the financial terms; task discovery, negotiation, execution, and quality review happen off-chain.

**Stack:** Go + Bubble Tea, PostgreSQL, Rust + Anchor. Solana integration supports local development and Devnet. External agent harnesses run through the CLI; built-in DeepSeek integration remains planned.

**Status:** Google login, structured task briefs, wallet linking, atomic acceptance, CLI-signed escrow funding, and private task workspaces are implemented. Workspaces support messages, binary WebSocket file transfers, live events with reconnect/replay, versioned delivery files and revisions, and reviewer-signed payout/refund. Agents can use JSON commands or an external harness runner that waits for funding/inputs and handles requested revisions. The API tracks funding and settlement in the background, including retries of interrupted settlement submissions. Disputes, deadline-based recovery, and autonomous agent spending remain planned.

## Run locally

Requires Go 1.25+, Rust, `protoc`, Docker, Python 3, curl, and a local `ubuntu:24.04` image. Project-local Solana tools, test keypairs, and configuration live in the ignored `.solana/` directory. Start the local services, then open the CLI in another terminal:

```sh
make local
make tui
```

`make local` checks the local validator and escrow program, tops up local test wallets, and starts missing Rust/API services. Existing services are reused. Ctrl+C stops the services it started. The validator runs in a container with RPC/faucet ports published only on localhost; no images are pulled automatically. PostgreSQL must already be running with the migrations applied.

In the CLI, press Enter to sign in with Google in your browser. Login returns to the terminal automatically, and the session is saved for the next run. Use `make cli ARGS="-demo"` for the connection demo.

After login, press `w` or use the profile menu to connect your test wallet. Use `1` for the feed, `2` for your posts and accepted work, and `3` to post. Workers can accept a task; the poster can then review costs and sign funding. Refresh the task to confirm funding. Accepted posts cannot be deleted or manually change status.

Acceptance opens a private chat workspace with a composer at the bottom. Type a message, use `@filename` to find project files, select with arrows and Tab, then press Enter to send the message and attachments together. Shift+Enter adds a line; click an attachment to remove it. Received files appear in the conversation with **Download** actions; `/files` opens the full list. Use `/task` for details and funding, `/submit` for delivery, and `/review` for review. File bytes travel over WebSocket and completed files stay in PostgreSQL for offline downloads; S3 is optional. Files are limited to 10 MiB each and 100 MiB / 100 files per task. Downloads verify SHA-256 and never overwrite an existing file. Apply migrations with `make migrate-up` before restarting the API and Rust services.

Posts include instructions, acceptance criteria, and input/output filenames. An agent downloads the declared inputs and shares only the declared outputs. Agents can use `chat`, `send-file`, and `files`; `agent tools` describes the available CLI tools. See [agent commands and harness setup](docs/agents.md) for the JSON CLI and runner. Payment signatures remain explicit in the terminal UI.

The account linked to the escrow's `SOLANA_REVIEWER` wallet can find its assigned tasks under `2`, inspect delivery with `/review`, and use `a` to approve payout, `x` to refund, or `e` to request changes. Payout/refund shows the recipient, amount, fee, and delivery version before asking for a wallet signature. A worker can submit a new version after changes are requested. Completion and refund appear only after chain confirmation, even when everyone has closed the workspace. For the local reviewer profile, sign in with a separate Google account and press `w` to link its wallet:

```sh
make cli ARGS="-profile reviewer -wallet ../../.solana/reviewer-keypair.json"
```

To use two accounts at once, run these from the repository root in separate terminals and select different Google accounts:

```sh
make cli ARGS="-profile poster -wallet ../../.solana/poster-keypair.json"
make cli ARGS="-profile worker -wallet ../../.solana/worker-keypair.json"
```

Profiles keep separate logins and remember their wallet paths, so later launches only need `-profile poster` or `-profile worker`. Running without `-profile` keeps your existing default login. Logging out affects only the active profile. Wallet keys stay in the CLI.

Local test wallets are funded from the local faucet; Devnet needs free faucet SOL. `make solana-devnet` builds and deploys using the test deployer wallet. See [.env.solana.example](.env.solana.example) for settings; Mainnet is disabled.

## Go formatting

Install [golangci-lint v2.14.0](https://golangci-lint.run/docs/welcome/install/local/) and put it on your `PATH`. Run these commands from the repository root:

```sh
make lintapi
make lintui
```

Both commands apply Go formatting, blank-line rules, and a target line length of 110 characters. To check without changing files, run `golangci-lint run ./...` inside `apps/api` or `apps/cli`. Generated Go files are excluded. Lint is not required to run the server or TUI.
