# 第一阶段·里程碑六 a：试用前补齐（命令行分发、写入前密钥扫描、评论不让确认作废、测试服脚本与备份）实现计划

**目标：** 让一位真实同事能在测试服上用起来。补齐四个缺口：同事电脑上没有 Go、读不到私有仓库也能一条命令装上与服务端版本一致的 `cohive`；问题单正文入库前扫描密钥，命中即拒绝；评论不再让人刚批准的发出、接单、关闭因版本冲突白做；测试服的启动脚本与数据库备份收进仓库。

**依据：**
- `docs/brain/decisions.md`“部署与发布”（单个可执行文件或 Docker 镜像、团队自行部署、开源倾向 Apache-2.0）、“人工确认”（改变状态的工具带 `expected_version` 与幂等键）、“路线”（M6-a 四项）。
- `docs/brain/requirements.md` 第 3.3 节（R5 同事装不上命令行、R6 密钥会进库、R7 测试服是一台笔记本、R9 评论让确认作废）、第 5 节顺序 2。
- `docs/plan.md` 第 8 节（命令行“需提供预编译版本”；审计“写入前用 gitleaks 规则扫描密钥”）、第 9 节安全红线第 5 条。

**不在本里程碑：** Docker 镜像与 compose（M6-b）；程序签名与公证；`cohive` 自动升级；本机转发器预扫密钥（见待拍板 6）；对已入库数据补扫；替换或重启正在运行的测试服（切换由主脑做，本文第 4.4 节给出步骤）。

**约束：** 只新增迁移，不改写 0001–0007（本里程碑预计不需要新迁移）；测试用真实 Postgres；不碰测试服进程与库 `cohive_test`、`cohive`、`cohive_m5`、`cohive_m5b_dev`、`cohive_m5b_try`；本机走查另起端口（127.0.0.1:8789）、另建库；命令行走查用 `COHIVE_CONFIG_DIR` 指向临时目录。

---

## 1. `cohive` 命令行分发

### 1.1 方案比较

| | A 协作服务自带安装包（嵌入服务端程序） | B 协作服务从目录提供安装包 | C 代码平台发布页 |
|---|---|---|---|
| 同事要什么 | 能打开协作服务网址 | 同左 | 能读仓库发布页 |
| 私有仓库 | 不受影响 | 不受影响 | **不可用**：同事读不到私有仓库的发布页 |
| 版本一致 | 天然一致：同一次构建产出，打进同一个服务端程序 | 靠部署时别忘了同步目录 | 各团队自部署的服务端版本各不相同，要自己对 |
| 部署形态 | 仍是一个可执行文件（符合 decisions“一个可执行文件或一个 Docker 镜像”） | 可执行文件 + 一个目录 | 与服务端无关 |
| 体积 | 服务端程序增加约 14 MB（五个平台压缩后，见下） | 服务端不变，目录约 14 MB | — |
| 自部署友好 | 好：无需外网，内网服务器也能装 | 好 | 依赖外网与代码平台 |

**实测体积**（`CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/cohive`，main `fc33a7b`）：

| 平台 | 原始 | gzip -9 |
|---|---|---|
| macOS arm64 | 6.5 MB | 2.7 MB |
| macOS amd64 | 7.0 MB | 2.9 MB |
| Linux amd64 | 6.8 MB | 2.9 MB |
| Linux arm64 | 6.4 MB | 2.6 MB |
| Windows amd64 | 7.0 MB | 2.9 MB |

**选 A。** 理由：版本天然一致、不依赖仓库可见性、内网也能装、仍是单个可执行文件。代价是服务端程序大约 14 MB，对自部署可以接受。以后仓库公开、要做代码平台发布页时，同一套构建产物直接上传即可，不冲突。

### 1.2 构建

