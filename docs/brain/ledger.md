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
| N-4 信息链通知补全 | 会话自建（从最新 main） | im-research / `local_92d8a142-f47f-4bef-b689-a8502b7ab245` | — | 已派 | 每次变化都通知除操作者外的相关方（群通知与会话提醒口径一致）；补留言、开始修复、转交告知发起人、管理员改派等；留言正文不进群；先交“动作 × 收件人”对照表；交回 `docs/brain/inbox/N-4.md`；之后同会话做 #12 |
| ISSUE-13 MCP 按昵称找成员 | 会话自建（从最新 main） | issue-13 / `local_2621a548-cd50-49c9-9e96-4fbad0120443` | — | 已派 | 新增成员查找工具；指派仍只认登录名，昵称或歧义时返回候选不指派；可本地化报错；技能文件补说明；避开 N-4 的文件；交回 `docs/brain/inbox/ISSUE-13.md` |
| IM-DT | 已合入 main `ce7c34e`（已验收 = `c8327a2`） | im-research / `local_92d8a142-f47f-4bef-b689-a8502b7ab245`（保留，处理实测反馈） | `c8327a2` | 待 guanyu（cohive-internal #6 首轮实测：做指南第 1–6 步，凭据私下交 lee，lee 配到测试服并建工作空间 dingtalk-test 后由他绑群验收；#5 接手后续开发维护，服务 dingtalk 负责人 guanyu） | 部署指南 `docs/guides/dingtalk.md` 文末 11 条“请实测并反馈”；反馈写回 #6 或另起单给服务 dingtalk；需改代码的仍可交回 im-research；guanyu 的仓库写权限待 lee 开通 |

## 2. 待用户拍板（拍板后删除，有长期效力的移到 decisions）

| 编号 | 问题 | 选项 | 建议 | 阻塞什么 |
|---|---|---|---|---|
| I18N-GH | 私密漏洞报告仍未开启（lee 亲手点：仓库设置 → 左侧“安全与质量”分组 → 高级安全 → 私密漏洞报告一栏的启用按钮） | 做 | 做 | 安全报告入口可用 |

## 3. 队列（按优先级，随时重排；派出后移到 §1）
排序依据见 requirements.md 第 5 节（2026-10-16 lee 同意）。
- P1 ISSUE-12 问题单“作废”（cohive-internal #12，lee 已接单）：发起人可作废未接单的单，管理员可作废任意单，草稿可删；写理由记动态；看板默认隐藏；通知另一方。N-4 合入后由 im-research 从最新 main 开分支做 —— 来源 guanyu
- P3 DEP-2 依赖跟进：cohive-ai/cohive#3 等机器人按新分组配置自动重建或关闭，10-25 前后仍没动就手动关并留言；重建的分组请求（react 19.3.1、i18next 26.4.5、react-query 5.104.3、react-i18next 17.0.16）10-28 前后满两周再合；typescript 7、jsdom 30、vitest 5 等大版本单独请求逐个评估 —— 来源 DEP-1b
- P2 T-5 cmd/cohive 用例从当前目录往上找 `.mcp.json`，在执行过 cohive init 的克隆里会读到本机配置，3 条用例失败（TestRevokedDeviceHintsForce、TestLoginOpensBrowserAndSaves、TestLegacyCredentials）；开源贡献者照着做也会碰到，测试应隔离仓库目录 —— 来源 N-2 合入时主脑发现
- P2 OS-4 打标签时用 GitHub Actions 构建多架构镜像推到 ghcr，compose 缺省用预构建镜像（公开后第一个小任务）；另：机器人入群后 /bind 绑群、未配对时 init 自动走 login、安装脚本经同意写 PATH、Caddy 自动 HTTPS 示例、发布物附第三方许可证清单 —— 来源 OSS-1 建议
- P3 T-3 转交或发单给正在被移出的成员时，assignable 不锁成员行，可能留下负责人是已移出成员的单（推测未验证）；在问题单锁之后对接收人成员行加 FOR SHARE 并复查状态 —— 来源 稳定性修复遗留
- P3 T-4 internal/secretscan 的 TestTruePositives 在 -count>1 时偶发（包级固定种子随机数）—— 来源 稳定性修复
- P3 测试服无人值守：launchd 是用户级服务，登录后才启动，重启停在登录界面时测试服断（Docker Desktop 同理）；要做到无人值守需开自动登录，由 lee 决定；日志不自动轮换 —— 来源 OPS-1
- P2 S-1 试用开始后：从事件记录生成统计视图（交接耗时、闭环时长、退回率、升级率等，按周看趋势），并把 docs/plan.md 第 11 节“先采集基线”改为“从系统记录计算” —— 来源 lee 2026-10-17
- P3 新登录私聊通知加一键吊销 —— 来源 M5 会话
- P3 过期设备授权请求、通讯连接请求清理；删掉旧表 `login_codes` —— 来源 M5 会话
- P3 第一阶段剩余范围（按试用反馈排序）：`CODEOWNERS` 路由（现在服务负责人只能用服务器命令行登记，网页上没有入口）、代码占用声明 —— 来源 `docs/plan.md` 第 10 节

