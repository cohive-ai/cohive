# 群通知与公网访问

Cohive 的群通知是“群里敲门，客户端开门”：问题单轮到谁处理，工作空间的群里就出现一条只含信号的消息并 @ 到人；被 @ 的人在自己的编程工具里用 `/cohive 12`（Claude Code）或 `$cohive 12`（Codex）拉取完整内容、初筛、拍板。复现脚本、证据、日志从不发到群里。

## 1. 准备机器人

1. 在 Telegram 里找 @BotFather 创建机器人，拿到机器人令牌（形如 `123456789:AAH…`，冒号两边都是令牌的一部分）。
2. 在运行协作服务的机器上保存令牌，输入时不显示、不进命令历史：

   ```bash
   bash scripts/set-telegram-token.sh
   ```

   令牌写入 `~/.config/cohive/telegram.env`（仅本人可读）。启动服务前加载它：

   ```bash
   source ~/.config/cohive/telegram.env
   ```

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

每位成员在群里发 `/whoami`，机器人回复其 Telegram 用户名；管理员据此登记，群通知才能 @ 到人：

```bash
./bin/cohive-server member set --workspace payments --name lee --telegram lee_ut
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

接单、开始修复、评论不通知。发送失败会按 10 秒、1 分钟、5 分钟、30 分钟自动重试，共 5 次；群编号错误或机器人被移出群时直接放弃。没设置机器人令牌时，待发通知会保留，设置后重启服务即补发。

## 4. 让异地成员访问（Cloudflare 隧道示例）

网络打通由团队自行选择方式，这里以 Cloudflare 隧道为例。要点只有两个：

1. 把公网域名转发到协作服务，例如 `https://cohive.example.com` → `http://127.0.0.1:8787`。
2. 启动协作服务时把这个域名加入白名单，否则 MCP 会以 403 拒绝（DNS 重绑定防护）：

   ```bash
   COHIVE_ALLOWED_HOSTS=cohive.example.com ./bin/cohive-server serve
   ```

隧道令牌建议用 `--token-file` 从文件读取，不要写在启动参数里（启动参数对本机所有用户可见）：

```bash
cloudflared tunnel --protocol http2 run --token-file ~/.config/cohive/cf-tunnel-token
```

异地成员的编程工具把 MCP 地址设为 `https://cohive.example.com/mcp`；令牌仍是访问的唯一凭据，丢失时用 `token revoke` 吊销。
