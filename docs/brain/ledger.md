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
| M6-e | `feature/phase1-m6e-session-alerts`（基于 main） | m6e / `local_1caa8871-0d36-4ef4-88e4-591341865886` | 计划 `86d70d9` 已批 | 实施中 | 先交两家客户端（命令行版与桌面版）钩子与频道能力的实测结论表和计划，主脑审批后再写代码。验收：全量；探针 ≥2（候选：提醒带出问题单正文、离线时阻塞会话、游标导致重复或漏提醒）；桌面版需 lee 实测（计划第 1.6 节暗号测试，结果转 m6e）|
| M6-d | `feature/phase1-m6d-external-url`（基于 main `2291288`） | m6c / `local_1699a2f1-f314-4288-a268-cf332597cf22`（M6-c 原会话接着做） | — | 已派（先出计划） | 对外地址服务器级（预填、环境变量锁定、并入允许主机名、迁移提升）、去掉工作空间级地址与误导占位、路径与显示名分离、两类修改进记录。验收：全量；探针 ≥2；测试服实测群消息链接 |

## 2. 待用户拍板（拍板后删除，有长期效力的移到 decisions）

| 编号 | 问题 | 选项 | 建议 | 阻塞什么 |
|---|---|---|---|---|

## 3. 队列（按优先级，随时重排；派出后移到 §1）
排序依据见 requirements.md 第 5 节（2026-10-16 lee 同意）。
- P1 L-2（审批按钮已通过：批准、拒绝各一次；群通知 @ 待 lee 回报）lee 亲手实测 Telegram：个人中心连接 Telegram；管理 → 工作空间填“小项目”群编号 `-1004357698945`（群里有其他真人），触发一次团队注册后在群里点批准。M5 已先合入（lee 2026-10-16：先按建议做，实际使用中有问题再调），真人试用前做完 —— 来源 D6
- P1 B-1 采集基线：试用前人工记录两周跨人交接情况（方案第 11 节）—— lee 亲手做，不派
- P2 M6-b 部署：Docker 镜像 + compose（数据目录挂载、初始密码文件位置）+ 数据库备份 —— 来源 M5 会话
- P3 新登录私聊通知加一键吊销 —— 来源 M5 会话
- P3 过期设备授权请求、通讯连接请求清理；删掉旧表 `login_codes` —— 来源 M5 会话
- P3 第一阶段剩余范围：`CODEOWNERS` 路由、会话钩子注入待办摘要、超时催办（届时引入 river）、代码占用声明 —— 来源 `docs/plan.md` 第 10 节
- P4 接第二种通讯工具前，把成员的通讯账号拆成独立表 —— 来源 M5 会话

## 4. 环境与待清理
- 测试服（2026-10-16 起）：main 构建的程序（`/Users/jiali/MyCode/cohive/bin/`，当前 `650d54e`）+ 长期测试库 `cohive_test`，由仓库脚本管理：`scripts/cohive-server.sh start|stop|restart|status`（配置 `~/.local/share/cohive-server/server.env`，数据目录 `…/data`，日志 `…/logs`，详见 `docs/guides/test-server.md`）。监听 `127.0.0.1:8787`，经 Cloudflare 隧道 `cohive-test` 对外（隧道跑在主脑会话的终端标签 c1；归档会话前先确认隧道不在它的终端里，见记忆 archive-kills-terminal-tabs）。
- 备份：`scripts/backup-db.sh` 手动可用，备份在 `~/.local/share/cohive-server/backups/`，保留 14 份；launchd 定时备份已安装（`~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist`，每天 03:30，日志 `…/logs/backup.log`；撤销见 `docs/guides/test-server.md` 第 5 节），2026-10-16 已试跑成功。Docker Desktop 没开时备份会失败。迁移 M5b 前的备份在主脑临时目录 `…/scratchpad/backup/`。
- 测试账号：admin（服务器管理员）、tester-a、tester-b（已停用）；密码文件在 `~/.local/share/cohive-server/data/`（权限 600，测试凭据）。工作空间：default、proj-b、proj-c。
- 旧库：`cohive`（M4 数据，群编号 `-1004357698945`，停用不删）、`cohive_m5`（旧版第六号，停用）。
- 待清理：主脑临时目录里的旧启动脚本 `test-restart.sh` 与 `test-data/` 已不再使用。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话、doc-1、m5b、m6-a。（m6c 合入 M6-c 后继续做 M6-d，不归档。）待归档会话：无。
