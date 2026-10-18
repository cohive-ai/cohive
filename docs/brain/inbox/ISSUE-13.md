# ISSUE-13 交回：按昵称查找成员，不凭昵称静默误指派

- 分支：`feature/issue-13-member-lookup`（从 origin/main `357467e` 开出）
- 代码提交：`f2ef299`；本交回文件另起一个提交，以分支最新提交为准
- 状态：已推送分支，未合入、未推 main、未动测试服

## 做了什么

1. **新工具 `cohive_find_member`**（只读）：在调用方工作空间的在册、账号未停用成员里按登录名、昵称检索，
   不区分大小写；完全相同的排前，其次是包含匹配（用 strpos，`%`、`_` 按原样匹配）；查询词为空时列出全部。
   返回 `members[]`（`name` 登录名、`display_name`、`role`、`services` 负责的服务、`exact`）和 `truncated`；
   上限 20 条（`core.MemberSearchLimit`）。工具说明写明：用户给的不是确定的登录名时先查，多候选让用户选，指派一律传登录名。
2. **指派入口**（起草 owner、发出 assignee、转交接收人）都走 `core.assignable`，仍只接受登录名，纯登录名照旧成功。新增两种拦截，
   返回 `core.MemberChoiceError`（可本地化，列出“登录名 + 昵称”候选）：
   - `member_nickname`：传入值不是任何人的登录名，但等于某些成员的昵称，或大小写不同的登录名；
   - `member_ambiguous`：传入值是某成员的登录名，同时又是其他成员的昵称。
   错误码：MCP 报错末尾附“（错误代码：xxx）/ (error code: xxx)”（fail 里通用处理：错误实现 `ErrorCode()` 就附）；
   接口 `writeError` 按 400 返回该码，并写进了错误码清单注释。
3. **文案**：core.json、mcp.json 中英两份都补了；owner / assignee / transfer_to 的参数说明加了“只给昵称时先用 cohive_find_member 查”。
4. **技能文件**中英两版：“把问题交给别人”一节和转交那一行都补了“昵称先查成员、确认登录名再指派，多候选让用户选”。
5. 顺带：`cohive init` 写入的放行清单、`examples/` 下 Claude Code 与 Codex 的示例配置，都把新工具列为直接放行（只读工具，与 who_owns 同级）。

## 改动文件

- 新增：`internal/core/member.go`（FindMembers、MemberChoiceError、assignable 移到这里）、`internal/core/member_test.go`、`internal/mcpserver/member_test.go`
- 修改：`internal/store/member.go`（FindMembers、MembersNamedLike）、`internal/core/core.go`（删掉旧 assignable）、
  `internal/mcpserver/server.go`、`internal/httpapi/httpapi.go`、`internal/i18n/locales/{zh-CN,en}/{core,mcp}.json`、
  `internal/skill/{zh-CN,en}/SKILL.md`、`cmd/cohive/init.go`、`examples/claude-code/.claude/settings.json`、`examples/codex/.codex/config.toml`、
  `internal/mcpserver/{server_test,i18n_test}.go`（工具数 10→11、只读注解表加新工具、夹具暴露连接池）
- 没碰 N-4 的文件（`internal/core/notify.go`、`internal/notify`、会话提醒相关）。

## 测试

走查库：`cohive_walk_issue13`（localhost:55432 上新建，合入后可删）。

- `go vet ./...` 通过；`go test -count=1 ./...` 全部通过（真实 Postgres）。
- `make build`：第一次在网页前端 vitest 阶段报失败（本次没改前端，失败用例名没留住）；随后连跑两次都通过（17 个测试文件 / 80 个用例全过，二进制与分发包都出来了）。疑似前端测试偶发不稳，建议留意。
- 新增用例（真实 Postgres）：
  - 唯一昵称能查到、登录名大小写不同也能查到、返回负责的服务；
  - 昵称重名返回多位候选，完全相同的排前，包含匹配在后；`%`、`_` 不当通配符；
  - 只返回本工作空间在册、未停用的成员（另一工作空间、已移出、已停用的同昵称成员都不出现，也不算候选）；
  - 超过上限截断并标 `truncated`；
  - 起草 / 发出 / 转交按昵称、按大小写不对的登录名、按与别人昵称相同的登录名都被拦下并给出候选，被拦下的发出不改动问题单；
  - 纯登录名起草、发出、转交照旧成功；对不上任何人时照旧报“成员不存在”；
  - 登录名所属账号已停用而它又是别人的昵称时，只列能指派的候选；
  - MCP 端到端：查找字段、中文报错带候选与错误码、英文账号报错格式。

## 变异验证（9 个探针，全部变红）

| 探针 | 结果 |
|---|---|
| P1 指派前检查不看昵称 | 红：AssignByNicknameIsBlocked 等 4 个 |
| P2 去掉“登录名撞别人昵称”的拦截 | 红：3 个 |
| P3 查找不排除停用账号 | 红：FindMembersOnlyActiveInWorkspace |
| P4 查找不限工作空间 | 红：FindMembersOnlyActiveInWorkspace |
| P5 截断判断少取一条 | 红：FindMembersLimit |
| P6 改用 LIKE（通配符生效） | 红：FindMembersByNickname |
| P7 MCP 报错不带错误码 | 红：FindMemberAndNicknameGuardOverMCP |
| P8 精确匹配不排前 | 起初存活（测试数据恰好按字母序也成立），补了一个字母序在前的包含匹配成员后变红 |
| P9 昵称只有一位候选时直接指派 | 红：3 个 |

## 与要求的出入、需要主脑定的事

1. **超出要求**：大小写不同的登录名（如 `GuanYu`）也按 `member_nickname` 拦下并给出候选，没有自动纠正成 `guanyu`。登录名规则本来就是小写，这样更稳；如果想自动纠正，改 assignable 一处即可。
2. **口径**：已停用账号和已移出成员不参与冲突判断，也不出现在候选里（他们本来就不能被指派）。代价是：如果用户说的昵称其实指的是一位已停用的人，而它恰好等于某在册成员的登录名，会照旧指派给那位在册成员。我认为可以接受，你看要不要更严。
3. **MCP 没有结构化错误通道**，错误码只能附在报错文字末尾；接口那边的映射目前没有任何端点会走到（网页和接口都不接受 assignee），只写了映射没写接口用例。
4. **已装用户的放行清单不会自动更新**：之前跑过 `cohive init` 的成员，用到新工具时编程工具会弹一次确认，重新 `cohive init` 后才直接放行。要不要在发版说明里提一句，你定。
5. **没改的地方**：管理员命令行（`cli.go` 的成员 / 负责人、`admin.go` 的改派操作人）仍直接按登录名查，没加昵称拦截——那里是管理员手打登录名，风险低；要统一的话可以另开单。
6. 仓库里没有 graphify-out，没跑 `graphify update`。
