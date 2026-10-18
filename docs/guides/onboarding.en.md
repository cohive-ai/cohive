# Onboarding new members

[中文](onboarding.md)

One project has one chat group, which maps to one Cohive workspace. Accounts are global: a person has one username, one password and one Telegram connection, and can belong to several workspaces. Roles (admin or member) are set per workspace. You pair a computer once, and it works for all the workspaces you belong to.

Members, devices, invites and approvals are all managed in the web UI. A new member opens an invite link and registers, then runs `cohive login` in a terminal and clicks Authorize (授权) in the browser to pair the computer. No keys are ever copied by hand. Chat tools (Telegram, DingTalk) are only attached on top, for @-mentions and approval buttons in the group; they are not required for registration or sign-in.

The web UI is available in English and Simplified Chinese. Where this guide names a button or menu, the English label comes first and the Chinese label follows in parentheses the first time it appears.

## Roles

| Role | What they can do |
|---|---|
| Server admin | Create and delete workspaces and assign each workspace's admins; deactivate and reactivate accounts; grant server admin rights; manage any workspace (but cannot see a workspace's issues unless they are a member of it; they can add themselves on the admin page) |
| Workspace admin | In their workspace: add people, send invites, approve requests, remove members (including transferring their work), change roles, edit group settings and display name; approve new devices of members of their workspace |
| Member | The board, and sending and receiving issues through their coding tool. Who can submit, accept or close an issue still depends on the issue's reporter and owner, not on the role |

## After deployment: the first admin

On first start, if there is no server admin yet, the server creates an account `admin`. It is a server admin and also the admin of the `default` workspace. Its random password is written to a file in the data directory (by default `data/initial_admin_password` under the working directory; set the data directory with the environment variable `COHIVE_DATA_DIR`), which can be read only on the server. The startup log mentions where the file is but never prints the password.

With a Docker deployment the file is in the data volume: run `docker compose exec cohive cat /data/initial_admin_password` to read it. The server commands below also run inside the container, for example `docker compose exec cohive cohive-server admin reset-password --name admin` (see [Deploy with Docker](deploy-docker.en.md)).

1. Read the file on the server and sign in to the web UI with the username and password in it.
2. You must change the password on first sign-in. The file is deleted once you do.
3. If you forget the password, run this on the server (no data is erased):

   ```bash
   ./bin/cohive-server admin reset-password --name admin
   ```

   The new password is shown once in the terminal. Change it right after signing in.

4. **Set the external URL**: under Server admin → Settings (服务器管理 → 设置), confirm the address your teammates use (for example `https://cohive.example.com`) and save. The first time you open the page, it is prefilled with the address you are currently using, but it is not saved automatically. Make sure it is not `localhost` or an address only you can open. Links in group messages and the host name used when coding tools connect through a tunnel both come from this setting. You can also set and lock it with the environment variable `COHIVE_EXTERNAL_URL`.

## Server admin: create a workspace

In the top bar, go to Server admin → Workspaces (服务器管理 → 工作空间) and enter a name (lowercase letters, digits, underscores and hyphens; it cannot be changed later because project configuration refers to it by name). Optionally enter the username of the first admin (default: yourself). Then open the new workspace's Admin → Workspace (管理 → 工作空间) page and enter the group ID: add the bot to the project chat group and send `/chatid` there to get it. **A group can be bound to only one workspace.**

A workspace with no issues at all (drafts included) can be deleted, for the case where you mistyped the name.

## Workspace admin: add people

Admin actions can only be done in the web UI while signed in with username and password. A web session opened without a password through `cohive web`, and the tokens of coding tools, cannot perform admin actions, so someone who gets hold of a computer does not also gain admin rights. Approval buttons in the group are not affected: they are checked against the Telegram account of whoever clicks, and only apply to the workspace that the group belongs to.

**People who already have an account** (they used Cohive in another workspace): go to Admin → Members → Add existing account (管理 → 成员 → 添加已有账号), enter their username (it must be typed exactly; you cannot browse all accounts), check the display name, pick a role and add them. It takes effect immediately. If someone who was removed earlier is added back, their previous issues are reattached.

**New people**: go to Admin → Invites (管理 → 邀请), generate an invite and send them the link:

| Invite | Uses | After a new person registers | When someone with an account opens it |
|---|---|---|---|
| Personal invite | One | Takes effect immediately; can be set to admin | They click "Join as X" (以 X 身份加入) and it takes effect immediately |
| Team invite | Configurable (default 20) | An admin must approve them under Admin → Pending approval (管理 → 待审) | A join request is created, which also needs approval |

