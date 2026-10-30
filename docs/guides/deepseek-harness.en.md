# Setting up DeepSeek Harness by hand (not officially supported yet)

[简体中文](deepseek-harness.md)

[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (command `dsh`) is DeepSeek's open-source coding agent harness. **It is a developer preview (this guide was written and tested against 0.2.1-rc.2), and Cohive does not officially support it yet**: `cohive init` does not generate its configuration. Everyone does the steps below once on their own computer, and they may need adjusting after a dsh upgrade.

Why `cohive init` cannot write it into the repository like it does for Claude Code, Codex and OpenCode: dsh keeps its MCP and plugin configuration only in your home directory (`~/.dsh/`) and does not read configuration from the repository. As a result, **one dsh profile connects to one Cohive workspace**. If you switch between projects in different workspaces, create one profile per workspace, or change the workspace name in the configuration.

## Before you start

- **Node 22.19 or later on the 22 line, or Node 24 or later.** Node 23 is not supported: `dsh` exits right away without printing anything.
- `pnpm` on your computer (`dsh plugin` uses it to install plugins).
- `cohive` installed and this computer paired; see [Installing the cohive CLI](install-cli.en.md) and [Onboarding](onboarding.en.md). Run this once more to issue a token for dsh:

  ```bash
  cohive login --tools dsh
  ```

The examples use the `web` profile (the one `dsh web` starts). Replace `https://cohive.example.com` and `pay` with your server URL and workspace name, and `/Users/you` with your home directory.

## Step 1: connect Cohive's MCP server

Add the following to `~/.dsh/profiles/web/cordis.patch.yml`. The file is created the first time you use the profile: a few comment lines followed by a single `[]` line (an empty list). **Replace the `[]` line with the snippet below**; otherwise the file is not valid YAML and dsh fails to start. If the file already has other entries, append the snippet to the end and keep what is there; if you have never used the profile and the file does not exist, create it:

```yaml
- insert:
    - id: mcp-cohive
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: cohive
        transport: stdio
        command: cohive
        args: [mcp-proxy, --server, https://cohive.example.com, --workspace, pay, --tool, dsh]
```

- In dsh the tools are named like `mcp__cohive__cohive_inbox`.
- If dsh cannot find `cohive`, set `command` to its full path (`which cohive` shows it).
- The configuration contains no token: `cohive mcp-proxy` reads it from the local credentials file.

## Step 2: confirmations and in-session reminders

**Without this step, dsh runs every Cohive tool without asking, including submit, accept and close, which change other people's work.** dsh has no setting that marks a tool name as "ask every time", so confirmations go through DeepSeek's "Claude Code hooks" compatibility plugin. In-session reminders use the same plugin.

1. Install the plugin. Use the same version as `dsh --version`; the package's latest tag points to an old release:

   ```bash
   dsh plugin --profile web add -w @deepseek-ai/dsh-hooks-claude-code@0.2.1-rc.2
   ```

   It warns that the package "declares no dsh.bundle"; you can ignore that.

2. Create the hooks file `~/.dsh/cohive-hooks.json`:

   ```json
   {
     "hooks": {
       "SessionStart": [{"hooks": [{"type": "command", "command": "cohive hook --server https://cohive.example.com --workspace pay --tool dsh", "timeout": 5}]}],
       "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "cohive hook --server https://cohive.example.com --workspace pay --tool dsh", "timeout": 5}]}],
       "PreToolUse": [{
         "matcher": "mcp__cohive__cohive_(submit_issue|accept_issue|close_issue|cancel_issue|delete_draft)",
         "hooks": [{"type": "command", "command": "echo '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"ask\",\"permissionDecisionReason\":\"Cohive: this affects other team members, please confirm\"}}'"}]
       }]
     }
   }
   ```

3. Add one more item to the `insert` list from step 1. Use an absolute path: a relative path is resolved from the directory you start dsh in.

   ```yaml
       - id: cohive-hooks
         name: '@deepseek-ai/dsh-hooks-claude-code'
         config:
           configPath: /Users/you/.dsh/cohive-hooks.json
   ```

4. Restart `dsh web`. Restart again whenever you change the hooks file: it is read only once at startup.

### Which tools need confirmation

| Confirm every time (`PreToolUse` returns ask) | Run directly |
|---|---|
| `cohive_submit_issue` submit | `cohive_inbox`, `cohive_get_issue`, `cohive_search` |
| `cohive_accept_issue` accept, decline, transfer | `cohive_who_owns`, `cohive_find_member` |
| `cohive_close_issue` close or reopen after verifying | `cohive_draft_issue`, `cohive_update_issue` |
| `cohive_cancel_issue` cancel | `cohive_request_verification` |
| `cohive_delete_draft` delete a draft | `cohive_watch_issue` |

The list matches what `cohive init` configures for Claude Code, Codex and OpenCode.

### What it looks like (tested with 0.2.1-rc.2)

- **Web UI**: an "awaiting approval" bar appears above the input with only "Reject" and "Allow once"; there is no "always allow".
- **Full access mode** (`danger-full-access`): tools that need confirmation are rejected, not let through.
- **Headless mode** (`dsh headless`): there is nobody to ask, so they are rejected as well.
- **In-session reminders**: they reach the model's context, but the web chat does not show them as a separate line. Following the `/cohive` skill, the agent tells you on a new line at the end of its final reply, for example "Cohive reminder: #12 assigned to you by tester-a. Use /cohive 12 to view".

## Step 3: the `/cohive` skill

dsh reads `.agents/skills/` and `.dsh/skills/` in the repository, **not** `.claude/skills/`.

- A project where `cohive init --tools codex` or `--tools opencode` has run already has `.agents/skills/cohive/SKILL.md`.
- For other projects, copy it from [examples/opencode/.agents/skills/cohive](../../examples/opencode/.agents/skills/cohive).

Restart dsh after adding a new skill directory; it then shows up in the list when you type `/`. Use `/cohive` or `/cohive 12`.

## Privacy: turn off session log upload

When dsh uses DeepSeek's official model API (including through a gateway you configure), **it uploads the session log to DeepSeek with later requests by default**. Cohive issue content reaches the session through tool results: titles, repro steps, logs and evidence. We recommend turning the upload off, in either of two ways:

- In the web UI, open **Settings → General** and turn off uploading the Session Log when using the official model API.
- Add this to `~/.dsh/cordis.patch.yml` (applies to every profile; again, replace a lone `[]` line):

  ```yaml
  - id: session-log-deepseek
    config:
      enabled: false
  ```

Separately, dsh only reports telemetry when you submit feedback; to turn that off, set the environment variable `DSH_TELEMETRY_DISABLED=1`.

## Troubleshooting

- **No reminders in the session**: run `cohive hook --check --server https://cohive.example.com --workspace pay --tool dsh`.
- **No `mcp__cohive__*` tools**: if dsh fails to connect to an MCP server at startup, it starts anyway without those tools. First run the `cohive mcp-proxy …` command from the `args` in step 1 directly in a terminal and check that it starts.
- Evidence and more detail are in the research report [2026-10-19-coding-tools-opencode-deepseek.md](../research/2026-10-19-coding-tools-opencode-deepseek.md) (in Chinese).