- 新增 `internal/buildinfo`：`Version`、`Commit` 两个变量，构建时用 `-ldflags -X` 注入；`mcpserver.Version` 改为取 `buildinfo.Version`。版本取 `git describe --tags --always --dirty`，没有标签时就是提交号（带 `-dirty` 表示有未提交改动）。
- 新增 `internal/clidist`：`//go:embed all:files`。`files/` 下只提交占位文件 `.gitkeep`，构建产物进 `.gitignore`（与 `web/dist` 同样做法）。
- `make cli-dist`：交叉编译五个平台（`CGO_ENABLED=0 -trimpath -ldflags '-s -w -X …'`），每个文件 `gzip -9 -n`（不写时间戳，构建可复现）后放进 `internal/clidist/files/`，并写 `manifest.json`：
  ```json
  {"version": "…", "commit": "…",
   "files": {"darwin-arm64": {"name": "cohive", "gz": "cohive-darwin-arm64.gz", "size": 6490594, "sha256": "<原始文件的 SHA-256>"}, …}}
  ```
- `make build` 依次执行 `web`、`cli-dist`、构建服务端，三者用同一个版本号。`make cli`（本机单平台）保留，供开发用。
- 没有执行 `make cli-dist` 的构建（例如 `go build ./cmd/cohive-server`、`go test`）照常能编译；这时下载接口回复“这台服务器没有附带命令行安装包”，安装脚本打印同样的话并以非零退出。

### 1.3 接口（公开，不需登录；见待拍板 1）

| 路径 | 返回 |
|---|---|
| `GET /install.sh` | macOS、Linux 安装脚本（`text/plain; charset=utf-8`，`Cache-Control: no-cache`）。服务端渲染时写入本站地址（取自本次请求，同 `origin(r)`）、版本号与五个平台的校验和 |
| `GET /install.ps1` | Windows PowerShell 安装脚本，内容同理 |
| `GET /dl/cohive/{os}-{arch}` | 原始可执行文件（服务端边解压边发，`Content-Length` 取自清单，`Content-Disposition: attachment`）；未知平台或无安装包 `404 {"error": "…"}` |
| `GET /dl/cohive/SHA256SUMS` | 五个平台的校验和，格式同 `sha256sum` 输出，方便手动核对 |
| `GET /api/v1/version` | `{"version": "…", "commit": "…", "cli": {"version": "…", "platforms": ["darwin-arm64", …]}}`；无安装包时 `cli` 为 `null` |

这些路由注册在看板的兜底路由之前；`/dl/` 下缺失的文件返回 404，不回落到 `index.html`。服务启动时打印一行“命令行安装包：版本 X，5 个平台”或“未附带命令行安装包”；安装包版本与服务端版本不一致时（理论上不会发生）打印警告。

### 1.4 安装脚本行为

**macOS、Linux**（`curl -fsSL https://服务地址/install.sh | sh`，POSIX sh，不依赖 bash）：
1. `uname -s`、`uname -m` 映射到 `darwin|linux` × `arm64|amd64`（`aarch64` 视为 `arm64`，`x86_64` 视为 `amd64`）；其他平台报错退出。
2. 只从**提供脚本的同一服务地址**下载 `/dl/cohive/{os}-{arch}` 到临时目录（`mktemp -d`，退出时清理）。
3. 用 `shasum -a 256`（macOS）或 `sha256sum`（Linux）校验，与脚本里写死的校验和不符则删除并报错退出，**不安装**。
4. 安装到 `${COHIVE_INSTALL_DIR:-$HOME/.local/bin}/cohive`（`chmod 755`，先写临时文件再改名，覆盖旧版是原子的）。不用 `sudo`，不改 shell 配置文件；目录不在 `PATH` 里时打印要加的那一行（见待拍板 2）。
5. 打印已安装版本（运行 `cohive version`）和下一步：`cohive login --server https://服务地址`。
6. 服务地址是 `http://` 时打印提醒“未加密，仅限内网”。

