# 第一阶段·里程碑六 d：对外地址改为服务器级、工作空间拆路径与显示名 实现计划

> **给执行者：** 本会话自己实施，最后整条分支交一次独立审查。步骤用 `- [ ]` 勾选。

**目标：** 对外地址只设一个、挂在服务器上（参照 GitLab 的 `external_url`）：服务器管理员在“服务器管理 → 设置”填写，环境变量可以锁定；群消息等服务端主动生成的链接一律用“对外地址 + /w/路径”拼。工作空间拆成不可改的“路径”（现有名字）和可随时改的“显示名”：给人看的地方用显示名，链接和项目配置用路径。两类修改都写管理操作记录。

**依据：**
- `docs/brain/decisions.md`“对外地址与工作空间名称”（2026-10-17 lee 定，台账 D12 选 A）。
- 主脑派活（2026-10-17）。

**前提（main `6b0d657`，已含 M6-c）：**
- 对外地址现在挂在工作空间上（`workspaces.public_url`，第三号迁移），只用在一处：`notify.Render` 拼群消息末尾的问题单链接。工作空间设置页的输入框把当前访问地址当占位文字，看起来像已经填了，实际是空的（主脑提到的误导）。
- 邀请链接、设备授权页、`cohive web` 登录链接、安装脚本都按请求拼地址（`httpapi.Origin(r)` 或前端的 `window.location`），这次不动。
- MCP 的主机名白名单（`mcpserver.hostGuard`）只在启动时从 `COHIVE_ALLOWED_HOSTS` 读一次。
- 管理操作记录已为本里程碑预留两个动作：`server.settings`、`workspace.display_name`（`core.ReservedActions`）。
- M6-e（另一个会话）在改 `cmd/cohive/init.go` 并新增提醒接口。本里程碑不碰 `cmd/cohive/`；`internal/httpapi` 只新增文件或做局部修改。

**不在本里程碑：** 修改工作空间路径；对外地址带路径前缀（例如 `https://example.com/cohive`）；邀请、授权等按请求生成的链接改用对外地址；命令行 `cohive`（成员本机）显示显示名。

## 全局约束

- 只新增迁移 `0009_external_url.sql`，不改写 0001–0008。
- 对外地址只允许 `http://` 或 `https://` 加主机名、可带端口，不带路径、查询参数、片段和用户信息。保存时规范化：协议与主机名转小写，去掉末尾的 `/`。
- 环境变量 `COHIVE_EXTERNAL_URL` 设了就以它为准：网页上只读，并标明“由环境变量锁定”；校验规则相同，不合法时服务启动失败并报清楚的错。
- 显示名：去掉首尾空白后最多 40 个字符，不能含控制字符；留空表示与路径相同。显示名不要求唯一。
- 管理操作只认密码登录的网页会话（沿用 D7）；两类修改都在业务事务里写记录，没有实际变化不写。
- 测试口径：`go vet ./... && go test -count=1 ./... && make build`，真实 Postgres，关键判定做变异验证。
- 本机走查用 `127.0.0.1:8791`，新建库 `cohive_m6d_try`。

## 审查重点（规格没明说、最容易咬到人的情况）

1. **对外地址改了之后，MCP 白名单要立刻跟上**：不用重启服务，新主机名就能用；旧主机名不再自动放行（`COHIVE_ALLOWED_HOSTS` 里写的照旧有效）。→ T2 用例。
2. **没设对外地址**：群消息不带链接，其余照常。工作空间设置页提示“请服务器管理员到 服务器管理 → 设置 里填写”；如果看页面的人自己就是服务器管理员，直接给出入口。→ T2、T4 用例。
3. **迁移时旧值不规范**：带路径、末尾带 `/`、大小写混杂、根本不是网址。规范化后能用的就提升到服务器级，不能用的丢弃，并在迁移日志里提示。→ T1 迁移用例。
4. **显示名里有 HTML 或 Telegram 特殊字符**（`<b>`、`&`、`@`）：群消息里转义，网页上按纯文本显示。→ T3 用例。
5. **两个工作空间显示名相同**：顶栏切换、服务器管理列表里，显示名与路径不同时一并显示路径，避免分不清。→ T4 用例。

---

## 1. 数据模型（迁移 `0009_external_url.sql`）

