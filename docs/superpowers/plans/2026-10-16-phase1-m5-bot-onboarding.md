# 第一阶段·里程碑五：网页账号、邀请注册、终端经浏览器授权、通讯工具连接 实现计划

**目标：** 管理员在网页上发邀请链接；成员打开链接在网页注册；在终端执行 `cohive login`，浏览器里点“授权”即完成这台电脑的配对；在个人中心连接 Telegram。通讯工具只是连接上去的，用于群里 @ 人和点按钮审批，不是注册和登录的前提。成员、设备、邀请、审批都在网页上管理，命令行只做服务器上的兜底。

**依据（2026-10-16 与 lee 确认）：**
- 配对由服务器负责，不经过通讯工具；开源自部署，各团队各有服务器。
- 有网页后：邀请链接 → 网页注册 → 个人中心 → 终端绑定。设备绑定“终端发起、浏览器授权”，不在页面和终端之间搬运秘密。
- 注册“自助 + 审批”（团队邀请）与“管理员直接邀请”（个人邀请）都要。
- 一人可多设备：账号获批后的第一台设备免审，之后加设备需管理员批准。
- 网页登录两种都要：账号密码；已配对的电脑上 `cohive web` 免密登录。
- 第一个管理员：首次启动生成随机初始密码，写进服务器数据目录下的文件（不进日志），首次登录强制改密码，改完删除文件；忘记密码用 `cohive-server admin reset-password`。
- 通讯工具连接在个人中心发起：网页打开带参数的机器人链接，机器人完成绑定。
- 修订前的群里 `/join`、机器人里确认登录、私聊 `/login` 登录码全部废弃。

## 数据（迁移 0006_invites_and_devices）

- `members`：`role`（member/admin）、`password_hash`（bcrypt，可空）、`must_change_password`
- `devices(id, member_id, name, created_at, revoked_at)`；`tokens.device_id`
- `invites(id, workspace_id, code_hash, kind personal|team, role, max_uses, uses, created_by, expires_at, revoked_at, created_at)`
- `signups(id, invite_id, workspace_id, name, display_name, password_hash, status pending|approved|rejected, member_id, decided_by, created_at, decided_at)`
- `device_auths(id, nonce, poll_secret_hash, tools, device_name, status, member_id, workspace_id, device_id, decided_by, created_at, expires_at)`；状态：`pending`（等浏览器授权）→ `awaiting_admin`（非首台设备，等管理员）→ `approved` → `delivered`；或 `denied`、`expired`
- `link_requests(id, member_id, provider, nonce, status pending|linked|failed, detail, expires_at, created_at)`
- `web_sessions(id_hash, member_id, created_at, expires_at)`、`web_login_codes(code_hash, member_id, expires_at, used_at)`

## 接口约定（网页与命令行共用，均返回 JSON，错误为 `{"error": "中文说明"}`）

鉴权：网页用会话 Cookie `cohive_session`（HttpOnly、SameSite=Lax，HTTPS 时 Secure）；命令行与编程工具用 `Authorization: Bearer 令牌`。会话方式下的非 GET 请求要求 `Origin` 与本站一致。

**账号**
- `POST /api/v1/auth/login {name, password, workspace?}` → `200 {member, workspace, role, must_change_password}` 并设 Cookie；同名成员在多个工作空间时 `409 {error, workspaces: [名称]}`；团队注册待审时 `403 {error: "…待管理员批准"}`
- `POST /api/v1/auth/logout` → `204`
- `POST /api/v1/auth/password {old_password, new_password}` → `204`（新密码至少 10 位；成功后其他会话失效）
- `GET /api/v1/me` → `{member, display_name, workspace, role, tool, must_change_password, has_password, telegram: {username, linked}}`（会话登录时 `tool` 为 `web`）
- `GET /auth/once/{code}` → 一次性登录码（`cohive web` 生成），设 Cookie 后跳转 `/`
- `POST /api/v1/auth/web-login`（令牌）→ `{url}`

**邀请与注册**
- `GET /api/v1/invites/{code}`（公开）→ `{workspace, kind, valid}`
- `POST /api/v1/register {code, name, display_name, password}` → 个人邀请：`200 {status: "active", member}` 并设 Cookie；团队邀请：`200 {status: "pending"}`