**Windows**（`irm https://服务地址/install.ps1 | iex`）：下载到 `%LOCALAPPDATA%\Programs\cohive\cohive.exe`，`Get-FileHash -Algorithm SHA256` 校验，不在用户 `PATH` 里时加入用户级 `PATH`（不需要管理员权限），打印下一步。**本机无法实测 Windows，只做内容检查**，作为遗留项交给第一位 Windows 同事验证。

**macOS 不弹“无法验证开发者”：** 用 `curl` 下载的文件不带隔离属性，未签名的程序可以直接运行；经浏览器下载的会被拦，所以文档只推荐脚本安装。

### 1.5 安全说明（写进 `docs/guides/install-cli.md`）

- 下载只来自团队自己的协作服务；脚本里的下载地址由服务端按本次请求的地址写入，不会指向别处。
- 校验和与程序来自同一台服务器：它能发现传输中损坏、下错平台、被中途截断，**不能**防“服务器本身被攻破”。真正的来源认证要靠签名（cosign、minisign 或系统签名），等仓库公开发布时再做（见待拍板 3）。
- 只经 HTTPS 访问时，传输由 TLS 保护；`http://` 只在内网用。
- 不放心 `curl | sh` 的人可以先下载再看：`curl -fsSLo install.sh https://服务地址/install.sh && less install.sh && sh install.sh`。

### 1.6 版本一致性检查

- `cohive version`：打印本机版本与提交号。
- `cohive login`、`cohive status` 访问服务端时顺带读 `/api/v1/version`；两边都不是开发构建（`dev`）且版本不同时，打印一行提醒和重装命令。只提醒，不阻断（升级窗口里旧版命令行仍应能用）。
- `cohive mcp-proxy` 不做检查（它的输出是给编程工具的，不宜混入提示）。

### 1.7 网页

个人中心“绑定新电脑”改为三步：
1. 安装命令行：macOS / Linux 一行、Windows 一行（可复制），下面一句话说明“只从本服务下载、装到哪里、会核对校验和”。
2. 配对：原有 `cohive login --server …`。
3. 已配对的电脑执行 `cohive web` 免密打开网页（原文保留）。

服务端没有附带安装包时（`/api/v1/version` 的 `cli` 为 `null`），第 1 步改为提示“这台服务器没有附带命令行安装包，请联系管理员”。

### 1.8 一个框架外的问题：编程工具找不到 `cohive`

`cohive init` 写进项目配置的命令是 `cohive`（项目配置要提交进仓库，不能写某个人的绝对路径）。如果编程工具不是从终端启动（例如从程序坞打开的桌面应用），它拿到的 `PATH` 可能不含 `~/.local/bin`，就会找不到转发器。本里程碑的处理：安装脚本检测到目录不在 `PATH` 时明确提示；安装说明里写“从终端启动编程工具，或把目录加入登录 shell 的 `PATH`”；`cohive init` 写完配置后检查 `cohive` 是否在当前 `PATH` 中，不在时提示。试用中若真碰到，再评估“`init --command` 写入本机的用户级配置而非项目配置”。

---

## 2. 写入前扫描密钥（安全红线第 5 条）

### 2.1 规则来源与许可

- 规则取自 **gitleaks v8.30.1 的默认规则文件 `config/gitleaks.toml`**（222 条规则，MIT 许可，版权 Zachary Rice）。MIT 与 Apache-2.0 兼容；原样嵌入，文件头加来源与版本说明，旁边放 gitleaks 的 `LICENSE` 原文。
- **不依赖 gitleaks 的 Go 包**：它的检测包会带进 viper、cobra、lipgloss、多种压缩格式库等几十个间接依赖，而 Cohive 只需要“对一段文本跑规则”。已有依赖 `BurntSushi/toml` 足够解析规则文件。
- 自写一个精简引擎（`internal/secretscan`），只实现文本扫描需要的部分，语义对齐 gitleaks 的 `detect` 包（实施时逐条对照源码）：
  - 关键词预筛（规则的 `keywords`，小写包含即进入正则）；
  - 正则匹配；密文取 `secretGroup` 指定的分组，未指定时取第一个非空分组，没有分组时取整个匹配；
  - 熵阈值（`entropy`，香农熵低于阈值不算）；gitleaks 对通用规则额外要求的条件照搬；
  - 放行规则：全局 `[allowlist]` 与规则自带的 `[[rules.allowlists]]`，支持 `regexes`（`regexTarget` 为 secret / match / line）、`stopwords`、`condition`（OR / AND）；
  - 只针对文件路径的规则与放行项（`path`、`paths`）跳过——问题单正文没有文件路径。
