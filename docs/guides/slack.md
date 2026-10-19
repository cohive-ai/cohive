# 在自己的 Slack 工作区里接入 Cohive

> 第 1–3 步在 Slack 里操作，第 4 步起在 Cohive 里操作。Slack 后台的菜单名称可能和下面写的略有出入，以实际界面为准。English version: [slack.en.md](slack.en.md)。

接好之后能做到这些：

- 问题单轮到谁处理（新单、请验证、被重开等），项目频道里会出现一条消息并 @ 到他；单子被接、验证通过关闭时，也会告知另一方。消息里只有单号、交接关系和指引，正文不进频道。完整的事件清单见 [群通知](group-notify.md) 第 3 节。
- 有人申请注册、加入或添加新设备时，频道里会出现带“批准 / 拒绝”按钮的审批消息，管理员直接点，消息随即改成结果。
- 成员在 Cohive 网页“个人中心”连接自己的 Slack 账号之后，才能被 @，也才能在频道里点审批按钮。

Cohive 只需要**出站**访问 Slack：它用 Socket 模式主动连到 Slack，不需要公网 IP、域名或内网穿透。

## 0. 开始前

- **Slack 工作区**：免费版就可以。你需要能在这个工作区里装应用（免费版的成员默认可以；付费版的管理员可能开了“装应用需审批”，那就请管理员批准）。免费版每个工作区最多装 10 个应用，Cohive 占一个。
- **所有成员都在这个工作区里**：跨组织共享频道（Slack Connect）里别的工作区的成员，看得到通知，但点按钮、发码都会被拒绝。
- **一个 Slack 应用只接一台 Cohive**：同一个应用同时被两台 Cohive 连着时，Slack 会把按钮点击随机发给其中一台。测试和正式各建一个应用。
- **网络**：Cohive 所在的机器要能访问 `slack.com` 和 Slack 的 WebSocket 地址（`wss://wss-primary.slack.com` 一类，由 Slack 动态下发）。在中国大陆访问 Slack 需要代理，代理由你自己配置，Cohive 不管。

## 1. 用应用清单建应用

仓库里有一份写好的应用清单 [`deploy/slack-app-manifest.yaml`](../../deploy/slack-app-manifest.yaml)，里面已经打开了 Socket 模式、按钮回调、需要的事件和最小权限。

1. 打开 <https://api.slack.com/apps>，点 **Create New App**，选 **From a manifest**。
2. 选要装到的工作区，点 **Next**。
3. 切到 **YAML** 页，把清单文件的内容整段粘贴进去（覆盖原有内容），点 **Next**。
4. 核对一下权限列表，点 **Create**。

清单申请的权限和用途：

| 权限 | 用途 |
|---|---|
| `chat:write` | 发通知和审批消息，审批后改成结果 |
| `app_mentions:read` | 收频道里 @Cohive 的指令（绑定群、群编号、我是谁） |
| `im:history` | 收成员私聊发来的连接码 |
| `channels:read`、`groups:read` | 绑定时记下频道名，在网页上显示 |
| `users:read` | 网页确认连接时，显示是哪位 Slack 用户发来的码 |

Cohive 不读频道里的历史消息，也只收 @它 的消息和发给它的私聊。

## 2. 安装应用，复制机器人令牌

1. 在应用后台左侧点 **Install App**（或 **OAuth & Permissions**），点 **Install to Workspace**，确认授权。
2. 复制 **Bot User OAuth Token**，它以 `xoxb-` 开头。

以后如果改了清单里的权限，要回到这里点 **Reinstall to Workspace** 才会生效。

## 3. 生成应用级令牌

Socket 模式要用一个单独的“应用级令牌”，清单建不了，要手工生成：

1. 左侧点 **Basic Information**，往下找到 **App-Level Tokens**，点 **Generate Token and Scopes**。
2. 名字随便填（例如 `cohive-socket`），点 **Add Scope** 选 `connections:write`，点 **Generate**。
3. 复制生成的令牌，它以 `xapp-` 开头。

两个令牌都相当于密码：不要发到频道里，不要截图外传，下一步只填进 Cohive 服务器。

---

## 4. 把令牌配置进 Cohive

按你运行 Cohive 的方式选一种：

- **Docker 编排部署**（见 [Docker 部署](deploy-docker.md)）：在服务器的 `deploy/.env` 里加两行：

  ```
  COHIVE_SLACK_BOT_TOKEN=xoxb-…
  COHIVE_SLACK_APP_TOKEN=xapp-…
  ```

  然后在 `deploy` 目录执行 `docker compose up -d`。改了 `.env` 要用 `up -d` 重建容器，`restart` 读不到新值。仓库里的 `deploy/docker-compose.yml` 已经把这两个变量传进容器；如果你用的是自己改过的编排文件，记得在 cohive 服务的 `environment` 里加上：

  ```yaml
  COHIVE_SLACK_BOT_TOKEN: ${COHIVE_SLACK_BOT_TOKEN:-}
  COHIVE_SLACK_APP_TOKEN: ${COHIVE_SLACK_APP_TOKEN:-}
  ```

  `.env` 里有令牌，不要提交到任何仓库。

