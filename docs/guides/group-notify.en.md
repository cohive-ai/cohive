# Group notifications and public access

[中文](group-notify.md)

Cohive's group notifications work like "knock in the group, open the door in the client". When an issue is waiting for someone, the workspace's chat group gets a short message that carries only a signal and @-mentions that person. The person who is mentioned then runs `/cohive 12` (Claude Code) or `$cohive 12` (Codex) in their own coding tool to fetch the full content, triage it and decide. Repro scripts, evidence and logs are never posted to the group.

This guide uses Telegram as the example. If you use DingTalk, read [Integrate Cohive with your own DingTalk organization](dingtalk.md) (Chinese): a DingTalk group is bound with a group binding code, members connect by sending a code in a direct chat and confirming in the web UI, and approval buttons come back to the server over a Stream long connection, so only outbound access is needed. Both chat tools can be enabled on the same server; in this first release each workspace binds to one group.

## 1. Prepare the bot

1. In Telegram, create a bot with @BotFather and get its bot token (it looks like `123456789:AAH…`; both sides of the colon are part of the token).
2. On the machine that runs the Cohive server, save the token. The input is not echoed and does not enter your command history:

   ```bash
   bash scripts/set-telegram-token.sh
   ```

   The token is written to `~/.config/cohive/telegram.env` (readable only by you). Run the server with the start script, which loads the token only inside the server process:

   ```bash
   scripts/run-server.sh
   ```

   Do not `source` this file in an interactive terminal: every program you start from that terminal afterwards (AI coding tools included) could then read the bot token.

   With a Docker deployment, put the token in `COHIVE_TELEGRAM_BOT_TOKEN` in `deploy/.env` and run `docker compose up -d` (see [Deploy with Docker](deploy-docker.en.md)).

3. Add the bot to the chat group that belongs to the workspace. The bot only needs ordinary member status. If you make it an admin, it receives every message in the group (Cohive discards non-command messages immediately and stores nothing).

## 2. Configure the workspace

One project has one group, which maps to one workspace; **a group can be bound to only one workspace** (when someone clicks an approval button in the group, the workspace is looked up from the group). A server admin creates workspaces in the web UI under Server admin → Workspaces (服务器管理 → 工作空间), or by running this on the machine that runs the server:

```bash
./bin/cohive-server workspace add --name payments --admin <username of the first admin>
```

After the server is running, send `/chatid` in the group and the bot replies with the group ID. A workspace admin enters the group ID under Admin → Workspace (管理 → 工作空间), chooses whether group messages show the title and brief, and saves. You can also configure it on the server with a command:

```bash
./bin/cohive-server workspace notify --workspace payments --telegram-chat -1001234567890
```

**Links in group messages** are built from the server-wide external URL as `<external URL>/w/<path>/issues/<number>` (for example `https://cohive.example.com/w/payments/issues/12`). There is only one external URL for the whole server. A server admin sets it under Server admin → Settings (服务器管理 → 设置), or by running `./bin/cohive-server server set --external-url https://cohive.example.com` on the server. You can also lock it with the environment variable `COHIVE_EXTERNAL_URL`; once locked, neither the web UI nor the command line can change it. If no external URL is set, group messages carry no link and everything else works as usual. The workspace settings page shows the resulting link read-only. The old `workspace notify --public-url` is deprecated and fails with a message pointing to `server set`.

Workspace names in group and approval messages use the **display name** (editable at any time under Admin → Workspace). Links and project configuration use the **path** (the name chosen when the workspace was created; it cannot be changed).

- `--show-title=false`, `--show-brief=false`: don't show the title or the group-notification brief in the group (both are shown by default).
- `--off`: turn off group notifications for this workspace.

Members click "Connect Telegram" in the web UI under Profile (个人中心) and open the bot in Telegram to finish connecting. The connection belongs to the account and works in all of their workspaces; after that, group notifications can @-mention them (see [Onboarding new members](onboarding.en.md)). Once the group is set up:

- registration and join requests for this workspace are posted to this group;
- device requests are posted to the group of every workspace of the requester that has a group configured (once approved, the computer works for all their workspaces, and the message says so);
- admins who have connected Telegram can approve directly with the buttons: this applies only to the workspace the group belongs to, and a device request also requires the requester to be a member of this workspace. When a request is handled anywhere, the approval messages in all groups change to show the result;
- when someone joins the workspace, the group announces it and @-mentions them;
- send `/whoami` in the group to see which account you are connected as and your role in this group's workspace.