- 升级规则：替换文件、跑误报与命中样例测试即可；文件头记录版本。

### 2.2 Cohive 追加的放行（单独一个文件，便于审查）

只用于减少问题单场景下的常见误报，每条都有样例测试：
- 通用规则（`generic-api-key`）命中时，若键名明显是标识或摘要（`id`、`uuid`、`request_id`、`trace_id`、`commit`、`sha`、`hash`、`digest`、`checksum`、`etag` 等结尾或前缀），且值是 UUID 形状或纯十六进制，则放行；
- 明显的占位值：`<REDACTED>`、`[已脱敏]`、`***`、`xxx…`、`${VAR}`、`{{ var }}`、`changeme`、`your-api-key-here` 之类（gitleaks 全局放行已覆盖一部分，补齐中文与常见占位）。

**误报样例集**（必须不命中）：UUID、`request_id=<uuid>`、40 位提交号、`sha256:<64 位十六进制>` 镜像摘要、`go.sum` 的 `h1:…=`、npm `integrity: sha512-…`、`password: ******`、`api_key=${API_KEY}`、`Authorization: Bearer <REDACTED>`、普通 Go / Python 报错堆栈、带端口的内网地址、Cohive 问题单单号与版本号。
**命中样例集**（必须命中）：AWS 访问密钥、GitHub 个人令牌、GitLab 个人令牌、Slack 机器人令牌、Stripe 密钥、Google API 密钥、OpenAI 与 Anthropic 密钥、Telegram 机器人令牌、PEM 私钥块、JWT、`DB_PASSWORD=…` 形式的高熵赋值、Cohive 自己的令牌（`cohive_` 前缀，见下）。

**Cohive 自己的令牌**：gitleaks 没有这条。追加一条规则：`cohive_`、`cohive_dev_` 前缀加 43 位 URL 安全 base64。智能体最可能贴错的恰恰是本机凭据文件里的东西。

### 2.3 扫描范围与时机

| 入口 | 扫描的字段 |
|---|---|
| `cohive_draft_issue`（`core.Draft`） | `title`、`brief`、`summary`、`expected`、`actual`、`repro_script`、`repro_env`、`evidence`、`repo`、`branch`、`commit`、`target_service`、`owner` |
| `cohive_submit_issue`、`cohive_accept_issue`（接单、退回、转交）、`cohive_update_issue`（评论、开始修复）、`cohive_request_verification`、`cohive_close_issue`（关闭、重开）——全部经 `core.Act` | `note` |
| 管理员改派（`reassign`） | 备注是固定文字，不需要扫 |

扫描放在 `core` 层、开启事务和查幂等记录**之前**：被拒绝的请求什么都不写，用同一幂等键重试脱敏后的内容可以正常成功。网页接口目前没有写正文的入口；以后新增的写入入口必须走同一个检查函数（审查重点第 6 条）。

### 2.4 拒绝时的错误

新增 `core.SecretError`（实现 `error`，并能被 `errors.As` 识别为输入错误），经 MCP 作为工具错误返回：

> 内容里疑似有密钥，已拒绝写入，什么都没有保存：
> - evidence 第 12 行：GitHub 个人访问令牌（规则 github-pat）
> - note 第 3 行：通用密钥赋值（规则 generic-api-key）
>
> 请把这些位置的密钥换成 `[已脱敏]` 之类的占位后重试。不要把原值展示给用户，也不要写进别的字段。

