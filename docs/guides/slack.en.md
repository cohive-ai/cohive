# Integrate Cohive with your own Slack workspace

> Steps 1–3 happen in Slack; from step 4 on you work in Cohive. Slack's menu names may differ slightly from what's written here; go by what you see. 中文版：[slack.md](slack.md).

Once it's set up:

- When it's someone's turn on an issue (a new issue, a verification request, a reopen, and so on), a message appears in the project channel and @-mentions them. When an issue is accepted, or closed after passing verification, the other side is told too. The message carries only the issue number, the hand-off and what to do next; the issue body never goes into the channel. The full list of events is in section 3 of [Group notifications](group-notify.en.md).
- When someone asks to sign up, join or add a new device, an approval message with Approve and Reject buttons appears in the channel. An admin clicks one and the message turns into the result.
- Members can be @-mentioned, and can click approval buttons, only after they connect their Slack account under Profile in the Cohive web app.

Cohive only needs **outbound** access to Slack: it connects out to Slack in Socket Mode, so you don't need a public IP, a domain or a tunnel.

## 0. Before you start

- **A Slack workspace**: the free plan is fine. You need to be able to install apps in it (members of a free workspace can by default; on a paid plan an admin may require app approval, so ask them to approve it). A free workspace can install at most 10 apps, and Cohive takes one.
- **Everyone is in this workspace**: members of other workspaces in a shared channel (Slack Connect) can see notifications, but their button clicks and codes are refused.
- **One Slack app serves one Cohive server**: if two Cohive servers connect with the same app, Slack hands each button click to one of them at random. Create separate apps for testing and production.
- **Network**: the machine running Cohive must be able to reach `slack.com` and Slack's WebSocket hosts (something like `wss://wss-primary.slack.com`, handed out by Slack at connect time). If Slack is blocked where you are, set up a proxy yourself; Cohive doesn't manage proxies.

## 1. Create the app from the manifest

The repository has a ready-made app manifest, [`deploy/slack-app-manifest.yaml`](../../deploy/slack-app-manifest.yaml). It turns on Socket Mode and button interactivity and asks for the events and minimal scopes Cohive needs.

1. Open <https://api.slack.com/apps>, click **Create New App** and choose **From a manifest**.
2. Pick the workspace to install into and click **Next**.
3. Switch to the **YAML** tab, paste the whole manifest file over what's there and click **Next**.
4. Review the scopes and click **Create**.

Scopes in the manifest and what they're for:

| Scope | Used for |
|---|---|
| `chat:write` | Posting notifications and approval messages, and updating them with the result |
| `app_mentions:read` | Commands sent as @Cohive in a channel (bindgroup, chatid, whoami) |
| `im:history` | Link codes members send in a direct message |
| `channels:read`, `groups:read` | Recording the channel name when binding, to show it in the web UI |
| `users:read` | Showing which Slack user sent the code when you confirm the connection in the web UI |

Cohive doesn't read channel history. It only receives messages that @-mention it and direct messages sent to it.

## 2. Install the app and copy the bot token

1. In the app settings, click **Install App** (or **OAuth & Permissions**) on the left, then **Install to Workspace**, and allow it.
2. Copy the **Bot User OAuth Token**. It starts with `xoxb-`.

If you later change the scopes in the manifest, come back here and click **Reinstall to Workspace** for the change to take effect.

## 3. Generate the app-level token

Socket Mode needs a separate app-level token, which the manifest can't create:

1. Click **Basic Information** on the left, scroll to **App-Level Tokens** and click **Generate Token and Scopes**.
2. Give it any name (for example `cohive-socket`), click **Add Scope**, choose `connections:write`, and click **Generate**.
3. Copy the token. It starts with `xapp-`.

Treat both tokens like passwords: don't post them in a channel or share screenshots of them. In the next step they go only into the Cohive server.

---

## 4. Configure the tokens in Cohive

Pick the one that matches how you run Cohive:

- **Docker Compose** (see [Docker deployment](deploy-docker.en.md)): add two lines to `deploy/.env` on the server:

  ```
  COHIVE_SLACK_BOT_TOKEN=xoxb-…
  COHIVE_SLACK_APP_TOKEN=xapp-…
  ```

  Then run `docker compose up -d` in the `deploy` directory. After changing `.env`, use `up -d` to recreate the container; `restart` doesn't pick up new values. The repository's `deploy/docker-compose.yml` already passes both variables into the container. If you use your own compose file, add these to the `environment` of the cohive service:

  ```yaml
  COHIVE_SLACK_BOT_TOKEN: ${COHIVE_SLACK_BOT_TOKEN:-}
  COHIVE_SLACK_APP_TOKEN: ${COHIVE_SLACK_APP_TOKEN:-}
  ```

  `.env` holds the tokens: never commit it to any repository.

