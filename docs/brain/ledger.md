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
| IM2-1 Slack 接入 | 已合入 main `129bce2`（已验收 = `0ef06c8`） | im-research-2 / `local_cc6acfd5-18c9-401c-b14b-8ccbcf70869a`（保留，处理实测反馈，之后做飞书与 Lark） | `0ef06c8` | 待用户（lee 建免费 Slack 工作区、按 deploy/slack-app-manifest.yaml 建应用，两个令牌亲手写进 pm-dev1 的 deploy/.env 后跑 upgrade.sh，再绑频道、连账号，按 docs/guides/slack.md 第 7 节验收） | 真环境确认：Socket 确认时限、只在消息块里的 @ 是否提醒、免费版改旧消息 |

## 2. 待用户拍板（拍板后删除，有长期效力的移到 decisions）

| 编号 | 问题 | 选项 | 建议 | 阻塞什么 |
|---|---|---|---|---|
| I18N-GH | 私密漏洞报告仍未开启（lee 亲手点：仓库设置 → 左侧“安全与质量”分组 → 高级安全 → 私密漏洞报告一栏的启用按钮） | 做 | 做 | 安全报告入口可用 |

## 3. 队列（按优先级，随时重排；派出后移到 §1）
排序依据见 requirements.md 第 5 节（2026-10-16 lee 同意）。
- P3 注册时自填显示名也做撞名检查（防冒充，与 #15 规则一致）；加入新工作空间时不检查现有显示名的问题一并评估 —— 来源 ISSUE-15
- P3 钉钉未核实项（国际版是否支持互动卡片与 Stream、卡片回调是否计入额度、模板导入导出、带参数打开单聊）遇到再起单，服务 dingtalk 负责人 guanyu；#5（接手钉钉维护）guanyu 尚未接单 —— 来源 #6 关闭
- P3 已作废的单加“复制为新草稿”；操作记录列表写明命令行作废以哪个管理员（--as）名义 —— 来源 ISSUE-12 建议
- P3 T-6 前端测试偶发失败：Cancel.test.tsx“看板默认隐藏已作废的单”在整套测试负载下偶发找不到元素（单跑 3 次、整套再跑 2 次均过），疑为等待时限不够 —— 来源 UI-1 验收
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
- 仓库外部提交：guanyu（GitHub 账号 Blear）是 cohive-ai 组织所有者（对所有仓库都是管理员），可直接推 main（lee 10-21 确认维持）。主脑每次合入前看一眼 main 上有没有不经主脑的新提交，有就事后审阅并在合并结果上照常跑全量，异常再报 lee。
- 仓库：公开在 `https://github.com/cohive-ai/cohive`（2026-10-17 起），Apache-2.0。往 main 提交的一切都会公开，台账里不写本机绝对路径、令牌、公司事项。
- 测试服（2026-10-18 19:30 起在办公室测试机 pm-dev1，与 pm 项目共用、互不干扰）：服务器上的检出 `~/cohive`（main），用仓库编排 `deploy/docker-compose.yml` 加本地覆盖文件（隧道容器用宿主机网络，不入仓），编排项目 `cohive`，容器 cohive-postgres-1、cohive-cohive-1、cohive-tunnel-1，卷 cohive_pgdata、cohive_data，均 unless-stopped；配置 `deploy/.env`（600，含机器人与隧道令牌、缺省语言 zh-CN，不入仓）；库为编排里的 `cohive`；监听 127.0.0.1:8787，经隧道 `cohive-test` 对外。运维目录 `~/cohive-ops`（README.md 中文说明、backup.sh、upgrade.sh、日志）。合入 main 后升级测试服：`ssh pm-dev1 ~/cohive-ops/upgrade.sh`（先备份，只快进拉 main，只重建 cohive，等 healthz，失败打印回滚办法），不再用本机 `scripts/cohive-server.sh`。当前 `ca88edf`，已执行至 0014 迁移。pm 的容器、pm-cloudflared、/etc/cloudflared 一概不碰。
- 备份：服务器 crontab 每天 UTC 20:30（即 UTC+7 03:30）跑 `~/cohive-ops/backup.sh`，保留 14 份，在 `~/cohive-ops/backups/`；成功后用 Cohive 专用密钥同步到 pm-dev2 的 `/hdd/backup/cohive/`（授权只能写该目录，取回用 lee 本机的 pm-dev2 别名；每次最多删 3 份；跨机失败只记日志）；切换时的导出 `~/cohive-ops/cutover-20261018-193003.dump`。
- 本机旧测试服已于 10-20 清理：删除库 `cohive_test`、`cohive_m5` 与 ~/Library/LaunchAgents 下三个 plist。**容器 cohive-dev-postgres 与其中的库 `cohive` 保留**：它们是本机开发与分支测试共用的 Postgres（localhost:55432，测试默认连 `cohive` 库），不属于测试服。本机备份目录与 data 目录（含测试账号密码文件）仍保留。
- 测试账号：admin（服务器管理员）、tester-a、tester-b（已停用）等；密码文件仍在本机 `~/.local/share/cohive-server/data/`（权限 600，测试凭据）。工作空间：default、proj-b、proj-c、lee、cohive-internal。
- 待清理：主脑临时目录里的旧启动脚本 `test-restart.sh` 与 `test-data/` 已不再使用。
- 机器人：同一个机器人令牌同时只能有一个程序收消息，测试服与本机调试不要同时跑。
- 已归档：M5 实施会话、doc-1、m5b、m6-a。、m6c（M6-c、M6-d 均已合入）、m6e（M6-e 至 M6-e3 均已合入）、im6（IM-6 已合入）、ops-launchd（OPS-1 已合入）、oss（OSS-1、OSS-2 已合入）、stab（稳定性修复已合入）。ops-migrate（测试服迁移已完成）、issue-13（#13 已合入）。im-research（钉钉接入、N-1 至 N-4、DEP-1、DEP-1b、DOC-2、#12 均已合入，钉钉实测通过）。tools-research（TOOLS 调研与 opencode 适配已合入）。brand-landing（BRAND-1、UI-1、THEME-1 已合入）。issue-15（#15 已合入）。待归档会话：无。