- **绝不回显密钥**：不含密钥本身、不含它的前几位、不含所在行的内容；只给字段名、行号（字段内从 1 开始）、规则类别与规则编号。
- 规则类别：常见的二三十条给中文名称（AWS、GitHub、GitLab、Slack、Stripe、Google、OpenAI、Anthropic、Telegram、私钥、JWT、通用密钥、Cohive 令牌等），其余显示“密钥或令牌（规则 xxx）”。
- 同一字段同一行同一规则只报一次；最多列 10 处，其余写“另有 N 处”。
- 服务端日志不记录请求内容；这条错误也不写进日志。
- 工具说明里加一句：“写入前会扫描密钥，命中会拒绝并指出字段与行号，请脱敏后重试。”

**服务端不静默改写内容**（派活要求）：不替智能体打码，避免“看起来存进去了、其实内容被改了”。

### 2.5 不提供“标记放行”

gitleaks 支持在行内写 `gitleaks:allow` 跳过检查。Cohive 不提供同类机制：智能体碰到拒绝时最省事的做法就是加标记，等于没扫（见待拍板 4）。真误报时智能体改写那一行（例如把值换成占位、只描述“是一串 40 位十六进制”）即可。

---

## 3. 评论不让已批准的确认作废

### 3.1 现状

`cohive_update_issue` 只补充说明时走 `Act(comment)`，与其他动作一样调用 `store.UpdateIssue`，`version` 加一。人在编程工具里批准“发出 / 接单 / 关闭”时，工具调用里的 `expected_version` 是批准那一刻看到的版本；只要这期间任何一方补了一条评论，批准过的调用就会因版本冲突失败，人要重新读、重新批准。

### 3.2 做法：收窄 `version` 的含义（放宽规则，不拆计数）

- `version` 改为“状态、归属与正文的版本”：只有改变状态、负责人、发起人或正文字段的写入才加一。评论只追加事件，不加一。
- 评论**不比对** `expected_version`：评论只追加，不存在“基于旧状态覆盖别人的修改”的问题。`cohive_update_issue` 只补充说明时 `expected_version` 改为可不填；填了也不比对（见待拍板 5）。`start_fixing` 是状态变更，照旧必填并比对。
- 评论仍在行锁（`FOR UPDATE`）下按**当前**状态与角色判定权限：例如已转交出去的原负责人再评论会被拒绝，与现在一致。
- 评论只刷新 `updated_at`（新增 `store.TouchIssue`），收件箱排序与“7 天内被退回”的窗口照旧随评论更新。
- 幂等照旧：同一幂等键重试评论返回同一结果，不重复写事件。
- 其余改变状态的动作（发出、接单、退回、转交、开始修复、请求验证、关闭、重开）与管理员改派照旧：行锁 + 版本比对 + `UPDATE … WHERE version = $2`，真正的并发状态冲突仍被拦住。
- 不需要新迁移；已有数据里过去评论加过的版本号保持原样，不回改。
- 拆分计数（新增 `comment_seq` 之类）的方案不选：多一个字段却没有调用方会传它；收窄含义已经足够。

### 3.3 并发用例（真实 Postgres，多协程同时起跑）

1. 待接单、版本 v：负责人评论与负责人接单（`expected_version = v`）同时发起，重复 20 轮：两者都成功，结束时状态“已接单”、版本 v+1、两条事件都在。
2. 顺序回归：读到版本 v → 对方评论 → 用 v 接单成功（这是本项要修的问题本身）。
3. 真冲突：同一版本下负责人同时“接单”与“退回”，恰好一个成功、另一个得到版本冲突；发起人同时“关闭”与“重开”同理；管理员改派与负责人接单同时发起，恰好一个成功。
4. 评论不填版本号成功；转交后原负责人评论被拒绝。
5. 评论的幂等重试不重复写事件，也不改版本。

