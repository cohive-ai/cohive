# 在飞台账（主脑维护，反映"现在"）

## 0. 规则
当前主脑会话：Cohive 主脑（local_ea0423c5-97dc-47af-b977-0bc3f3c12fe0；2026-W42 起；每次换主脑时更新）
交回目录：docs/brain/inbox/

以上两行的开头文字固定不变，讨论会话靠 grep 这两个前缀找到台账和交回目录。

1. 派活权、验收权只在主脑。讨论会话只把结论落成文档（`docs/brain/inbox/`），直接提交到 main 后通知主脑。
2. WIP：同时在飞代码分支 ≤ 2 条。
3. 串行合入；每合一条，其余在飞分支 rebase 并重跑测试。
4. 联合验收：在合并后的 main 上跑全量。
5. 契约先行：单仓项目，接口约定写在对应里程碑计划（`docs/superpowers/plans/`）里，计划先于代码提交。
6. 合入权限：主脑验收通过即由主脑合入 main 并 push，事后告知 lee（lee 2026-10-16 授权，原为“每次合入先问 lee”）；禁止 force push main；每个大阶段结束仍提醒 lee 检查。
7. 测试口径：`go vet ./... && go test -count=1 ./... && make build`；测试用真实 Postgres（本机 Docker，`localhost:55432`，用户/密码/库均为 `cohive`）；涉及登录、部署的改动还要在测试服 https://cohive-test.anormlylee.com 实测。

## 1. 在飞（合入后删除这一行）
状态只用这几个值：`已派` / `实施中` / `待验收` / `验收中` / `打回` / `待用户` / `可合入`。

| 编号 | 分支 | 会话（短名 / 会话编号） | tip | 状态 | 验收要点 |
|---|---|---|---|---|---|
| M5b | `feature/phase1-m5b-accounts`（基于 main `e8a63a2`） | m5b / `local_2e577d38-8052-4c21-97ab-a6422db710a3` | 已验收 = `0cf53e6`（打回小项） | 打回 | 按设计稿 T1–T8；T1、T2 完成先回报（迁移与权限先验）。验收：全量；设计稿 6.2 节 16 条均有用例；设计稿建议的 5 条探针 + 自选 ≥2；迁移在 `cohive_test` 的副本上试跑；测试服实测由主脑做。终验打回：补停用两道防线各自的用例（探针 R1 去掉停用时删会话、R1b 去掉会话查询的停用检查、R6 去掉设备密钥查询的停用检查，均存活）；旧版配对电脑执行 `cohive init` 误报“不是成员”，应提示 `--force` 重新配对。复验：核对新 tip、全量、重打 R1/R1b/R6 |

## 2. 待用户拍板（拍板后删除，有长期效力的移到 decisions）

| 编号 | 问题 | 选项 | 建议 | 阻塞什么 |
|---|---|---|---|---|

## 3. 队列（按优先级，随时重排；派出后移到 §1）
排序依据见 requirements.md 第 5 节（2026-10-16 lee 同意）。

- P1 L-2 lee 亲手实测 Telegram：个人中心连接 Telegram；管理 → 工作空间填“小项目”群编号 `-1004357698945`（群里有其他真人），触发一次团队注册后在群里点批准。M5 已先合入（lee 2026-10-16：先按建议做，实际使用中有问题再调），真人试用前做完 —— 来源 D6
- P1 M6-a（M5b 合入后再派，避免同改 `internal/core`、`cmd/cohive` 冲突）发给同事能用：`cohive` 命令行分发（预编译程序，不要求对方装 Go、不要求读私有仓库）+ 写入前密钥扫描 + 评论导致确认作废（评论也让版本号加一）—— 来源 M5 会话、M1 审查遗留
- P1 L-1 lee 亲自验证 Claude Code 与 Codex 交互模式下确认弹窗确实出现（方案第 4 节要求实测，无界面模式已验证拦截）—— 来源 M5 会话；lee 亲手做，不派
- P1 B-1 采集基线：试用前人工记录两周跨人交接情况（方案第 11 节）—— lee 亲手做，不派
- P2 M6-b 部署：Docker 镜像 + compose（数据目录挂载、初始密码文件位置）+ 数据库备份 —— 来源 M5 会话
- P3 新登录私聊通知加一键吊销 —— 来源 M5 会话
- P3 过期设备授权请求、通讯连接请求清理；删掉旧表 `login_codes` —— 来源 M5 会话
- P3 第一阶段剩余范围：`CODEOWNERS` 路由、会话钩子注入待办摘要、超时催办（届时引入 river）、代码占用声明 —— 来源 `docs/plan.md` 第 10 节
- P4 接第二种通讯工具前，把成员的通讯账号拆成独立表 —— 来源 M5 会话

## 4. 环境与待清理
- 测试服（2026-10-16 起）：**现跑 M5b 分支程序**（验收工作树 `…/scratchpad/m5b-accept`，`0cf53e6`）+ 长期测试库 `cohive_test`（已执行第七号迁移；迁移前备份 `…/scratchpad/backup/cohive_test-before-m5b.dump`，M5b 若作废就恢复它并换回 main 程序），监听 `127.0.0.1:8787`，经 Cloudflare 隧道 `cohive-test` 对外（隧道跑在主脑会话的终端标签 c1；原 c3 随 M5 会话归档被关掉过，见记忆 archive-kills-terminal-tabs）。重启：`/private/tmp/claude-503/-Users-jiali-MyCode-cohive/f7d08f07-8081-4977-b6d1-3be664ac942f/scratchpad/test-restart.sh /Users/jiali/MyCode/cohive`（先在 main `make build`）。**脚本与数据目录在临时目录，重启电脑可能丢失**，M6-a 把启动脚本收进仓库。lee 本机经测试服的 Cohive 连接需重新 `cohive login`（旧令牌属于旧库）。
- 测试服数据目录 `…/scratchpad/test-data/`：初始密码文件已在首登改密后自动删除；admin 新密码在 `admin-password-new.txt`，测试成员 `tester-a`、`tester-b` 的密码在 `member-password.txt`（权限 600，只是测试凭据）。
- 实测用的命令行凭据目录 `…/scratchpad/cli-a`（已吊销）、`cli-a2`、`cli-adm`，用 `COHIVE_CONFIG_DIR` 指定，没动 lee 的 `~/.config/cohive`。
- 旧库：`cohive`（M4 数据，群编号 `-1004357698945`，停用不删）、`cohive_m5`（旧版第六号，停用；里面的 `claude-test`、`tester-ann` 随库停用，不再单独删）。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话“英文名和项目目录设置”（M5 已合入 `d21d6d8`）；DOC-1 会话 doc-1（已合入）。待归档会话：无。
