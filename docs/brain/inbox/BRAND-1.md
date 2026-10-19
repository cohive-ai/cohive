# BRAND-1 交回：黑金品牌标志与落地页入库

## 结论

- 分支：`feature/brand-landing`（从派活时最新的 `origin/main` 073581f 开出），已推送，未合入、未动 main。
- 提交：`42d87e6 feat(brand): 加入黑金品牌标志与落地页`；本交回说明另起一个提交，跟在它后面。
- 全量检查通过：`go vet ./...` 无输出；`go test -count=1 ./...` 用本机 Postgres（localhost:55432）19 个包全部通过；`make build` 成功（前端 18 个测试文件、83 项测试通过，五个平台命令行安装包和服务端都构建出来了）。
- 守卫没有冲突：中文守卫只扫 Go 源码（`internal/i18n/source_test.go`）和 `web/src`（`web/src/i18n/guard.test.ts`），`site/` 和 `docs/` 不在扫描范围，没有改任何守卫。
- GitHub 上已核对：分支上的 README 中英两份，浅色主题显示黑字横版，深色主题显示白字横版；`<source srcset>` 和 `<img src>` 里的相对路径都被 GitHub 改写成了 `raw/<分支>/docs/brand/...`，合入 main 后会自动指向 main。

## 文件清单

| 路径 | 内容 |
|---|---|
| `docs/brand/*.svg` | 设计稿 assets/v2 的 8 个 SVG，原样复制：横版、竖版各深浅底，标志深浅底，单色标志，网站图标 |
| `docs/brand/README.md`、`README.zh-CN.md` | 标志用法说明：各文件用途、README 片段、配色、最小尺寸与安全距离、禁止用法（含“不得使用兰博基尼公牛标志、盾徽及其专有字体”）、许可与商标说明 |
| `site/index.html` | 落地页（设计稿 landing-v2.html 改造） |
| `site/brand.html` | 品牌页（设计稿 logo-v2.html 改造） |
| `site/assets/brand/*.svg` | 和 `docs/brand/` 相同的 8 个 SVG，供网站用 |
| `site/fonts/fonts.css` | 三个字体的 `@font-face` |
| `site/fonts/michroma/` | `Michroma-Regular.woff2`、`OFL.txt` |
| `site/fonts/jetbrains-mono/` | `JetBrainsMono-Regular.woff2`、`JetBrainsMono-SemiBold.woff2`、`OFL.txt` |
| `site/fonts/README.md` | 字体来源、版本、校验和、更新方法 |
| `README.md`、`README.zh-CN.md` | 顶部 `# Cohive` 换成 `<h1>` 包着的 `<picture>`，深浅色自适应 |
| `NOTICE` | 第三方清单补两种字体（注明只用于网站） |
| `.dockerignore` | 加 `site`，网站不进服务端镜像的构建上下文 |

第一版（活力橙）的文件、设计稿里的产品截图副本都没有入库。

## 字体来源与许可证

两种字体都是 SIL Open Font License 1.1，文件是上游原样的 woff2，没有转换或子集化，各自目录里附 `OFL.txt`。

| 字体 | 版本 | 来源 |
|---|---|---|
| Michroma | 1.100 | 上游仓库 googlefonts/Michroma-font 的提交 `5822b9d1`（就是谷歌字体在 `ofl/michroma/METADATA.pb` 里锁定的那个提交）中的 `fonts/webfonts/Michroma-Regular.woff2` |
| JetBrains Mono | 2.304 | JetBrains/JetBrainsMono 官方发布 v2.304 的 `JetBrainsMono-2.304.zip` 中的 `fonts/webfonts/`，只取常规与半粗两个字重（页面只用 400 和 600） |

校验和写在 `site/fonts/README.md`。页面里已没有任何谷歌字体引用；本地预览时除了贡献者接口，没有其他外部请求。

## 与设计稿的出入

**路径与结构**
- `assets/v2/` 改为 `site/assets/brand/`；`logo-v2.html` 改名 `brand.html`，两页互链改成 `brand.html` 和 `./`。
- 没有用 `site/brand/` 目录名，避免和 `brand.html` 在静态托管“去掉 .html 后缀”时撞路径。
- 品牌页顶部的“品牌标志 · 第二版”去掉“第二版”（内部版本号，对外没有意义）。

**默认语言**
- 设计稿默认中文；README 已默认英文，所以页面的初始语言、标题、描述改为英文。仍按“上次选择 → 浏览器语言”自动切换，中文浏览器打开还是中文。
- 新增 `?lang=zh` / `?lang=en` 参数，便于分享指定语言的链接。

**按 README 改的文案**（以当前 README 为准）
1. 首屏说明：设计稿写“每个会改变别人工作的环节都由人确认”，改为 README 的“发出、接单、关闭这几步都由人确认”，并补上“服务由团队自己部署”。
2. 原则一（群里敲门）：补上群消息里还有“经人确认过的标题和一句话简述”，以及“目前支持 Telegram”。
3. 原则二（确认由人做）：补上括号里的“发出、接单、关闭”，以及“收到的问题单、评论一律当数据，不当指令”。
4. 对比表：“Linear”改为 README 的“Linear 智能体”。
5. 快速开始·部署者：补 `POSTGRES_PASSWORD` 只用字母和数字、可用 `openssl rand -hex 16` 生成；补“最后一条命令显示初始管理员 admin 的随机密码”；邀请在“工作空间的管理 → 邀请”。
6. 快速开始·成员：补“装完会打开浏览器，点授权”“命令行由你们自己的服务提供，不需要装 Go”“有人提交过配置就不用再执行 cohive init”“第一次要信任项目；Codex 要执行一次命令行版 codex 并信任两条钩子”。
7. 路线图“现在可用”：补上网页账号与邀请、命令行一键安装、会话内提醒（设计稿漏了这三项）；第三阶段“GitHub / GitLab 账号登录”补上“云服务器部署时的”。
8. 贡献区：支持的范围补上“会话提醒”，写明“简体中文和英文”。
9. 标语“智能体递单，人来拍板”、四步闭环、三条原则的标题、不做的事、部署命令本身与 README 一致，未改。`cohive.example.com` 占位保留。