- The link is shown only once, when you generate it. It is valid for 7 days by default (you can choose from 1 hour to 30 days) and can be revoked at any time.
- Usernames are unique across the whole server: a new person cannot register a username that an existing account, or a request waiting for approval in another workspace, already uses.
- Once the group is set up, pending registrations and join requests are posted to the workspace's group, and device requests are posted to every group of the requester's workspaces. Admins just click Approve / Reject (批准 / 拒绝). When one is handled anywhere, the messages in all groups change to show the result.

## Members: three steps

**1. Open the invite link**: a new person enters a username, display name and password (at least 10 characters) to register. With a team invite, you can sign in only after an admin approves you. Someone who already has an account signs in first and then clicks "Join as X".

**2. Install the CLI and pair your computer** (once per computer). You do not need Go or access to the code repository: the CLI is served by your team's own Cohive server, and its version matches the server (see [install-cli.en.md](install-cli.en.md)). In the web UI, Profile → Pair a new computer (个人中心 → 绑定新电脑) shows the command with your server address filled in, ready to copy. After installing, it continues straight into pairing:

```bash
curl -fsSL https://cohive.example.com/install.sh | sh -s -- --login
```

On Windows, run `$env:COHIVE_LOGIN=1; irm https://cohive.example.com/install.ps1 | iex` in PowerShell. It installs to `~/.local/bin` by default. If it tells you the directory is not on your PATH, add it as instructed, and restart your coding tool from a terminal (it must be able to find `cohive` on its PATH). Developers working in the repository can also run `make install-cli`. On a computer that already has the CLI, just run `cohive login --server https://cohive.example.com`.

During pairing, the terminal opens the authorization page in your browser (it also shows the link and a QR code). Sign in to the web UI with your username and password, check the device name, the tool, and the workspaces under "Once approved, usable for" (批准后可用于), then click Authorize (授权). The terminal then reports that you are signed in and lists the tools each workspace issued to you.

- **The first computer takes effect as soon as you authorize it. After that, adding another computer needs approval from an admin of any workspace you belong to** (once approved, the computer works for all your workspaces). The terminal waits while you wait. You can press Ctrl+C or close the laptop at any time: progress is saved on this computer, and after approval you run `cohive login --resume` to pick it up (valid for 24 hours).
- If the repository already has the config generated by `cohive init`, the server address and the tool to sign in for are read from it (this works from subdirectories too). The first time you sign in to an address, the terminal shows the address and asks you to press Enter to confirm.
- The device key and tokens are stored in `~/.config/cohive/credentials.json`, readable only by you. Run `cohive status` to check (keys and tokens are not displayed).

**3. Run `cohive init` in the project directory, and trust the hooks the first time you open the project** (skip `cohive init` if a teammate already did it and committed the config). Then use `/cohive` in Claude Code and `$cohive` in Codex. If you belong to several workspaces, specify which one this project belongs to:

```bash
cohive init --workspace pay
```

The first time you open the project in a coding tool, trust Cohive's hooks (needed for in-session reminders):

- **Claude Code**: the first time you open this folder, choose "trust this folder" (it is the same step that enables the MCP server).
- **Codex**: run the `codex` CLI once in the project directory and agree to trust the project. At startup it says "Hooks need review"; choose "Trust all and continue" to trust Cohive's two hooks (`SessionStart` and `UserPromptSubmit`). **Desktop-only users must do this once too**: the desktop app has no place to trust hooks, but once you trust them in the CLI the desktop app picks them up. **Trust once per computer and per clone directory.** If you change the server address or workspace (by running `cohive init` again with different values), you need to trust again; simply re-running `cohive init` does not.

## In-session reminders

Both tools, in CLI and desktop versions, support this (verified with the CLI and desktop versions of both Claude Code and Codex; where the trust prompt appears may differ slightly between versions). Claude Code requires trusting the folder; Codex requires running the `codex` CLI once in the project directory and choosing to trust everything.

When something new involves you (an issue is assigned to you, declined, sent to you for verification, reopened, needs a person to step in, an issue you submitted is accepted, or an issue you fixed is closed), **the agent tells you at the end of its final reply to your next message, on a separate line after the main text**. For example: "Cohive reminder: #12 from tester-a was assigned to you. Use /cohive 12 to view it." When you start a new session, the first reply ends by telling you how many issues are waiting for you. The reminder goes at the end because replies are often long, and a note at the top is easy to skip. A reminder contains only the issue number, the action and who; it carries no title or body. To see the content, use `/cohive 12` (`$cohive 12` in Codex). Each reminder is shown only once per session.