## 4. 环境与待清理
- 本仓库自用 Cohive（2026-10-18 起）：主检出已 `cohive init` 接入测试服工作空间 `cohive-internal`（只配 Claude Code，含会话钩子），生成的 `.mcp.json`、`.claude/settings.json`、`.claude/skills/` 写在本地 `.git/info/exclude`，不提交；服务 `cohive` 负责人 lee。工作树里的实施会话没有这些文件，不受提醒打扰。朋友与同事在该空间报的问题单，由 lee 或主脑在主检出会话里用 /cohive 拉取，再分派。
- 仓库：公开在 `https://github.com/cohive-ai/cohive`（2026-10-17 起），Apache-2.0。往 main 提交的一切都会公开，台账里不写本机绝对路径、令牌、公司事项。
- 测试服（2026-10-18 19:30 起在办公室测试机 pm-dev1，与 pm 项目共用、互不干扰）：服务器上的检出 `~/cohive`（main），用仓库编排 `deploy/docker-compose.yml` 加本地覆盖文件（隧道容器用宿主机网络，不入仓），编排项目 `cohive`，容器 cohive-postgres-1、cohive-cohive-1、cohive-tunnel-1，卷 cohive_pgdata、cohive_data，均 unless-stopped；配置 `deploy/.env`（600，含机器人与隧道令牌、缺省语言 zh-CN，不入仓）；库为编排里的 `cohive`，已执行至 0013 迁移；监听 127.0.0.1:8787，经隧道 `cohive-test` 对外。运维目录 `~/cohive-ops`（README.md 中文说明、backup.sh、upgrade.sh、日志）。合入 main 后升级测试服：`ssh pm-dev1 ~/cohive-ops/upgrade.sh`（先备份，只快进拉 main，只重建 cohive，等 healthz，失败打印回滚办法），不再用本机 `scripts/cohive-server.sh`。当前 `b351103`。pm 的容器、pm-cloudflared、/etc/cloudflared 一概不碰。
- 备份：服务器 crontab 每天 UTC 20:30（即 UTC+7 03:30）跑 `~/cohive-ops/backup.sh`，保留 14 份，在 `~/cohive-ops/backups/`；成功后用 Cohive 专用密钥同步到 pm-dev2 的 `/hdd/backup/cohive/`（授权只能写该目录，取回用 lee 本机的 pm-dev2 别名；每次最多删 3 份；跨机失败只记日志）；切换时的导出 `~/cohive-ops/cutover-20261018-193003.dump`。
- 本机旧测试服（回滚用，保留）：launchd 的 server、tunnel、backup 三个服务已停用，plist 保留；容器 cohive-dev-postgres（已设 unless-stopped）与库 `cohive_test` 保留，数据停在 10-18 19:30；本机备份在 `~/.local/share/cohive-server/backups/`。lee 定 10-19 晚（迁移满 24 小时、服务器无异常）删除，删除清单：本机容器 cohive-dev-postgres 及其中的库 cohive_test、cohive、cohive_m5（容器删除即全删），~/Library/LaunchAgents 下 com.anormlylee.cohive.server、tunnel、backup 三个 plist；本机备份目录 `~/.local/share/cohive-server/backups/` 与 data 目录（含测试账号密码文件）先保留，删前再问。
- 测试账号：admin（服务器管理员）、tester-a、tester-b（已停用）等；密码文件仍在本机 `~/.local/share/cohive-server/data/`（权限 600，测试凭据）。工作空间：default、proj-b、proj-c、lee、cohive-internal。
- 旧库：`cohive`（M4 数据，群编号 `<旧测试群编号>`，停用不删）、`cohive_m5`（旧版第六号，停用）。
- 待清理：主脑临时目录里的旧启动脚本 `test-restart.sh` 与 `test-data/` 已不再使用。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话、doc-1、m5b、m6-a。、m6c（M6-c、M6-d 均已合入）、m6e（M6-e 至 M6-e3 均已合入）、im6（IM-6 已合入）、ops-launchd（OPS-1 已合入）、oss（OSS-1、OSS-2 已合入）、stab（稳定性修复已合入）。ops-migrate（测试服迁移已完成）。待归档会话：无。