Check that it works:

```bash
./bin/cohive-server notify test --workspace payments
```

## 3. Which events are posted to the group

Group messages (notifications, reminders and the bot's replies in the group) use the workspace's language: a workspace admin picks it under Admin → Workspace → Group message language on the web, or a server operator runs `cohive-server workspace set --name <path> --language en`. When it is not set, the server's default language `COHIVE_DEFAULT_LANGUAGE` is used (`en` or `zh-CN`; English when unset). Direct messages to a person use the language that person chose in their Profile.

| Event | Who is @-mentioned |
|---|---|
| A new issue is handed to someone (submitted, transferred, or reassigned by an admin when removing a member) | The owner |
| An admin reassigns an issue that was ready for verification to a new reporter while removing a member | The new reporter |
| Declined | The reporter |
| Fixed, please verify | The reporter |
| Verification failed, reopened | The owner |
| Reopened several times, a person needs to step in | The reporter and the owner |

Accepting, starting a fix and comments do not notify.

How people get @-mentioned:
- Members who have connected Telegram are mentioned by **user ID** (the message contains a link to them, with their name as the text). They don't need a Telegram username, and changing it later doesn't stop notifications.
- Older data that has only a username and no user ID (for example registered early through the command line) falls back to `@username`; reconnecting once under Profile in the web UI switches them to mention-by-ID.
- With neither, only the name is written and the person is not notified.
- The mentioned person must be in the group to be notified (the notification is delivered even if they muted the group). For people who aren't in the group, Telegram guarantees that the mention works only if they have chatted privately with the bot (which they did when connecting Telegram) and haven't turned on the "forwarded messages" privacy restriction for the bot.

Delivery rules:
- A failed send is retried automatically with growing intervals, 7 attempts in about 8.5 hours. If the group ID is wrong or the bot was removed from the group, it gives up immediately.
- If the bot token is wrong or the bot is rate limited, delivery pauses and notifications are kept, without counting as failures.
- When a group is upgraded to a supergroup, the new group ID is used automatically.
- The issue is checked before delivery: a notification that has been overtaken by newer developments (for example the issue was transferred to someone else or closed) is not sent, and neither is one that couldn't be delivered within 24 hours. This avoids stale reminders in the group.
- If no bot token is set, notifications waiting to be sent are kept and delivered after you set it and restart the server.
- `workspace list` shows the number of pending and failed notifications for each workspace; after fixing the problem, resend failed ones with `notify retry --workspace <name>`.
- Delivery is at-least-once: if the server crashes right after sending and before recording it, a message may be sent twice.

## 4. Let remote members connect (Cloudflare Tunnel example)

How you make the server reachable is up to your team. This uses a Cloudflare Tunnel as the example. There are only two key points:

1. Forward a public domain to the Cohive server, for example `https://cohive.example.com` → `http://127.0.0.1:8787`.
2. Set this address as the **external URL** (Server admin → Settings, or `cohive-server server set --external-url https://cohive.example.com`). The host name of the external URL is added to MCP's host allowlist automatically and takes effect immediately, with no restart. Otherwise MCP rejects requests with a 403 (DNS rebinding protection).

   If the same server must also be reached under other domains, add them to `COHIVE_ALLOWED_HOSTS` (comma-separated, host names only, without `https://` or a path; if the value is malformed the server refuses to start and tells you):

   ```bash
   COHIVE_ALLOWED_HOSTS=other.example.com scripts/run-server.sh
   ```

With a Docker deployment you can use the optional tunnel service in the compose file (`docker compose --profile tunnel up -d`; see section 4 of [Deploy with Docker](deploy-docker.en.md)).

We recommend reading the tunnel token from a file with `--token-file` instead of putting it in the launch arguments (launch arguments are visible to every user on the machine):

```bash
cloudflared tunnel --protocol http2 run --token-file ~/.config/cohive/cf-tunnel-token
```

Remote members set their coding tool's MCP address to `https://cohive.example.com/mcp`. The token is still the only credential for access; if one is lost, revoke it with `token revoke`.