```sql
-- 服务器级设置：只有一行
CREATE TABLE server_settings (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    external_url  text NOT NULL DEFAULT '',     -- 空表示没设
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES accounts(id)
);
INSERT INTO server_settings (external_url)
SELECT coalesce((
    SELECT lower(m[1]) || lower(m[2])
    FROM workspaces, regexp_match(trim(public_url), '^(https?://)([^/?#@\s]+)/?$', 'i') AS m
    WHERE trim(public_url) <> '' ORDER BY id LIMIT 1), '');
-- 有非空值但没能提升（格式不对）时 RAISE NOTICE 写进迁移日志

ALTER TABLE workspaces ADD COLUMN display_name text NOT NULL DEFAULT '';  -- 空表示与路径相同
ALTER TABLE workspaces DROP COLUMN public_url;                             -- 见待拍板 1
```

- **“第一个非空”的含义**：按工作空间编号最小的那个。它的值带路径（例如 `https://x.com/cohive`）时，提升会丢掉路径。我的倾向是丢掉路径后仍然提升，并写 NOTICE（见待拍板 3）。上面的骨架是严格版（带路径的直接跳过），实施时按拍板结果二选一。
- `server_settings` 只有一行，`CHECK (id)` 保证不会多出第二行。

## 2. 对外地址

### 2.1 读取与缓存（`internal/core/settings.go`）

```go
// ExternalURL 是生效的对外地址；Locked 表示由环境变量锁定。
type ExternalURL struct {
	URL    string // 空表示没设
	Locked bool
}
func NormalizeExternalURL(raw string) (string, error)                     // 校验与规范化；空串合法（表示清除）
func (s *Service) ExternalURL(ctx context.Context) (ExternalURL, error)    // 优先环境变量，其次数据库；结果缓存在进程里
func (s *Service) ExternalHost() string                                   // 缓存里对外地址的主机名（不含端口），给 MCP 白名单用
func (s *Service) SetExternalURL(ctx context.Context, act Actor, raw string) (ExternalURL, error)
```

- `Service` 新增字段 `EnvExternalURL string`，由 `serve` 从 `COHIVE_EXTERNAL_URL` 读入；启动时先规范化，不合法就报错退出。
- 缓存放在 `Service` 的 `atomic.Pointer` 里：启动时加载，`SetExternalURL` 提交成功后更新。只有一个服务进程，不需要跨进程失效。`Service` 现在到处用 `&core.Service{Pool: …}` 直接构造，所以缓存为空时现查数据库，零值也能用。
- `SetExternalURL`：要求服务器管理员；锁定时报“对外地址由环境变量 COHIVE_EXTERNAL_URL 锁定，请改环境变量后重启”；值没变不写；变了就写 `server.settings` 记录，附加信息 `{"external_url": {"from", "to"}}`。

### 2.2 使用处

| 地方 | 现在 | 改为 |
|---|---|---|
| `notify.Render`（群消息末尾的问题单链接） | `ws.PublicURL + /w/ws.Name/issues/编号` | `Render(base string, ws, is, n, members)`：base 为空不带链接，否则 `base + /w/路径/issues/编号`；投递时向 `core` 取 base |
| MCP 主机名白名单 | 启动时读 `COHIVE_ALLOWED_HOSTS` | `mcpserver.Protected(svc, extra…)`：除 localhost 和 `COHIVE_ALLOWED_HOSTS` 外，每次请求再比对 `svc.ExternalHost()`（读内存缓存，不查库） |
| 设备、注册审批等机器人消息 | 不带链接 | 不变（只改用显示名，见第 3 节） |
| 邀请链接、授权页、安装脚本、`cohive web` | 按请求 | 不变 |

拦截提示改为“如经隧道访问，请在 服务器管理 → 设置 里填写对外地址，或把该域名加入 COHIVE_ALLOWED_HOSTS”。

### 2.3 接口

| 路径 | 说明 |
|---|---|
| `GET /api/v1/server/settings` | `{"external_url": "…", "locked": false, "suggested": "<本次请求的地址>"}`；只给服务器管理员，`srv()` 包装 |
| `POST /api/v1/server/settings` | `{"external_url": "…"}`；锁定时 409，格式不对时 400 |
| `GET /api/v1/w/{ws}/admin/workspace` | 去掉 `public_url`；新增 `path`、`display_name`、`issue_link_example`（例如 `https://c.example.com/w/pay/issues/编号`，没设时为空）、`external_url_set` |
| `POST /api/v1/w/{ws}/admin/workspace` | 去掉 `public_url`；新增 `display_name`。还带 `public_url` 的旧请求忽略这个字段 |