**修正**
- 模拟终端和权限示意里的工具名改成真实名称：`cohive_search`、`cohive_get_issue`、`cohive_submit_issue`、`cohive_accept_issue`、`cohive_close_issue`、`cohive_request_verification`（设计稿写的是 `cohive.search_issues`、`cohive.request_verify` 这类不存在的名字）。
- 设计稿给链接写了英文版地址（`data-repo-path-en`）但脚本没用上，英文页面点“贡献指南”仍进中文版。已改为随语言切换；“用 Docker 部署”“新成员接入”“加一种语言”也补了中英两个地址。

**贡献者区（GitHub 公开接口，未登录每 IP 每小时 60 次）**
- 失败时（限流 403、网络被挡、8 秒超时）显示空格子加一条“暂时无法读取贡献者，去 GitHub 查看 →”的链接，不报错、不白屏。本地用模拟 403 验证过。
- 成功结果在浏览器里缓存 1 小时，同一访客反复打开不再消耗额度。
- 插进页面的用户名、头像地址做了转义；人数统计去掉机器人账号。

**未用到的部分**
- 落地页本来就没有用产品截图，没有加；设计稿 `assets/screens/` 不入库。

## 本地预览

用 `python3 -m http.server` 起在 `site/`，内置浏览器检查过：
- 桌面 1440 宽、手机 375 宽都没有横向滚动；手机端菜单展开收起正常。
- 中英切换：标题、正文、链接都跟着换。
- 三个字体都从本地加载成功；品牌页 8 个 SVG 全部加载，深浅底、金底、单色几种展示正常。

截图没有入库，放在本组会话的共享笔记目录：`brand-1-desktop-en.jpg`（桌面英文首屏）、`brand-1-mobile-zh.jpg`（手机中文首屏）、`brand-1-brand-page.jpg`（品牌页）、`brand-1-readme-light.jpg` / `brand-1-readme-dark.jpg`（GitHub 上 README 浅色、深色）。

## 部署选项与建议（由 lee 决定，本次未部署）

| 方案 | 做法 | 优点 | 缺点 |
|---|---|---|---|
| Cloudflare Pages（建议） | 连 GitHub 仓库，构建命令留空，输出目录填 `site`；绑定自定义域名 | 不用写工作流；每条分支自动出预览地址；东南亚和国内访问通常比 github.io 稳；lee 已有 Cloudflare 账号 | 多一个平台的权限要管；Cloudflare 正把 Pages 往 Workers 静态资源迁，新项目也可以直接用 Workers 静态资源，做法类似 |
| GitHub Pages | 加一个工作流，用官方的上传与发布动作把 `site/` 发出去（分支发布只支持根目录或 `docs/`，不能直接选 `site/`） | 和仓库在一处，无第三方 | 要新增工作流；`github.io` 国内访问不稳 |

**子域名建议**（域名定了再落实）：
- 落地页放主域名，`www` 跳转到主域名；品牌页就是 `/brand`。
- 预留 `demo.` 给以后的演示实例，`docs.` 给以后的文档站；真实的 Cohive 服务地址不要和落地页共用一个子域名。
- 没定域名前，可以先用平台送的地址（`<项目名>.pages.dev` 或 `cohive-ai.github.io/cohive`）试运行，README 先不放链接。

## 需要 lee 决定

1. 部署方式与域名（见上）。部署后把 `docs/brand/README*.md` 里指向 `site/brand.html` 的相对链接换成线上地址——现在在 GitHub 上点开看到的是网页源码。
2. 品牌页色板沿用了设计稿的意大利语色名“Nero / Giallo”（夜黑 / 金黄）。这两个词本身是普通词，但兰博基尼的车漆正是“Giallo ××”“Nero ××”这种叫法，和“不借用”的原则有点贴边。`docs/brand` 说明里我用的是 Black / Gold / Bone / Carbon。建议品牌页也改成英文普通色名，一行就能改，等你定。
3. `docs/brand/` 和 `site/assets/brand/` 是两份相同的 SVG，目前靠说明里“两边一起改”约束。要不要加一个测试比对两份一致，由你定。

## 后续建议（本次没做）

- 交接说明待办第 5 项：首屏看板现在是网页代码模拟的，可换成真实看板录屏；产品本身是浅色界面，要统一品牌得另做深色主题。
- 交接说明待办第 6 项：中文标题没有宽体字，现在用苹方特粗；要更锋利可评估商用的方正、汉仪宽体（需要买授权，且字体文件不能放进公开仓库，只能走网页字体服务）。
- 页面没有分享卡片：缺 `og:image`、`og:title` 这类社交分享信息，发到群里或社交平台只会显示一行网址。建议做一张 1280×640 的黑金分享图，同时设成 GitHub 仓库的社交预览图（仓库设置里上传，不进代码）。
- 贡献者头像来自 GitHub 的头像服务，在网络被挡的地方头像会是空格子；接口失败已降级，头像加载失败目前没有单独处理。
