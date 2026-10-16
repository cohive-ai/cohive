# 第一阶段·里程碑五：邀请配对、设备与通讯工具连接 实现计划

**目标：** 成员凭管理员给的邀请链接，在终端执行一条 `cohive login <链接>` 即与团队的协作服务配对；每台电脑是一个设备，加设备由成员发起、管理员批准；通讯工具只是“连接”上去的，用来在群里 @ 人和点按钮审批，不是入会和登录的前提。

**依据（2026-10-16 与 lee 确认）：**
- 配对由服务器自己负责，不经过通讯工具。开源自部署，各团队各有服务器，邀请链接里自带服务器地址。
- 加入要“自助 + 审批”（同 GitLab）；也要能由管理员直接邀请某人。
- 通讯工具的核心概念是“连接”，不是靠通讯工具建立用户关系。连接由终端发起：打开带参数的机器人链接完成绑定。以后换 Slack 等只换这一步。
- 允许一人多设备，但不能由成员自由添加：加设备由成员发起、管理员批准。
- 修订前的版本（群里 `/join` 入会、机器人里确认登录、私聊领登录码）以通讯工具为根，已废弃。

## 概念

| 概念 | 说明 |
|---|---|
| 邀请 | 管理员或成员生成，链接形如 `https://服务器/i/邀请码`，自带服务器地址。三种：个人邀请、团队邀请、设备邀请 |
| 配对申请 | 终端兑换邀请后产生；需要审批的停在“待审”，终端凭只有自己知道的轮询密钥取结果 |
| 设备 | 每台电脑一条；令牌挂在设备上，吊销设备即吊销它的全部令牌 |
| 通讯连接 | 成员的 Telegram 账号与成员绑定，只用于群通知 @ 人和群里审批按钮 |

**三种邀请：**

| 类型 | 谁生成 | 次数 | 兑换后 |
|---|---|---|---|
| 个人邀请 | 管理员（`cohive-server invite create --name ann`；`--member ann` 给已有成员接入新电脑；`--admin` 生成管理员） | 一次 | 直接生效，免审批 |
| 团队邀请 | 管理员（`cohive-server invite create --team [--uses N] [--expires 7d]`） | 多次 | 新建成员的申请，需管理员批准 |
| 设备邀请 | 成员在已登录的电脑上 `cohive device add` | 一次，1 小时 | 给该成员加设备的申请，需管理员批准 |

第一个管理员：在服务器上 `cohive-server invite create --name lee --admin`，自己兑换。

## 流程

**配对：** `cohive login https://服务器/i/邀请码 [--tools claude-code,codex] [--name ann]`
1. 终端从链接取出服务器地址与邀请码，`POST /api/v1/pair/start {invite, device_name, tools, name, display_name}`。
2. 个人邀请：服务端建成员（或用已有成员）与设备，直接返回令牌。
3. 团队邀请、设备邀请：返回 `{status: pending, request_id, poll_secret}`；服务端在工作空间群里发审批消息（配了机器人时），也可在服务器上 `cohive-server request approve --id N`。
4. 终端每 2 秒 `POST /api/v1/pair/poll` 等待；最多等 10 分钟，之后保存待审申请到本机，批准后执行 `cohive login --resume` 领取。申请 24 小时内有效。
5. 批准后轮询拿到各工具的令牌（只交付一次），保存到本机凭据（带设备编号）。

**已配对的电脑补登录其他工具：** `cohive login --tools codex`，用本机已有令牌证明身份，在同一设备上签发，不需审批。

**加设备：** 旧电脑 `cohive device add` 得到一次性设备邀请链接，新电脑 `cohive login <链接>`，管理员批准。旧电脑丢了：管理员 `invite create --member ann` 发个人邀请，并 `device revoke` 吊销丢失的设备。

**连接通讯工具：** `cohive link telegram`
1. 终端用本机令牌 `POST /api/v1/links/start {provider: telegram}`，得到 `https://t.me/机器人?start=ln_xxx` 并打开。
2. 机器人收到 `/start ln_xxx`，把这个 Telegram 账号（用户编号与用户名）绑定到发起的成员，回复结果。
3. 终端轮询 `GET /api/v1/links/{id}` 显示结果。同一工作空间里一个 Telegram 账号只能连一个成员。

**审批：** 只有管理员能批。群里按钮按点击者已连接的 Telegram 账号校验管理员身份；命令行 `cohive-server request list|approve|reject` 不受限（服务器管理员）。

**设备管理：** `cohive-server device list [--member]`、`device revoke --id`。`cohive-server token issue` 与 `cohive login --token-stdin` 保留，作为自动化与应急入口。

**移除：** 群里 `/join`、私聊 `/login` 登录码、`/api/v1/login`、`cohive login --code`、机器人确认登录、工作空间“加入需审批”开关。`login_codes` 表保留不用（删除表需另行确认）。

## 安全要点

- 邀请码 24 字节随机数，只存哈希；可设次数、过期，`invite revoke` 立即作废。
- 兑换接口不需身份：按来源地址限频；邀请码错误计入全局失败上限。
- 轮询先不加锁核对密钥，只在交付时锁行；令牌只交付一次。
- 终端只接受与邀请链接同一服务器的地址，打开的机器人链接只接受 `https://t.me/` 开头。
- 设备名由终端自报，只允许主机名字符。

## 任务

1. 迁移 0006 重写：成员角色、设备、令牌挂设备、邀请、配对申请、通讯连接请求。
2. 业务层：邀请生成与兑换、审批、轮询交付、设备列表与吊销、已配对设备补签工具、通讯连接。
3. 接口：配对发起与轮询、邀请落地页、设备邀请、补签工具、通讯连接。
4. 机器人：群里审批按钮、`/start ln_` 连接；去掉 `/join`、`/login`、确认登录。
5. 管理命令：`invite`、`request`、`device`；成员命令：`login <链接>`、`login --resume`、`device add`、`link telegram`。
6. 文档与真实环境实测。