---

## 4. 测试服启动脚本与数据库备份

### 4.1 目录约定（可用环境变量覆盖）

```
${COHIVE_HOME:-$HOME/.local/share/cohive-server}/
  server.env        非秘密配置：数据库地址、监听地址、允许的域名、程序路径、备份参数（权限 600）
  data/             COHIVE_DATA_DIR：初始管理员密码文件等
  logs/server.log   服务输出（追加写；启动脚本不打印环境变量）
  run/server.pid    启动脚本写的进程号
  backups/          COHIVE_BACKUP_DIR 缺省位置
```

不用 macOS 的 `~/Library/Application Support`：路径带空格，手敲命令容易出错；Linux 上同样适用。

### 4.2 `scripts/cohive-server.sh start|stop|restart|status|run`

- 读 `server.env`（不存在时用环境变量；缺 `COHIVE_DATABASE_URL` 报错）。机器人令牌沿用 `~/.config/cohive/telegram.env` 的加载方式：只在脚本进程里 `source` 并导出，不打印、不写进日志、不进 `server.env`。
- `start`：已在运行（进程号文件里的进程还活着且确是 `cohive-server`）则提示并退出；监听端口已被别的进程占用则打印占用者并退出，**不杀**；否则后台启动 `cohive-server serve --addr …`（`serve` 自带迁移），写进程号文件，轮询 `/healthz` 最多 15 秒，失败时打印日志末尾 20 行。
- `stop`：只停进程号文件里记录、且命令名确是 `cohive-server` 的进程；先 `TERM`，10 秒不退再提示，不自动 `KILL`。
- `run`：前台运行（供 launchd、systemd 之类托管时用）。
- `status`：是否在运行、进程号、版本（`/api/v1/version`）、数据目录。
- 现有 `scripts/run-server.sh` 保留不动（文档里有引用），说明里注明新脚本是测试服推荐用法。

### 4.3 `scripts/backup-db.sh`

- `pg_dump -Fc` 写到 `backups/cohive-<库名>-YYYYmmdd-HHMMSS.dump`：先写 `.partial`，`pg_restore --list` 校验能读后再改名，失败不留半截文件。
- 保留最近 `COHIVE_BACKUP_KEEP`（缺省 14）份：**只在本次备份成功后**删除更旧的，且只删匹配 `cohive-<库名>-*.dump` 的文件。
- **版本陷阱（实查）：** 本机 Homebrew 的 `pg_dump` 是 15，开发库容器是 Postgres 17，`pg_dump` 会拒绝备份更新版本的服务器。脚本先比较两边主版本；不一致时，若设置了 `COHIVE_PG_CONTAINER`（例如 `cohive-dev-postgres`）就改用 `docker exec` 在容器里跑同版本的 `pg_dump`（连接参数从数据库地址解析，容器内连 `127.0.0.1:5432`），否则报错并给出两种解决办法（装同版本客户端，或设置容器名）。
- 密码只经环境变量 `PGPASSWORD` 传给 `pg_dump`，不出现在进程参数里，也不打印。
- 退出码：成功 0，失败非 0，便于 launchd 记录。

### 4.4 macOS 定时备份与切换步骤（写进 `docs/guides/test-server.md`）

- `scripts/launchd/cohive-backup.plist.template`：`StartCalendarInterval` 每天 03:30，标准输出与错误写到 `logs/backup.log`；文档给出渲染路径、`launchctl bootstrap gui/$(id -u) …`、`launchctl kickstart` 立即试跑、`launchctl bootout` 撤销。**本会话不安装到 lee 的系统里**，只在临时目录渲染并用 `plutil -lint` 检查格式。
- 说明 launchd 的行为：电脑睡眠错过的定时任务会在唤醒后补跑一次；关机期间错过的不补。
- 恢复：一律恢复到**新库**（`createdb` + `pg_restore -d 新库`），核对后再切换，不覆盖原库。
- 切换步骤（主脑执行）：停旧测试服 → 把现数据目录的文件复制到 `~/.local/share/cohive-server/data/` → 写 `server.env`（库 `cohive_test`、`127.0.0.1:8787`、允许域名、容器名）→ `scripts/cohive-server.sh start` → 先手动跑一次备份 → 再装定时任务。