**设备授权**
- `POST /api/v1/device/start {tools, device_name}`（公开，限频）→ `{request_id, poll_secret, url, expires_in}`；`url` 为 `https://服务器/device/{nonce}`
- `GET /api/v1/device/auth/{nonce}`（会话）→ `{device_name, tools, created_at, status, first_device}`
- `POST /api/v1/device/auth/{nonce} {approve}`（会话）→ `{status}`：`approved`（首台）、`awaiting_admin`、`denied`
- `POST /api/v1/device/poll {request_id, poll_secret}` → `{status, member, workspace, device_id, tokens}`；`tokens` 只在交付那一次出现
- `POST /api/v1/devices/self/tools {tools}`（令牌）→ 在本设备补签其他工具的令牌

**个人中心**
- `GET /api/v1/me/devices` → `{items: [{id, name, created_at, revoked_at, tools}]}`
- `POST /api/v1/me/devices/{id}/revoke` → `204`
- `POST /api/v1/me/links {provider: "telegram"}` → `{id, url}`（`https://t.me/机器人?start=ln_xxx`；未启用机器人时 `503`）
- `GET /api/v1/me/links/{id}` → `{status, detail}`
- `DELETE /api/v1/me/links/telegram` → `204`

**管理（仅账号密码登录的网页会话，须为本工作空间管理员；编程工具令牌与 cohive web 免密会话一律 403，返回 `need_password_login: true`。2026-10-16 lee 定）**
- `GET /api/v1/admin/pending` → `{signups: [{id, name, display_name, created_at}], devices: [{id, member, device_name, tools, created_at}]}`
- `POST /api/v1/admin/signups/{id} {approve}`、`POST /api/v1/admin/device-auths/{id} {approve}` → `204`
- `GET /api/v1/admin/members` → `{items: [{id, name, display_name, role, telegram_username, telegram_linked, devices, has_password}]}`
- `POST /api/v1/admin/members/{id} {role?}` → `204`；`DELETE /api/v1/admin/members/{id}/telegram` → `204`
- `GET /api/v1/admin/devices` → `{items: [{id, member, name, created_at, revoked_at, tools}]}`；`POST /api/v1/admin/devices/{id}/revoke` → `204`
- `GET /api/v1/admin/invites` → `{items: [{id, kind, role, max_uses, uses, expires_at, revoked_at, created_at}]}`
- `POST /api/v1/admin/invites {kind, role?, max_uses?, ttl_hours?}` → `{id, code, url}`（`url` 只在这里出现一次）
- `POST /api/v1/admin/invites/{id}/revoke` → `204`
- `GET /api/v1/admin/workspace` → `{name, public_url, telegram_chat, show_title, show_brief, bot_enabled}`；`POST /api/v1/admin/workspace {public_url, telegram_chat, show_title, show_brief}` → `204`

**网页路由**：`/login`、`/i/{code}`（注册）、`/device/{nonce}`（授权）、`/me`（个人中心）、`/admin`（管理：待审、成员、设备、邀请、工作空间），原有看板与问题单页面保留。`must_change_password` 为真时任何页面都先跳到改密码。

## 命令行

- `cohive login [--server 地址] [--tools claude-code,codex]`：终端发起，打开浏览器授权页（同时显示链接与二维码），轮询；等待管理员批准时最多等 10 分钟，之后 `cohive login --resume` 领取。已配对的电脑补登录其他工具直接签发。`--token-stdin` 保留。
- `cohive web`：免密打开网页。
- `cohive-server admin reset-password --name 成员名 [--workspace]`：重置密码并在终端显示一次。

## 机器人

保留 `/chatid`、`/whoami`；`/start ln_xxx` 完成通讯工具连接；群里为待审注册与设备申请发审批按钮（按点击者已连接的 Telegram 账号校验管理员）。去掉 `/join`、`/login`、确认登录。

## 任务

1. 迁移、存储层、业务层（账号与会话、初始管理员、邀请与注册、设备授权、通讯连接）。
2. 接口与网页会话鉴权。
3. 网页：登录、注册、设备授权、个人中心、管理页。
4. 机器人与命令行。
5. 文档与真实环境实测（测试库需先重置第六号迁移，届时确认）。
