# 第一阶段·里程碑五：机器人完成加入与登录 实现计划

**目标：** 新成员在群里发 `/join` 申请加入，管理员在群里点“批准”；成员在仓库里执行 `cohive login`，点终端给出的链接，在机器人私聊里选中终端显示的数字即完成登录。全程不复制粘贴任何码或令牌。

**依据：** 2026-10-16 与 lee 确认：自助申请 + 管理员审批（同 GitLab）；成员名默认取 Telegram 用户名；登录码方式保留作备用。

## 全局约束

- 加入默认需要管理员批准；工作空间可关闭审批（群成员直接加入）。
- 审批消息发在工作空间的群里，按钮按点击者的 Telegram 用户编号校验管理员身份。
- `/join` 只在工作空间配置的那个群里有效（群编号对应工作空间）。
- 登录确认必须选中终端显示的数字（三选一），选错即作废本次登录，防止“别人发起、骗你确认”。
- 登录请求 10 分钟过期；令牌只交付一次；终端凭只有自己知道的轮询密钥取令牌。
- 一次确认可为多个工具（claude-code、codex）各签发一个令牌。
- `cohive login` 在已执行过 `cohive init` 的仓库里自动读出服务地址；没有时用 `--server`。
- Telegram 回调数据不超过 64 字节；所有用户输入转义。

## 设计

**数据（迁移 0006）：**
- `members.role`（`member` / `admin`，默认 `member`）
- `workspaces.join_requires_approval boolean default true`
- `join_requests(id, workspace_id, telegram_user_id, telegram_username, display_name, status, decided_by, created_at, decided_at)`；同一工作空间同一账号只能有一条待审申请
- `device_logins(id, poll_secret_hash, start_nonce unique, match_number, tools text[], device_name, workspace_name, telegram_user_id, member_id, status, expires_at, created_at)`

**机器人：**
- 群里 `/join`：已是成员则提示；否则建申请。需要审批时在群里发审批消息（批准 / 拒绝按钮）；不需要时直接加入。
- 审批按钮：只有管理员能点；批准后建成员（用户名冲突时加数字后缀）并绑定编号与用户名，编辑审批消息显示结果，在群里 @ 新成员告知下一步。
- 私聊 `/start dl_<nonce>`：找到登录请求与发起人所在的工作空间，显示设备名和三个数字按钮（含取消）；多个工作空间且请求未指定时先选工作空间。
- 数字按钮：选对则批准，选错则作废；编辑消息显示结果。

**接口：**
- `POST /api/v1/device/start {tools, device_name, workspace}` → `{request_id, poll_secret, link, match_number, expires_in}`；未启用机器人时返回 503 并提示改用登录码。
- `POST /api/v1/device/poll {request_id, poll_secret}` → `pending` / `denied` / `expired` / `approved`（附各工具令牌，只返回一次）。

**命令行：** `cohive login [--server] [--tools claude-code,codex] [--workspace]`：默认走设备授权，显示链接、二维码与数字，轮询直到完成；`--code`、`--token-stdin` 保留。

**管理命令：** `member set --role admin|member`；`workspace notify ... --join-approval=false`；`join list`、`join approve|reject --id`（机器人不可用时的备用）。

## 任务

1. 迁移 0006、成员角色、加入审批开关与管理命令。
2. Telegram 客户端：内联按钮、回调查询、编辑消息、回应回调。
3. 加入流程（业务层 + 机器人 `/join` 与审批按钮）。
4. 设备授权登录（业务层 + 接口 + 机器人深链与数字按钮）。
5. `cohive login` 设备授权流程（读取仓库配置中的服务地址、二维码、轮询）。
6. 文档更新与真实环境实测。