- **Running on your own machine** (see [Single-host setup](single-host.en.md)): run `bash scripts/set-slack-tokens.sh` and paste the bot token first, then the app-level token when asked. Neither is echoed. They're written to `~/.config/cohive/slack.env`, readable only by you, and loaded only inside the server process. Then run `scripts/cohive-server.sh restart`.

  If you start `cohive-server serve` yourself without these scripts, put the tokens in the environment variables `COHIVE_SLACK_BOT_TOKEN` and `COHIVE_SLACK_APP_TOKEN`. Set both: if only one is set, or the two are swapped, the server refuses to start and says why.

When you see these two lines in the startup log, it's connected:

```
  Group notifications: Slack enabled, Socket Mode connecting; one Slack app can serve only one Cohive server
Slack: Socket Mode connected
```

Under Server admin > Settings > Chat apps in the Cohive web app you can see whether Slack is online. When it's offline the reason is shown, for example `invalid_auth` (a wrong token, or the app was uninstalled).

## 5. Bind a channel to a workspace

1. Create a project channel in Slack (public or private) and send `/invite @Cohive` in it to add the bot.
2. Sign in to the Cohive web app as a workspace admin, go to Admin > Workspace, and click Generate channel code on the Slack channel row.
3. The page shows a full message, `bindgroup XXXXXXXX`. @-mention Cohive in the channel and send it, for example `@Cohive bindgroup XXXXXXXX`.
4. The bot replies that the channel is now bound to workspace X, and the web page updates to show "Bound: #channel-name".

The code is valid for 10 minutes and works once. A channel can be bound to only one workspace, and in this first release a workspace binds to one group (unbind a group in another chat app first).

You can also bind by hand: @-mention Cohive in the channel with `chatid`, the bot replies with the channel ID (starting with `C`), and a server admin runs `cohive-server workspace notify --workspace <path> --slack-chat <channel ID>` on the server.

You can also @-mention Cohive with `whoami` to see the Cohive account you've connected and your role in the channel's workspace. Both English and Chinese commands work (`绑定群`, `群编号`, `我是谁`), in any letter case. Anything else gets a usage reply. If you @-mention it in a thread, it replies in that thread.

## 6. Each member connects Slack

1. Sign in to the Cohive web app, go to Profile > Chat apps and click Connect Slack. The page shows an 8-character link code.
2. In Slack, find Cohive under Apps in the sidebar (or search for Cohive), open its Messages tab and send it the code. Letter case, spaces and hyphens don't matter.
3. The bot replies "Received". Back in the web app, the page says the bot received a link code from Slack user so-and-so. If that's you, click "Yes, that's me. Connect"; if not, click "Not me, cancel". The connection only takes effect after you confirm.

Note: send the code to the bot in a **direct message** only. A code posted in a channel is voided immediately; just generate a new one in the web app.

## 7. Acceptance checklist

- [ ] Send someone an issue: a message appears in the channel and the person is notified by the @-mention.
- [ ] Register a new member with a team invite link: an approval message appears. When an admin clicks Approve, the message changes to "Approved (by …)" and the buttons go away.
- [ ] Do it again, but reject it in the web app under Admin > Pending: the message in the channel changes to "Rejected" as well.
- [ ] Have a member who is **not** an admin click an approval button: only they see a "no permission" notice, and the request is unaffected.
- [ ] Post an account link code in the channel: the reply says the code was voided (without repeating it), and the web page shows the connection failed.
- [ ] Server admin > Settings > Chat apps shows Slack as online.

## Troubleshooting

- **Nothing happens when I @-mention Cohive**: check the startup log for `Slack: Socket Mode connected`; make sure the bot was `/invite`d to this channel and that Socket Mode is on in the app settings.
- **Notifications fail with `not_in_channel` or `channel_not_found`**: the bot isn't in the channel, or the channel was archived. Send `/invite @Cohive` in it.
- **`missing_scope` in the log or the channel status**: the scopes in the manifest changed but the app wasn't reinstalled. Go to **Install App** and click **Reinstall to Workspace**.
- **Status shows `invalid_auth`**: a token was copied wrong, regenerated, or the app was uninstalled. Copy both tokens again, update the configuration and recreate the container or restart the server.
- **Status says Socket Mode is turned off**: turn it on under **Socket Mode** in the app settings.
- **Approval messages have no buttons**: Socket Mode was offline when the message was posted (a click couldn't have reached Cohive). Handle it in the web app under Admin > Pending; the message in the channel is still updated with the result.
- **Notifications arrive but nobody is @-mentioned**: the person hasn't connected Slack yet (step 6).
- **External members of a shared channel are refused when they click a button**: that's intentional. Only members of this workspace can approve.
- **Can one app serve both a test and a production Cohive?** No, see section 0.

## About Slack's API terms

Slack's API terms, in effect since October 2025, require that Slack apps for products users may pay for be distributed through the Slack Marketplace, and the Marketplace requires a public request URL rather than Socket Mode. Cohive is open source and free, and each team creates its own app in its own workspace, so this doesn't apply. If you offer a paid service to customers based on Cohive, assess this term for yourself first.