- **No reminders while the coding tool is idle or before a session starts**: it can only speak up when you send a message. In those cases, rely on @-mentions in the Telegram group.
- Claude Code also shows a separate "Cohive reminder" line in its interface. The Codex desktop app does not; it relies on the agent mentioning it in the reply.
- If the Cohive server is unreachable or slow, or your token is invalid, there is no reminder, and your message is neither slowed down nor blocked (it waits at most 2 seconds, and checks at most once per 20 seconds per session).
- You are not reminded about your own actions. Each reminder is shown once per session; if several sessions are open, each one shows it once.
- If reminders don't show up, run the `cohive hook --check …` command printed at the end of `cohive init`.
- To turn them off: `cohive init --no-hooks` (it removes only Cohive's hooks and leaves your own alone).
- If a teammate still runs an old `cohive`, the hook reports "unknown command". This does not affect usage; upgrading `cohive` fixes it.
- Inside a git worktree, Codex reads the hook configuration from the main checkout directory, so commit the files generated by `cohive init`.

## After joining another workspace

You do not need to pair again. After cloning the new project (its config is already in the repository), just open the coding tool: the local forwarder notices there is no token for this workspace yet and issues one automatically using this computer's device key. You can also run `cohive init --workspace <name>` in the repository, or run `cohive login` again to fill in tokens for all your workspaces.

## Profile

- **My workspaces**: the workspaces you belong to, with your role in each. Click one to switch; there is also a switcher dropdown in the top bar.
- **Open the web UI without a password**: on a paired computer, run `cohive web` and the browser signs you in directly. This session can use Profile and the board, but cannot perform admin actions, accept invites or authorize new devices; sign in with your username and password for those.
- **My devices**: view and revoke your own computers (listed with their tokens per workspace). After you revoke one, its tokens and device key stop working immediately.
- **Connect Telegram**: click "Connect Telegram" and open the bot in Telegram to finish. It works for all your workspaces. Once connected, group notifications can @-mention you, and admins can use the approval buttons in the group. One Telegram account can be connected to only one Cohive account.
- **Connect DingTalk** (when the server has DingTalk enabled): click "Connect DingTalk", send the code shown on the page to the bot in a **direct chat**, then confirm in the web UI that it is you. See section 9 of [DingTalk integration](dingtalk.md) (Chinese). One DingTalk account can be connected to only one Cohive account.
- **Change password**: after you change it, your sign-ins in other browsers are invalidated.

## Removing and deactivating

- **Remove** (workspace admin; affects one workspace): Admin → Members → Remove (管理 → 成员 → 移出) lists the person's unfinished issues (as owner and as reporter) and the services they own. You must pick a new person for each item before removing them. Reassignments are written to the issue timeline and the new person gets a group notification. After removal, their tokens in this workspace stop working immediately; other workspaces are unaffected.
- **Deactivate** (server admin; affects the whole person): revokes all their devices and tokens, signs them out of every web session, disconnects Telegram, and they can no longer sign in. Deactivating does not require a hand-over. The admin page marks the account as "Deactivated" with the number of issues awaiting action, and you can "reassign" them separately. Reactivating does not restore devices: they must run `cohive login` again and be approved by an admin.

## Admin audit log

All admin actions are recorded in an append-only log (it cannot be edited or deleted), written in the same transaction as the action itself: if the action fails, no record is left.

- **Where to look**: a workspace's Admin → Audit log (管理 → 记录) shows only that workspace; Server admin → Audit log (服务器管理 → 记录) shows everything (filterable by workspace, including deleted ones). Both can be filtered by type, actor and entry point, are sorted newest first, and show 50 entries per page. On the server you can also run `cohive-server audit list` (same filters; use `--before` to page).
- **What is recorded**: approvals (registration requests, join requests and new devices; approvals and rejections, whether done in the web UI or with group buttons), role changes, invite creation and revocation, device revocation (including revoking your own devices, and the old device revoked by `cohive login --force`), direct joins, transfers and removals (including who the work was handed to), deactivation and reactivation, granting and revoking server admin, creating and deleting workspaces and changing their settings, password resets from the command line, profile changes, issuing and revoking tokens, and registering a service owner. Also recorded are self-service actions that change who can get in and with what: authorizing your first device, registering or joining with a personal invite, connecting and disconnecting Telegram, changing your password, and creating the initial admin on first start. Signing in and out are not recorded.
- **Rejected admin requests are recorded too**: when an authenticated identity calls an admin endpoint (workspace admin, server admin, device authorization, accepting an invite, viewing the log) and is refused, a "rejected admin request" entry is written. This covers coding tool tokens, device keys and `cohive web` passwordless sessions being blocked (admin actions only accept a web session signed in with username and password), or someone who signed in with a password but lacks permission; it also covers someone without approval rights clicking an approval button in a group. What is recorded is the endpoint template (for example `POST /api/v1/invites/{code}/accept`), not actual values such as the invite code in the path. Unauthenticated requests, and requests to workspaces you are not in (which return "not found"), are not recorded. For the same identity (account plus credential type) and the same endpoint, at most one entry is written per minute. The entry point of such records may be "coding tool token".
- **What each entry contains**: the actor (empty for server command line actions, shown as "server command line"), the entry point (web UI / group button / server command line / member's local CLI / automatic by the server), the workspace (empty for server-level actions), the target, and any necessary extra information (for example the values before and after a role change, or who each issue was handed to on removal), and the time. **Passwords, tokens, invite codes and device keys are never recorded.**
- **What workspace admins cannot see**: server-level actions such as deactivating an account or approving a device on the server admin page appear only on the server admin page.
- **History from before the feature**: on upgrade, registrations, joins and device approvals whose approver was already stored in the database are backfilled as entries, with the entry point marked "unknown (before this feature)". The database has no decision time for device approvals, so approvals use the device's creation time and rejections use the request time, both marked "time is approximate". Other historical admin actions have no actor or time in the database and cannot be backfilled.
- **How tamper-resistant it is**: the program has no code path that edits or deletes entries, and database triggers block updates and deletes. Someone who can change the database schema directly can still bypass this; this layer protects against program bugs and mistakes.

## Security notes

- The device name on the authorization page is reported by the terminal and allows only host name characters. **Authorize only the request you just started yourself in your terminal.**
- The terminal only opens the Cohive server's own authorization page. If the server returns any other address, it fails with an error.
- A device authorization is valid for 15 minutes. The device key and tokens are delivered once, and only to the computer that started the authorization.
- A device key can only be used to issue tokens for your workspaces, sign in with `cohive web` without a password, and revoke your own devices. It cannot call the issue or admin APIs directly.
- Web sign-in uses a same-site-only cookie, and cross-site requests are always rejected. Changing or resetting a password invalidates other sign-ins.

## Files written by cohive init

| Tool | Files |
|---|---|
| Claude Code | `.mcp.json` (the `cohive` entry), `.claude/settings.json` (confirmation rules; under `hooks`, one `cohive hook …` group each for session start and prompt submit), `.claude/skills/cohive/SKILL.md` |
| Codex | `.codex/config.toml` (the section between `# >>> cohive` and `# <<< cohive`, including the two hooks), `.agents/skills/cohive/SKILL.md` |

- It changes only the parts it manages and keeps your existing config. Running it again gives the same result.
- These files **contain no tokens**: the coding tool connects to the Cohive server through the local command `cohive mcp-proxy --server … --workspace … --tool …`, which reads the token from the local credentials file. They are safe to commit and share with your team.
- The first time you open the project in a coding tool, it asks whether to trust the project and whether to enable the `cohive` MCP server. Agree to both. For Codex, also run the `codex` CLI once in the project directory to trust Cohive's two hooks (see step 3 above).
- `enabledMcpjsonServers` in `.claude/settings.json` is shared through the repository, so pay attention to changes to it and to `.mcp.json` when reviewing pull requests.
- `.mcp.json` and `.codex/config.toml` contain the server address and workspace name; keep that in mind for public repositories.

## Alternative: pairing without a browser

For automation and environments without a browser, an admin issues a token with `cohive-server token issue --workspace <name> --member <username> --tool <tool>` and passes it to the person through a trusted channel. The person feeds it in through standard input, so it does not end up in shell history:

```bash
pbpaste | cohive login --server https://cohive.example.com --token-stdin
```

## Troubleshooting

- **The terminal says authorization was denied**: you clicked Deny in the browser, or an admin did not approve it. Run `cohive login` again.
- **The coding tool says you are not a member of workspace X**: ask an admin of that workspace to add you, then reopen the coding tool.
- **The coding tool says the Cohive token is invalid or revoked**: if you were removed from this workspace, contact an admin. Otherwise run `cohive login --force` to pair again. Pairing again counts as adding a new device and needs admin approval; once it succeeds, this computer's previous device is revoked automatically.
- **"This computer was paired with an older version"**: computers paired with an earlier version (before device keys were introduced) have no device key. Their old tokens still work, but automatic issuing for new workspaces and `cohive web` require `cohive login --force` to pair again first (admin approval needed).
- **Lost computer**: revoke it under Profile → My devices (个人中心 → 我的设备), or ask an admin to revoke it under Admin → Devices (管理 → 设备).
- **No Cohive reminders in a session**: run `cohive hook --check --server … --workspace … --tool …` (the full command is printed at the end of `cohive init`). For Codex, first make sure you ran the `codex` CLI in the project directory and chose "Trust all and continue".
- **Connecting Telegram says the bot is not enabled**: the server has no `COHIVE_TELEGRAM_BOT_TOKEN` set.