- **本机运行**（见 [单机运行](single-host.md)）：执行 `bash scripts/set-slack-tokens.sh`，按提示先粘贴机器人令牌、再粘贴应用级令牌，输入时都不显示。令牌写入 `~/.config/cohive/slack.env`，只有你自己能读，并且只在服务进程里加载。然后执行 `scripts/cohive-server.sh restart`。

  不用这两个脚本、自己启动 `cohive-server serve` 时，把令牌放在环境变量 `COHIVE_SLACK_BOT_TOKEN`、`COHIVE_SLACK_APP_TOKEN` 里即可。两个变量要一起设置；只设了一个，或者两个填反了，服务启动时会直接报错说明。

启动日志里看到下面两行，就表示接通了：

```
  Group notifications: Slack enabled, Socket Mode connecting; one Slack app can serve only one Cohive server
Slack: Socket Mode connected
```

Cohive 网页“服务器管理 → 设置 → 通讯工具”里能看到 Slack 是否在线；不在线时会写明原因，例如 `invalid_auth`（令牌错了或应用被卸载）。

## 5. 把频道绑定到工作空间

1. 在 Slack 里建一个项目频道（公开或私有都可以），在频道里发 `/invite @Cohive`，把机器人加进来。
2. 用工作空间管理员账号登录 Cohive 网页，进入“管理 → 工作空间”，在“Slack 频道”一行点“生成频道绑定码”。
3. 网页上会给出一整句 `绑定群 XXXXXXXX`。在频道里 @Cohive，把这句发出去，例如 `@Cohive 绑定群 XXXXXXXX`。
4. 机器人回复“已把本频道绑定到工作空间 X”，网页上也会自动显示“已绑定：#频道名”。

绑定码 10 分钟内有效，只能用一次。一个频道只能绑一个工作空间；一个工作空间第一期只绑一个群（已绑别的通讯工具的群，要先解绑）。

也可以手工绑：在频道里 @Cohive 发 `群编号`，机器人回复本频道编号（`C` 开头），服务器管理员在服务器上执行 `cohive-server workspace notify --workspace <路径> --slack-chat <频道编号>`。

在频道里 @Cohive 还可以发 `我是谁`，看自己连接的 Cohive 账号，以及在本频道工作空间里的角色。中文指令和英文指令（`bindgroup`、`chatid`、`whoami`）都认，大小写不要紧。发别的内容，机器人会回复用法。在话题里 @它，它就回到话题里。

## 6. 每位成员连接 Slack

1. 登录 Cohive 网页，进入“个人中心 → 通讯工具”，点“连接 Slack”，网页上会显示一个 8 位绑定码。
2. 在 Slack 左侧栏的“应用”里找到 Cohive（或在搜索里搜 Cohive），进它的“消息”标签，把码发过去。大小写、中间的空格和连字符都不要紧。
3. 机器人回复“已收到”。回到网页，页面会显示“机器人收到了 Slack 用户 某某 发来的绑定码”，确认是你本人就点“是我，确认连接”；不是你就点“不是我，取消”。确认之后才生效。

注意：码只能**私聊**发给机器人。发到频道里的码会立即作废，回网页重新生成一个就行。

## 7. 验收清单

- [ ] 发一张问题单交给某人：频道里出现消息，被 @ 的人收到提醒。
- [ ] 用团队邀请链接注册一个新成员：频道里出现审批消息。管理员点“批准”后，消息改成“已批准（审批人 …）”，按钮消失。
- [ ] 再来一次，这次在网页“管理 → 待审”里拒绝：频道里的消息同步改成“已拒绝”。
- [ ] 让一位**不是管理员**的成员点审批按钮：只有他自己看到“没有权限”的提示，申请不受影响。
- [ ] 把账号绑定码发到频道里：提示码已作废（回复里不会出现这个码），网页上这次连接显示失败。
- [ ] 服务器管理 → 设置 → 通讯工具：Slack 显示“在线”。

## 常见问题

- **频道里 @Cohive 没有反应**：看启动日志里有没有 `Slack: Socket Mode connected`；确认机器人已经 `/invite` 进这个频道；确认应用设置里 Socket Mode 是开着的。
- **通知发不出去，日志里是 `not_in_channel` 或 `channel_not_found`**：机器人不在这个频道里，或频道被归档了。在频道里 `/invite @Cohive`。
- **日志或通道状态里是 `missing_scope`**：清单里的权限改过但没有重新安装应用。到 **Install App** 点 **Reinstall to Workspace**。
- **状态显示 `invalid_auth`**：令牌复制错了、被重新生成过，或应用被卸载了。重新复制两个令牌，更新配置后重建容器或重启服务。
- **状态提示 Socket Mode 被关了**：到应用后台 **Socket Mode** 页把它打开。
- **审批消息没有按钮**：发消息时 Socket 模式不在线（点了按钮也收不到）。到网页“管理 → 待审”里处理，处理后频道里的消息照样会改成结果。
- **能看到通知，但没有 @ 到人**：被 @ 的人还没连接 Slack（第 6 步）。
- **跨组织共享频道里的外部成员点按钮被拒绝**：这是有意的。审批只认本工作区的成员。
- **同一个应用能不能给测试和正式两台 Cohive 用**：不能，见第 0 节。

## 关于 Slack 的接口条款

Slack 自 2025 年 10 月起的接口条款规定：用户可能为之付费的产品，其 Slack 应用要经应用市场分发，而应用市场要求公网回调地址，不能用 Socket 模式。Cohive 开源、免费，每个团队在自己的工作区里自建应用，不属于这种情况。如果你基于 Cohive 向客户提供收费服务，请先自行评估这条条款。