### 2.4 网页

- **服务器管理新增“设置”一栏**：
  - 对外地址输入框。没设时预填 `suggested`（当前访问地址），并用醒目的文字说明“尚未保存：确认这是同事访问用的地址后点保存”，不再拿占位文字冒充已填写。
  - 锁定时只读，显示“由环境变量 COHIVE_EXTERNAL_URL 锁定”。
  - 说明用途：群消息里的链接、编程工具经隧道访问 MCP 时的主机名。
- **工作空间设置页**：
  - 去掉公网地址输入框，改为只读一行：“群消息里的问题单链接：`https://…/w/路径/issues/编号`”。
  - 没设对外地址时显示提示；看页面的人是服务器管理员时附上“去设置”的链接。
  - 新增“显示名”输入框，标明“留空则显示路径 `pay`；路径出现在链接与项目配置里，不能修改”。

### 2.5 服务器命令行

- `cohive-server server set --external-url <地址>`（`--external-url ""` 清除）与 `server show`。环境变量锁定时 `set` 报错。
- `cohive-server workspace set --name <路径> --display <显示名>`（`--display ""` 恢复为路径）；`workspace add` 增加可选的 `--display`。
- `workspace notify` 去掉 `--public-url`，用到时报“对外地址已改为服务器级：用 server set --external-url”。
- `workspace list` 原来最后一列显示公网地址，改为显示显示名。

## 3. 显示名

- `store.Workspace` 新增 `DisplayName`（库里的原值，可能为空）和方法 `Label()`（为空时返回路径）。全局搜一遍 `Name`：链接、配置、日志、匹配用路径；给人看的地方用 `Label()`。
- `core.SetWorkspaceDisplayName(ctx, act, wsID, name)`：权限 `canAdmin`；校验见全局约束；值没变不写；变了就写 `workspace.display_name` 记录，附加信息 `{"display_name": {"from", "to"}}`。建工作空间时可以一并给显示名（网页表单与命令行 `workspace add --display`）。
- 用显示名的地方：
  - 网页：顶栏切换器、管理页标题、服务器管理的工作空间列表、个人中心的成员资格、注册与接受邀请页的“加入 X”、设备授权页的“批准后可用于”。
  - 机器人消息：申请、批准、加入通知、`/whoami`。
  - 群消息正文现在不显示工作空间名，不加。
- 接口：凡是返回工作空间的地方都带 `display_name`（已经是路径的就填路径），包括 `/me` 的 `workspaces`、`/server/workspaces`、邀请信息、设备授权页、接受邀请与注册的结果。前端统一用 `workspaceLabel(w)` 显示：显示名与路径不同时，在切换器和服务器列表里写成“显示名（路径）”。
- 不改的：MCP 工具输出、`cohive` 命令行、项目配置里写的都是路径，智能体与配置只认路径。

## 4. 管理操作记录

- 从 `core.ReservedActions` 删掉 `server.settings` 与 `workspace.display_name`，补业务用例。`workspace.settings` 的附加信息里不再出现 `public_url`（旧记录照常显示，中文名保留）。
- 网页入口表新增 `POST /api/v1/server/settings`、`POST /api/v1/w/{ws}/admin/workspace`（显示名）；命令行表新增 `server set`、`workspace set`；只读名单新增 `server show`。

## 5. 任务

每项完成即 commit + push `feature/phase1-m6d-external-url`。

### T1：迁移与存储
- [ ] 迁移测试 `TestMigrate0009`：在 0008 结构上造几种旧值——编号最小的为空、第二个是 `HTTPS://Cohive.Example.com/`、第三个另有值；另造“只有带路径的值”“只有不合法的值”两组——迁移后断言：提升了 `https://cohive.example.com`；带路径与不合法两组的结果按待拍板 3 定；`public_url` 列按待拍板 1 处理；`display_name` 为空。
- [ ] 存储层：读写 `server_settings`、`SetWorkspaceDisplayName`，扫描 `display_name` 列。
- [ ] commit：`feat(M6d-T1): 服务器级对外地址与工作空间显示名的迁移与存储`

