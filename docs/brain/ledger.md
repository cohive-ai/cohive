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

## 2. 待用户拍板（拍板后删除，有长期效力的移到 decisions）

| 编号 | 问题 | 选项 | 建议 | 阻塞什么 |
|---|---|---|---|---|

## 3. 队列（按优先级，随时重排；派出后移到 §1）
排序依据见 requirements.md 第 5 节（2026-10-16 lee 同意）。
- P2 S-1 试用开始后：从事件记录生成统计视图（交接耗时、闭环时长、退回率、升级率等，按周看趋势），并把 docs/plan.md 第 11 节“先采集基线”改为“从系统记录计算” —— 来源 lee 2026-10-17
- P2 IM 接入钉钉（试用之后）：先做第 0 步（通道接口抽象、通讯账号与工作空间群拆表，约 2–3 人日），再接钉钉国内版（企业内部应用机器人、互动卡片、Stream）；lee 注册测试企业后实测报告里的未核实项。同主题优先交回 im-research 会话（`local_92d8a142-f47f-4bef-b689-a8502b7ab245`）出计划 —— 来源 lee 2026-10-17
- P2 T-1 修偶发用例 `internal/notify` 的 TestChatMigratedUpdatesTarget：2026-10-17 两次合并后全量各失败一次，均为此用例（日志：群升级后“应改发到新群：[]”）；原因是派发器排时间用 Go 当前时间、判断到期用数据库 now()，相差几毫秒。改为统一用数据库时间或测试里放宽 —— 来源 主脑
- P2 M6-b 部署：Docker 镜像 + compose（数据目录挂载、初始密码文件位置）+ 数据库备份 —— 来源 M5 会话
- P3 新登录私聊通知加一键吊销 —— 来源 M5 会话
- P3 过期设备授权请求、通讯连接请求清理；删掉旧表 `login_codes` —— 来源 M5 会话
- P3 第一阶段剩余范围（按试用反馈排序）：`CODEOWNERS` 路由（现在服务负责人只能用服务器命令行登记，网页上没有入口）、超时催办（届时引入 river）、代码占用声明 —— 来源 `docs/plan.md` 第 10 节

## 4. 环境与待清理
- 测试服（2026-10-16 起）：main 构建的程序（`/Users/jiali/MyCode/cohive/bin/`，当前 `650d54e`）+ 长期测试库 `cohive_test`，由仓库脚本管理：`env -u COHIVE_DATABASE_URL scripts/cohive-server.sh start|stop|restart|status`（**必须清掉 COHIVE_DATABASE_URL**：环境变量优先于 server.env，2026-10-17 曾因此连到旧库 `cohive` 启动失败）（配置 `~/.local/share/cohive-server/server.env`，数据目录 `…/data`，日志 `…/logs`，详见 `docs/guides/test-server.md`）。监听 `127.0.0.1:8787`，经 Cloudflare 隧道 `cohive-test` 对外（隧道跑在主脑会话的终端标签 c1；归档会话前先确认隧道不在它的终端里，见记忆 archive-kills-terminal-tabs）。
- 备份：`scripts/backup-db.sh` 手动可用，备份在 `~/.local/share/cohive-server/backups/`，保留 14 份；launchd 定时备份已安装（`~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist`，每天 03:30，日志 `…/logs/backup.log`；撤销见 `docs/guides/test-server.md` 第 5 节），2026-10-16 已试跑成功。Docker Desktop 没开时备份会失败。迁移 M5b 前的备份在主脑临时目录 `…/scratchpad/backup/`。
- 测试账号：admin（服务器管理员）、tester-a、tester-b（已停用）；密码文件在 `~/.local/share/cohive-server/data/`（权限 600，测试凭据）。工作空间：default、proj-b、proj-c。
- 旧库：`cohive`（M4 数据，群编号 `-1004357698945`，停用不删）、`cohive_m5`（旧版第六号，停用）。
- 待清理：主脑临时目录里的旧启动脚本 `test-restart.sh` 与 `test-data/` 已不再使用。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话、doc-1、m5b、m6-a。、m6c（M6-c、M6-d 均已合入）、m6e（M6-e 至 M6-e3 均已合入）、im6（IM-6 已合入）。待归档会话：无。