---

## 5. 接口与文件清单

| 位置 | 改动 |
|---|---|
| `internal/buildinfo/` | 新增：`Version`、`Commit` |
| `internal/clidist/` | 新增：嵌入安装包、读清单、`Open(os, arch)` 解压读取、渲染两个安装脚本（`text/template`） |
| `internal/server/server.go` | 注册 `/install.sh`、`/install.ps1`、`/dl/cohive/…`、`/api/v1/version` |
| `internal/secretscan/` | 新增：引擎、`gitleaks/gitleaks.toml` 与 `gitleaks/LICENSE`、`cohive.go`（追加规则与放行）、中文类别表 |
| `internal/core/core.go` | `Draft`、`Act` 入口扫描；评论不比对、不加版本；`SecretError` |
| `internal/store/store.go` | `TouchIssue` |
| `internal/mcpserver/server.go` | `UpdateArgs.expected_version` 可选；工具说明补一句扫描提示 |
| `cmd/cohive/` | `version` 命令；`login`、`status` 的版本提醒；`init` 检查 `PATH` |
| `cmd/cohive-server/serve.go` | 启动时打印安装包信息 |
| `web/src/pages/Me.tsx`、`web/src/api.ts` | 安装三步 |
| `Makefile`、`.gitignore` | `VERSION`、`cli-dist`、`build` 依赖 |
| `scripts/` | `cohive-server.sh`、`backup-db.sh`、`launchd/cohive-backup.plist.template`、`server.env.example` |
| `docs/guides/` | 新增 `install-cli.md`、`test-server.md`；`onboarding.md` 接入步骤改为先装命令行 |

## 6. 任务与审查重点

### 6.1 任务（每项完成即 commit + push 分支；测试口径 `go vet ./... && go test -count=1 ./... && make build`）

| 编号 | 内容 | 验收 |
|---|---|---|
| T1 | 评论不让确认作废（第 3 节） | 3.3 五组用例 |
| T2 | 密钥扫描引擎与规则（2.1、2.2） | 命中与误报样例集；对照 gitleaks 自带的规则样例抽查 |
| T3 | 扫描接入写入入口与错误格式（2.3、2.4） | 每个入口一条拒绝用例且库里无写入；错误文本不含密钥任何片段；脱敏后同一幂等键重试成功 |
| T4 | 构建与安装包、下载接口、安装脚本、版本检查（1.2–1.6） | 下载与校验和接口测试；在本机用测试服务器跑一次 `install.sh` 端到端（装到临时目录）；篡改校验和时不安装 |
| T5 | 网页安装三步（1.7） | `pnpm test` 与构建；本机 8789 端口走查个人中心 |
| T6 | 测试服脚本与备份（第 4 节） | 本机另起 8789 端口、另建库走一遍 start / status / stop；对临时库备份、轮换、恢复到新库各一次；plist 格式检查 |
| T7 | 文档 | 三份指南互相对得上；`go vet` 等全量 |

T1 改动最小、风险独立，先做；T2、T3 次之；T4、T5 依赖构建改动；T6 与代码无关，可穿插。

### 6.2 审查重点（每条对应要写的测试）