### T2：对外地址的业务、接口、MCP 白名单、群消息
- [ ] 用例：
  - `NormalizeExternalURL` 表驱动（合法、带路径、带查询参数、带用户信息、`ftp://`、空格、大写、末尾 `/`）。
  - 环境变量锁定时网页保存报 409，返回 `locked`。
  - 非服务器管理员 403，并留下被拒记录。
  - 值没变不写记录。
  - 改了之后 MCP 白名单立刻放行新主机名，不用重启；旧主机名被拒。
  - 群消息里的链接改用对外地址加路径；没设时不带链接。
  - 启动时环境变量不合法就报错。
- [ ] commit：`feat(M6d-T2): 对外地址改为服务器级（环境变量可锁定），群消息链接与 MCP 主机名白名单跟随`

### T3：显示名
- [ ] 用例：
  - 业务层校验：超长、控制字符、留空恢复为路径、值没变不写记录。
  - 权限：工作空间管理员可以改本空间的，成员不行。
  - 记录与人话。
  - 机器人消息用显示名，并且转义 `<b>&` 等字符。
  - `/me` 等接口带 `display_name`。
  - 建工作空间时可以一并给显示名。
- [ ] commit：`feat(M6d-T3): 工作空间显示名（路径不可改），给人看的地方用显示名`

### T4：网页与命令行
- [ ] vitest：
  - 服务器“设置”一栏：没设时预填当前地址并提示“尚未保存”；锁定时只读。
  - 工作空间设置页：没有公网地址输入框，只读显示链接，没设时有提示。
  - 显示名输入。
  - 切换器与服务器列表显示“显示名（路径）”。
  - 显示名按纯文本渲染。
- [ ] 命令行：`server set|show`、`workspace set --display`、`workspace add --display`、`workspace notify --public-url` 的报错；表驱动与“每条命令都归了类”的检查同步更新。
- [ ] commit：`feat(M6d-T4): 服务器设置页、工作空间设置页只读链接与显示名、命令行`

### T5：文档、走查、变异
- [ ] 文档：
  - `docs/guides/test-server.md`：环境变量 `COHIVE_EXTERNAL_URL`，以及测试服切换时对外地址的检查。
  - `onboarding.md`：服务器管理员首次要设对外地址。
  - `group-notify.md`：链接从哪来。
- [ ] 走查：8791 端口、库 `cohive_m6d_try`。
- [ ] 变异：
  - M1 Render 仍用旧字段或不拼路径
  - M2 白名单不跟随缓存
  - M3 锁定时仍可保存
  - M4 校验放过带路径的地址
  - M5 显示名不转义
  - M6 显示名改了不写记录
  - M7 没变也写记录
  - M8 迁移不规范化
- [ ] 全量验证，回报主脑。

## 6. 待拍板

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| 1 | **`workspaces.public_url` 删列还是保留只读** | A 本迁移直接删列；B 保留一个版本，代码不再读写，下个里程碑再删 | **A**。留着的唯一好处是能把测试服回退到旧程序；而 0008、0009 都不向后兼容，回退本来就要用迁移前的备份。删掉就不会有人再误读它 |
| 2 | **首次预填当前访问地址：只预填，还是第一次有服务器管理员访问时自动保存** | A 只预填，要人点保存；B 自动保存 | **A**。自动保存可能把 `localhost` 或内网地址存成对外地址，群消息里的链接就错了；预填只省掉打字，人一定要看一眼 |
| 3 | **迁移时旧值带路径（如 `https://x.com/cohive`）** | A 去掉路径后提升，写 NOTICE；B 不提升，写 NOTICE，由人到设置页填 | **A**。去掉路径后主机名对了，链接就能用；本来就不支持路径前缀部署，带路径的值几乎一定是填错了。完全不像网址的值丢弃，写 NOTICE |
| 4 | **显示名要不要唯一** | A 不要求；B 全服务器唯一 | **A**。GitLab 的 name 也不唯一；会混淆的地方（切换器、服务器列表）同时显示路径就够了 |
| 5 | **服务器命令行要不要能改对外地址** | A 要：`server set --external-url`；B 不要，只用环境变量 | **A**。“命令行只做服务器上的兜底”，环境变量要重启才生效；命令行改的也写记录 |

除非主脑另行指示，待拍板全部按建议做。
