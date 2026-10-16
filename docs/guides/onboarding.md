# 新成员接入

新成员在团队群里申请加入，在自己电脑上执行一条命令、在 Telegram 里点一下即可完成登录。全程不复制粘贴任何码或令牌，令牌也不会出现在聊天记录或项目文件里。

## 管理员先做的事（每个工作空间一次）

1. 按 [群通知](group-notify.md) 配好工作空间的群（`/join` 只在这个群里有效）。
2. 把自己设为管理员（只有管理员能审批加入申请），并绑定自己的 Telegram 用户编号（私聊机器人发 `/whoami` 可查）：

   ```bash
   ./bin/cohive-server member set --workspace payments --name lee --telegram-id 7012345678 --role admin
   ```

3. 在项目仓库里执行一次 `cohive login --server https://cohive.example.com` 与 `cohive init`，把生成的配置提交进仓库。之后的成员拉下仓库就能直接登录。

加入默认需要管理员批准（和 GitLab 的申请加入一样）。信得过群里所有人时，可以关闭审批，群成员发 `/join` 即直接加入：

```bash
./bin/cohive-server workspace set --workspace payments --join-approval=false
```

## 成员自己做的三步

**1. 在团队群里发 `/join`。** 机器人在群里发出审批消息，管理员点“批准”后你就成为成员：

- 成员名默认取你的 Telegram 用户名（转小写；和已有成员重名时加 `-2` 等后缀；没有用户名时为 `tg` 加用户编号）。
- 机器人同时记下你的 Telegram 用户编号和用户名：编号用来确认“在机器人里确认登录的是本人”，用户名用来在群通知里 @ 到你。
- 建议先在 Telegram 设置里设好用户名，否则群通知没法 @ 到你。

**2. 安装 cohive 命令行**（编程工具要能在 PATH 里找到它）：

```bash
make install-cli
```

**3. 在仓库目录执行 `cohive login`：**

```bash
cohive login
```

终端会显示一个链接、一个二维码和一个两位数，并自动打开 Telegram。在和机器人的私聊里会看到登录确认，**点选和终端上一样的数字**，终端随即显示“已登录”。

- 仓库里已有 `cohive init` 生成的配置时，服务地址、工作空间和要登录的工具（Claude Code、Codex）都从配置读取，一次确认同时登录两个工具。
- 还没有配置的仓库，加上服务地址：`cohive login --server https://cohive.example.com`，登录后执行 `cohive init`。
- 只登录某个工具：`--tools codex`；指定工作空间：`--workspace payments`（加入了多个工作空间又没指定时，机器人会先让你选）。
- 令牌保存在 `~/.config/cohive/credentials.json`，只有你本人可读。`cohive status` 可以查看已登录的工作空间（不显示令牌）。

之后在 Claude Code 里用 `/cohive`、在 Codex 里用 `$cohive` 查看和处理分给你的问题单。第一次在编程工具里打开项目时，会询问是否信任项目、是否启用 `cohive` 这个 MCP 服务，都选同意。

## 登录确认的安全设计

- 登录请求 10 分钟内有效，令牌只交付一次，只有发起登录的那台电脑能取回（凭只有它知道的轮询密钥）。
- 三个数字里只有一个和终端一致，**选错立即作废**；链接被第二个 Telegram 账号打开也会作废。
- **只确认你自己刚刚在终端发起的登录。** 如果有人发来一个登录链接、并告诉你点哪个数字，那是在骗你把身份交给他的电脑，请点“取消”。

## cohive init 写入的文件

| 工具 | 文件 |
|---|---|
| Claude Code | `.mcp.json`（`cohive` 条目）、`.claude/settings.json`（确认规则）、`.claude/skills/cohive/SKILL.md` |
| Codex | `.codex/config.toml`（`# >>> cohive` 与 `# <<< cohive` 之间的一段）、`.agents/skills/cohive/SKILL.md` |

- 只改动自己管理的部分，原有配置保留；重复执行结果相同。
- 这些文件**不含令牌**：编程工具通过本机命令 `cohive mcp-proxy` 连接协作服务，令牌由它从本机凭据文件读取。所以可以放心提交进仓库，团队共用；每个人用自己的登录。
- 没有信任的项目里，确认规则以外的放行规则不会生效。
- `.claude/settings.json` 里的 `enabledMcpjsonServers` 会随仓库共享：以后有人改动 `.mcp.json` 里 cohive 的启动命令，其他成员不会再被单独询问。和“提交者能改钩子”是同一级别的信任，审查合并请求时留意这两个文件的改动。
- `.mcp.json` 和 `.codex/config.toml` 里有协作服务地址，公开仓库要注意。
- `cohive init` 不会跟随符号链接写入；原有配置类型不对（例如 `permissions.allow` 不是数组）时会报错而不是覆盖。

## 备用方式

- **机器人发不出审批消息、或管理员不方便在群里操作**：在服务器上处理申请。

  ```bash
  ./bin/cohive-server join list --workspace payments
  ./bin/cohive-server join approve --id 3
  ```

- **登录码**：私聊机器人发 `/login` 领一次性登录码（10 分钟有效，只能用一次，新码签发后旧码作废），每个工具登录一次：

  ```bash
  cohive login --code ABCD-EFGH --tool codex
  ```

- **没有 Telegram 的成员**：管理员先 `member add` 添加成员，再用 `cohive-server token issue` 签发令牌，通过可靠渠道交给成员；成员把令牌从标准输入传给 `cohive login`（不会进 shell 历史）：

  ```bash
  pbpaste | cohive login --server https://cohive.example.com --token-stdin
  ```

## 常见问题

- **终端提示“登录没有通过”**：在机器人里点了取消、选错了数字，或链接被别的账号打开过。重新执行 `cohive login`。
- **机器人回复“还没有加入任何 Cohive 工作空间”**：先在团队群里发 `/join`，等管理员批准。
- **`/join` 提示这个群还没有对应的工作空间**：管理员还没把这个群配置成工作空间的群，见 [群通知](group-notify.md)。
- **服务端提示没有启用 Telegram 机器人**：服务没有设置 `COHIVE_TELEGRAM_BOT_TOKEN`，只能用令牌方式登录。
- **编程工具里提示“Cohive 令牌无效或已吊销”**：令牌被管理员吊销了，重新执行 `cohive login`。
- **电脑丢失或令牌泄露**：请管理员用 `cohive-server token list` 找到对应令牌并 `token revoke`；重新登录会签发新令牌，但不会自动吊销旧的。
