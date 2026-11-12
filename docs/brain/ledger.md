# 台账（主脑维护，反映"现在"）

在飞、待拍板、队列都在同目录的 cards.json 里，这里只放规则、遗留小项和环境。

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
8. 模型分工（lee 2026-10-29 定）：编排（拆分、设计、派活、审核子任务）用 Opus；具体实现、抽取、翻译等执行工作用 Sonnet 子代理；验收由 Opus 做（主脑）。简单的长程任务（机械、范围清楚、不需要设计判断）整个分支会话直接用 Sonnet。派活时在提示里写明。

## 1. 遗留小项（零碎的小事，不上卡；件数同步到 cards.json 的 leftovers；挑出来排期时转成待办卡并从这里删掉）
- 钉钉未核实项（国际版是否支持互动卡片与 Stream、卡片回调是否计入额度、模板导入导出、带参数打开单聊）遇到再起单，服务 dingtalk 负责人 guanyu；#5（接手钉钉维护）guanyu 尚未接单 —— 来源 #6 关闭
- 已作废的单加“复制为新草稿”；操作记录列表写明命令行作废以哪个管理员（--as）名义 —— 来源 ISSUE-12 建议
- T-4 internal/secretscan 的 TestTruePositives 在 -count>1 时偶发（包级固定种子随机数）—— 来源 稳定性修复
- 测试服无人值守：pm-dev1 重启后若停在登录界面 Docker 不起；日志不自动轮换；要不要开自动登录由 lee 定 —— 来源 OPS-1
- 新登录私聊通知加一键吊销 —— 来源 M5 会话
- 过期设备授权请求、通讯连接请求清理；删掉旧表 `login_codes` —— 来源 M5 会话

## 2. 环境与待清理
- 本仓库自用 Cohive（2026-10-18 起；11-01 起改用户级配置）：lee 本机用新版 `cohive init` 装了用户级 Claude Code、Codex 配置，本机绑定表把本仓库绑到测试服工作空间 `cohive-internal`，同一仓库所有工作树自动生效，项目里不写任何配置文件；服务 `cohive` 负责人 lee。朋友与同事在该空间报的问题单，由 lee 或主脑用 /cohive 拉取，再分派。
- 仓库外部提交：guanyu（GitHub 账号 Blear）是 cohive-ai 组织所有者（对所有仓库都是管理员），可直接推 main（lee 10-21 确认维持）。主脑每次合入前看一眼 main 上有没有不经主脑的新提交，有就事后审阅并在合并结果上照常跑全量，异常再报 lee。
- 仓库：公开在 `https://github.com/cohive-ai/cohive`（2026-10-17 起），Apache-2.0。往 main 提交的一切都会公开，台账里不写本机绝对路径、令牌、公司事项。
- 测试服（2026-10-18 19:30 起在办公室测试机 pm-dev1，与 pm 项目共用、互不干扰）：服务器上的检出 `~/cohive`（main），用仓库编排 `deploy/docker-compose.yml` 加本地覆盖文件（隧道容器用宿主机网络，不入仓），编排项目 `cohive`，容器 cohive-postgres-1、cohive-cohive-1、cohive-tunnel-1，卷 cohive_pgdata、cohive_data，均 unless-stopped；配置 `deploy/.env`（600，含机器人与隧道令牌、缺省语言 zh-CN，不入仓）；库为编排里的 `cohive`；监听 127.0.0.1:8787，经隧道 `cohive-test` 对外。运维目录 `~/cohive-ops`（README.md 中文说明、backup.sh、upgrade.sh、日志）。合入 main 后升级测试服：`ssh pm-dev1 ~/cohive-ops/upgrade.sh`（先备份，只快进拉 main，只重建 cohive，等 healthz，失败打印回滚办法），不再用本机 `scripts/cohive-server.sh`。当前 `b5a86f7`，已执行至 0017 迁移。pm 的容器、pm-cloudflared、/etc/cloudflared 一概不碰。
- 备份：服务器 crontab 每天 UTC 20:30（即 UTC+7 03:30）跑 `~/cohive-ops/backup.sh`，保留 14 份，在 `~/cohive-ops/backups/`；成功后用 Cohive 专用密钥同步到 pm-dev2 的 `/hdd/backup/cohive/`（授权只能写该目录，取回用 lee 本机的 pm-dev2 别名；每次最多删 3 份；跨机失败只记日志）；切换时的导出 `~/cohive-ops/cutover-20261018-193003.dump`。
- 本机旧测试服已于 10-20 清理：删除库 `cohive_test`、`cohive_m5` 与 ~/Library/LaunchAgents 下三个 plist。**容器 cohive-dev-postgres 与其中的库 `cohive` 保留**：它们是本机开发与分支测试共用的 Postgres（localhost:55432，测试默认连 `cohive` 库），不属于测试服。本机备份目录与 data 目录（含测试账号密码文件）仍保留。
- 测试账号：admin（服务器管理员）、tester-a、tester-b（已停用）等；密码文件仍在本机 `~/.local/share/cohive-server/data/`（权限 600，测试凭据）。工作空间：default、proj-b、proj-c、lee、cohive-internal。
- 待清理：主脑临时目录里的旧启动脚本 `test-restart.sh` 与 `test-data/` 已不再使用。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话、doc-1、m5b、m6-a。、m6c（M6-c、M6-d 均已合入）、m6e（M6-e 至 M6-e3 均已合入）、im6（IM-6 已合入）、ops-launchd（OPS-1 已合入）、oss（OSS-1、OSS-2 已合入）、stab（稳定性修复已合入）。ops-migrate（测试服迁移已完成）、issue-13（#13 已合入）。im-research（钉钉接入、N-1 至 N-4、DEP-1、DEP-1b、DOC-2、#12 均已合入，钉钉实测通过）。tools-research（TOOLS 调研与 opencode 适配已合入）。brand-landing（BRAND-1、UI-1、THEME-1 已合入）。issue-15（#15 已合入）。ws-archive（WS-1 已合入）。t-6（T-6 已合入）。cc-watchers（CC-1 已合入）。dm-notify（DM-1 已合入）。待归档会话：无。
