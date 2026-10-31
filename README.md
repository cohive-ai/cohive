<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/cohive-lockup-reverse.svg">
    <img src="docs/brand/cohive-lockup.svg" alt="Cohive" width="320">
  </picture>
</h1>

[中文](README.zh-CN.md)

**A collaboration layer that lets the coding agents of everyone on a small team hand issues to each other, with people making the calls.**

Team members write code with their own tools, such as Claude Code, Codex and OpenCode. Cohive lets these agents hand off issues to each other over remote MCP: whoever finds a problem drafts an issue, the owner accepts it, fixes it and asks for verification from inside their own coding tool, and the reporter confirms and closes it. Submitting, accepting and closing are confirmed by a person. The team chat only gets a signal ("whose turn it is, and what to do") with an @-mention. Teams host Cohive themselves.

Cohive speaks English and Simplified Chinese: the web UI, CLI, MCP tool descriptions, session reminders and chat notifications. Each person picks a language in their Profile, each workspace picks the language of its group messages, and the server default is set with `COHIVE_DEFAULT_LANGUAGE`. More languages are welcome; see [Languages and translations](CONTRIBUTING.md#languages-and-translations).

![Board](docs/images/board.en.png)

## The problem

When several people work across several repositories with different coding tools, their agents cannot talk to each other. People copy context around, retell it and chase progress by hand:

- context gets lost in the retelling, and the other agent investigates from scratch;
- nobody knows who should take an issue, or whether anyone picked it up;
- after a fix, nobody tells the reporter to verify, so the loop never closes;
- the two sides are not talking about the same commit.

When one person juggles several sessions, the coding tools already cover that. Cohive is about collaboration **between different people using different tools**.

## How it works

**The issue loop.** Say A writes the frontend with Claude Code and B owns the order service with Codex:

1. While integrating, A's agent finds a broken endpoint, collects evidence, writes a repro script, checks for duplicates and drafts an issue. A reviews it and confirms sending it.
2. A notice appears in the workspace's chat group and @-mentions B.
3. B types `$cohive 12` in Codex. The agent pulls the full issue, summarizes it and gives a first read (duplicate? mine? what's missing?). B confirms accepting it, fixes it in their own session, then asks for verification.
4. A is notified, reruns the repro script, and confirms closing it or reopens it.

Everything is visible on the board, and every step records which person's which tool did it.

![Issue detail](docs/images/issue.en.png)

**The chat knocks, the client opens the door.** Chat messages contain only the issue number, the service, who handed it to whom, what to do next, and a title and one-line brief that a person has confirmed. Repro scripts, logs and evidence never go to the chat; they are read through authenticated MCP in the coding tool or on the board. Telegram is supported today.

**People confirm.** `cohive init` configures the tools that change someone else's work (submit, accept, close) as "ask the user every time", so the coding tool's own permission prompt (Claude Code, Codex, OpenCode) stops them, rather than relying on a "please ask the user first" line in a prompt. Incoming issues and comments are treated as data, never as instructions.

**In-session reminders.** When something new concerns you (handed to you, sent back, waiting for your verification), the agent tells you at the end of its reply the next time you send a message.

## Quick start

### For the person deploying it: Docker, about 5 minutes

You need a machine with Docker.

```bash
git clone https://github.com/cohive-ai/cohive.git
cd cohive/deploy
cp .env.example .env      # set POSTGRES_PASSWORD (letters and digits; e.g. openssl rand -hex 16)
docker compose up -d --build
docker compose exec cohive cat /data/initial_admin_password
```

The last command shows the random password of the initial admin `admin`. It is written only to this file in the data volume, never to the logs. Open `http://localhost:8787`, sign in and change the password when asked; the file is then deleted. Next, set the external URL under Server admin → Settings and create an invite link under the workspace's Admin → Invites.

Chat notifications, Cloudflare Tunnel, backups and upgrades: see [Deploying with Docker](docs/guides/deploy-docker.en.md).

To change the board's colors to your own, see [Custom themes](docs/guides/theming.en.md).

### For team members: three steps

1. **Open the invite link and register.**
2. **Install the CLI and pair your computer.** Copy the command from Profile → Pair a new computer. After installing it opens the browser; click Authorize.

   ```bash
   curl -fsSL https://cohive.example.com/install.sh | sh -s -- --login
   ```

   On Windows, in PowerShell: `$env:COHIVE_LOGIN=1; irm https://cohive.example.com/install.ps1 | iex`. The CLI is served by your own Cohive server; no Go toolchain needed.
3. **Run `cohive init` in the project directory** (once per computer and repository; every worktree of the repository follows automatically). Then use `/cohive` in Claude Code and OpenCode, and `$cohive` in Codex.

The first time you open the project in Claude Code or Codex, trust the project. For Codex, also run the `codex` CLI once in the project directory and trust Cohive's two hooks. OpenCode needs no trust step. Details: [Onboarding](docs/guides/onboarding.en.md).

## Concepts

| Concept | Meaning |
|---|---|
| Workspace | One per team or product area, with one chat group. Accounts are global; a person can belong to several workspaces with a role in each |
| Issue | Draft → open → accepted → fixing → ready for verification → closed; it can be declined, transferred or reopened. Its state lives only in Cohive's database |
| Device pairing | Run `cohive login` in a terminal and click "Authorize" in the browser. Pair a computer once and it works for all your workspaces; from the second computer on, an admin approves it |
| Tokens | Issued per person + tool + workspace and kept only in a local credentials file. The config `cohive init` writes (user-level, or into the repository with `--project`) holds no secrets |
| Admin actions | Only allowed from a web session signed in with a password; coding-tool tokens cannot do them. Every admin action goes into an append-only log |

## How it differs from nearby products

| Product | Core model | Difference |
|---|---|---|
| Multica | Assign issues to agents that run in the background | Cohive does not execute anything on its own; both sides are a person with their own agent, and people confirm |
| Claude Code cross-session messaging, agent teams | One person's sessions working together | Not across people or tools |
| GitHub Agent HQ, GitLab Duo, Linear agents | Assign issues to cloud agents | The hub is an issue on a hosted platform, not the session a developer is working in |

What Cohive does not do: claim work or change code on anyone's behalf, or run agents unattended in the background. The server never calls a language model. There is no hosted service.

## Status and roadmap

Phase one; the author's team is trying it out. Supported coding tools: Claude Code, Codex and OpenCode (DeepSeek Harness can be set up by hand following [this guide](docs/guides/deepseek-harness.en.md); it is not officially supported yet). Working today: the issue loop with human confirmation, the board, Telegram notifications and in-chat approvals, web accounts and invites, device pairing, multiple workspaces, an admin audit log, one-line CLI install, secret scanning before writes, in-session reminders and Docker deployment.

Next, ordered by trial feedback:

- routing by `CODEOWNERS`, reminders for stalled issues, code-area claims;
- more chat platforms (DingTalk first);
- phase two: API change broadcasts, GitHub / GitLab merge request and pipeline integration;
- phase three: shared team conventions, statistics, GitHub / GitLab sign-in for cloud deployments.

The full plan (Chinese) is in [docs/plan.md](docs/plan.md).

## Building from source

You need Go 1.26, Node 20+, pnpm and Docker (for the test Postgres).

```bash
make db-up      # local development Postgres on localhost:55432
make build      # web UI, CLI packages for five platforms, and the server, in bin/
go test ./...
```

## Contributing

Bug reports, fixes, docs and translations are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) to get started, and follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report security problems privately as described in [SECURITY.md](SECURITY.md).

<a href="https://github.com/cohive-ai/cohive/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=cohive-ai/cohive" alt="Contributors" />
</a>

## License

[Apache License 2.0](LICENSE). By anormlylee.