| # | 审查点 | 测试 |
|---|---|---|
| 1 | 评论不再让版本变化，真冲突仍被拦 | 3.3 第 1–3 组 |
| 2 | 评论权限仍按行锁下的当前状态判定 | 3.3 第 4 组 |
| 3 | 密钥命中即拒绝，什么都不写 | 起草、评论、请求验证等每个入口：命中后问题单、事件、幂等记录、待发通知条数都不变 |
| 4 | 错误不回显密钥 | 断言错误文本不包含密钥本身、也不包含其任意 8 个字符的子串 |
| 5 | 误报可控 | 2.2 误报样例集全部不命中 |
| 6 | 新写入入口不会漏扫 | 测试遍历 MCP 全部写工具（按 `ReadOnlyHint=false` 枚举），每个都喂一条带密钥的正文，必须全部被拒 |
| 7 | 安装包与服务端版本一致 | `/api/v1/version` 的 `version` 与 `cli.version` 相同（构建时注入）；清单缺失时接口与脚本给出明确提示 |
| 8 | 校验失败不安装 | `install.sh` 端到端：校验和被篡改时退出码非零、目标位置没有新文件 |
| 9 | 下载只来自同一服务地址 | 渲染出的脚本里只出现本次请求的地址；请求带奇怪的 `Host` 时地址按原样转义，不能注入命令 |
| 10 | 备份脚本不误删 | 轮换只删本库、本命名规则的旧文件；备份失败时不删任何旧文件 |
| 11 | 启动脚本不误杀 | 进程号文件指向别的程序时 `stop` 拒绝动手 |

**变异验证（实施时逐条做，完工回报列出）：**
- 让评论照旧调用 `UpdateIssue` → 第 1 条的顺序回归与并发用例变红。
- 去掉状态动作的版本比对（只留 `UPDATE … WHERE version`，或两者都去）→ 真冲突用例变红。
- 去掉 `Act` 里对 `note` 的扫描 → 第 3、6 条变红。
- 去掉熵阈值判断 → 误报样例变红。
- 错误信息里拼入密钥前缀 → 第 4 条变红。
- 安装脚本跳过校验 → 第 8 条变红。

## 7. 待拍板

未拍板前实施按“建议”做。

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **下载接口与安装脚本要不要登录** | A 公开；B 需要邀请码或网页登录 | **A**。命令行不含任何秘密，不配对什么也做不了；新同事在拿到账号之前就要能装。代价：经隧道暴露在公网的服务器上，任何人都能下载这个程序——仓库虽然私有，程序本身没有秘密，项目也打算开源 |
| 2 | **安装位置与 `PATH`** | A `~/.local/bin`，不改 shell 配置，只提示；B 自动往 `~/.zshrc` 等文件追加一行；C `/usr/local/bin`（要 `sudo`） | **A**。不碰用户的配置文件、不要管理员权限；Windows 加入用户级 `PATH` 是系统常规做法，例外 |
| 3 | **校验和与签名** | A 本里程碑只做同源校验和，签名等公开发布时做；B 现在就做 minisign 签名，公钥写进文档 | **A**。现在服务器和下载者是同一个团队，签名防的“服务器被攻破”这一层对试用没有实际增益；公开发布时才有“第三方镜像、发布页”这类需要签名的场景 |
| 4 | **是否提供行内“放行”标记** | A 不提供；B 提供，但只有人在网页上才能放行 | **A**。见 2.5；B 要新做网页写入入口，试用期不值得 |
| 5 | **评论是否还比对版本号** | A 不比对，可不填（本文默认）；B 仍要求填写并比对，只是不再加一 | **A**。B 下评论会因为对方刚改了状态而失败，智能体要重读再评论，而评论本身不会覆盖任何东西 |
| 6 | **本机转发器也预扫密钥** | A 不做，只在服务端扫；B 本机 `cohive mcp-proxy` 先扫一次，命中直接在本机拒绝，密钥不出本机 | **A，记为后续项**。服务端拒绝时密钥已经到过服务端内存（不落库、不进日志），对团队自建的服务器可以接受；B 要让转发器解析 MCP 请求，代价不小。命令行与服务端版本现在已经一致，以后要做时规则同步不是问题 |
| 7 | **备份保留份数与时间** | 默认每天 03:30、保留 14 份 | 按默认；测试服数据量小，14 份约等于两周试用期 |
