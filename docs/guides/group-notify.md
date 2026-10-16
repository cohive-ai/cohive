# 群通知与公网访问

Cohive 的群通知是“群里敲门，客户端开门”：问题单轮到谁处理，工作空间的群里就出现一条只含信号的消息并 @ 到人；被 @ 的人在自己的编程工具里用 `/cohive 12`（Claude Code）或 `$cohive 12`（Codex）拉取完整内容、初筛、拍板。复现脚本、证据、日志从不发到群里。

## 1. 准备机器人

1. 在 Telegram 里找 @BotFather 创建机器人，拿到机器人令牌（形如 `123456789:AAH…`，冒号两边都是令牌的一部分）。
2. 在运行协作服务的机器上保存令牌，输入时不显示、不进命令历史：

   ```bash
   bash scripts/set-telegram-token.sh
   ```

   令牌写入 `~/.config/cohive/telegram.env`（仅本人可读）。用启动脚本运行服务，令牌只在服务进程里加载：

   ```bash
   scripts/run-server.sh
   ```

   不要在交互终端里 `source` 这个文件：那样之后从这个终端启动的所有程序（包括 AI 编程工具）都能读到机器人令牌。

3. 把机器人拉进工作空间对应的群。机器人只需要普通成员身份；设成管理员后它能收到群里所有消息（Cohive 会直接丢弃非命令消息，不存储）。

## 2. 配置工作空间

一个业务组一个工作空间、一个群。以下命令在运行协作服务的机器上执行。

```bash
./bin/cohive-server workspace add --name payments
```

服务启动后，在群里发 `/chatid`，机器人回复群编号和一条可直接复制的配置命令：

```bash
./bin/cohive-server workspace notify --workspace payments --telegram-chat -1001234567890 --public-url https://cohive.example.com
```

- `--public-url`：群消息里看板链接的地址，没有公网地址可以不填。
- `--show-title=false`、`--show-brief=false`：不在群里显示标题、群通知简述（默认都显示）。
- `--off`：关闭这个工作空间的群通知。

成员在群里发 `/join` 申请加入，管理员在群里点“批准”后，机器人自动登记其 Telegram 用户名和用户编号，群通知就能 @ 到人（见 [新成员接入](onboarding.md)）。已有成员也可以手工登记：发 `/whoami` 查到用户名与编号后执行

```bash
./bin/cohive-server member set --workspace payments --name lee --telegram lee_ut --telegram-id 7012345678
```

确认接通：

```bash
./bin/cohive-server notify test --workspace payments
```

## 3. 哪些事件会发到群里

| 事件 | @ 谁 |
|---|---|
| 新单交给某人（发出、转交） | 负责人 |
| 被退回 | 发起人 |
| 已修复，请验证 | 发起人 |
| 验证未通过，已重开 | 负责人 |
| 多次重开，需要人介入 | 发起人与负责人 |

接单、开始修复、评论不通知。

投递规则：
- 发送失败自动重试，间隔逐步拉长，共 7 次、约 8.5 小时；群编号错误或机器人被移出群时直接放弃。
- 机器人令牌错误、被限流时暂停投递，通知保留，不计失败次数。
- 群升级为超级群后，自动改用新的群编号。
- 投递前核对问题单：已有新进展（例如已转交给别人、已关闭）的通知不再发送，超过 24 小时未送达的也不再发送，避免群里出现过时的提醒。
- 没设置机器人令牌时，待发通知会保留，设置后重启服务即补发。
- `workspace list` 显示每个工作空间的待发与失败条数；修好问题后用 `notify retry --workspace <名称>` 重发失败的通知。
- 至少送达一次：服务恰好在发出后、记录前崩溃时，可能重复发一条。

## 4. 让异地成员访问（Cloudflare 隧道示例）

网络打通由团队自行选择方式，这里以 Cloudflare 隧道为例。要点只有两个：

1. 把公网域名转发到协作服务，例如 `https://cohive.example.com` → `http://127.0.0.1:8787`。
2. 启动协作服务时把这个域名加入白名单，否则 MCP 会以 403 拒绝（DNS 重绑定防护）：

   ```bash
   COHIVE_ALLOWED_HOSTS=cohive.example.com scripts/run-server.sh
   ```

   只写域名，不要带 `https://` 或路径，写错时服务会拒绝启动并提示。

隧道令牌建议用 `--token-file` 从文件读取，不要写在启动参数里（启动参数对本机所有用户可见）：

```bash
cloudflared tunnel --protocol http2 run --token-file ~/.config/cohive/cf-tunnel-token
```

异地成员的编程工具把 MCP 地址设为 `https://cohive.example.com/mcp`；令牌仍是访问的唯一凭据，丢失时用 `token revoke` 吊销。
