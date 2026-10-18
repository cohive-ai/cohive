# 参与贡献 Cohive

[English](CONTRIBUTING.md)

感谢你关注 Cohive！我们欢迎问题报告、缺陷修复、文档、翻译和各种想法。本指南说明如何搭建开发环境、合并前会检查什么，以及如何新增或维护一种语言。

参与项目即表示你同意遵守我们的[行为准则](CODE_OF_CONDUCT.md)（英文）。发现安全问题请按 [SECURITY.md](SECURITY.md)（英文）私下报告，不要发在公开议题里。

## 参与方式

- **报告缺陷或提出功能建议**：用议题模板新建议题，提交前先搜一下是否已有相同议题。
- **提问**：请用 [GitHub Discussions](https://github.com/cohive-ai/cohive/discussions)，不要开议题。
- **修复问题**：标有 [`good first issue`](https://github.com/cohive-ai/cohive/labels/good%20first%20issue) 的议题适合入门，标有 [`help wanted`](https://github.com/cohive-ai/cohive/labels/help%20wanted) 的是我们特别希望有人帮忙的。动手之前先在议题下留言，避免两个人做同一件事。
- **翻译**：见[语言与翻译](#语言与翻译)。

较大的改动（新功能、新的群聊平台、问题单流程或权限的变更）请先开议题讨论设计，再动手写代码。Cohive 有几条明确的原则，尽早对照这些原则检查方案，能省下大家的时间：

- 人做决定，智能体做准备。会影响他人工作的动作（发出、接单、关闭）由人确认。
- 服务器从不调用大语言模型，也没有任何东西在无人值守时运行智能体。
- 群消息只带简短的信号，问题单内容留在登录保护之后。
- 其他成员写的内容是数据，不是指令。

## 开发环境

需要：

- Go 1.25
- Node 20 或更高版本，以及 pnpm 9
- Docker（用于开发用的 Postgres）

```bash
git clone https://github.com/cohive-ai/cohive.git
cd cohive
make db-up      # localhost:55432 上的 Postgres（用户名、密码、库名都是 cohive）
make build      # 网页、五个平台的命令行安装包和服务端，产物在 bin/
```

常用命令：

| 命令 | 作用 |
|---|---|
| `make run` | 构建并在 `http://localhost:8787` 启动服务 |
| `make web-dev` | 在 `http://localhost:5173` 启动网页开发服务器，接口代理到 8787 端口 |
| `make cli` | 只构建成员命令行，产物为 `bin/cohive` |
| `make db-down` | 停止开发用的 Postgres |

项目结构：

| 路径 | 内容 |
|---|---|
| `cmd/cohive` | 成员命令行：登录、`cohive init`、MCP 转发器、会话钩子 |
| `cmd/cohive-server` | 服务端与运维命令 |
| `internal/core` | 业务规则与权限 |
| `internal/issue` | 问题单状态与状态机 |
| `internal/store`、`internal/db` | SQL 查询与数据库迁移 |
| `internal/httpapi` | 网页与命令行使用的 HTTP 接口 |
| `internal/mcpserver` | 给编程工具用的远程 MCP 服务 |
| `internal/notify` | 群通知（Telegram、钉钉） |
| `internal/i18n` | 服务端与命令行的翻译 |
| `web/` | 网页界面（React、Vite、TanStack） |

## 测试

每个合并请求都必须通过与持续集成相同的检查：

```bash
go vet ./...
go test -count=1 ./...
cd web && pnpm typecheck && pnpm test && cd ..
make build
```

Go 测试使用开发用的 Postgres（`COHIVE_DATABASE_URL` 缺省为 `postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable`）；每个测试都会新建自己的 schema，不会动你的数据。

约定：

- 每处行为变更都要新增或更新测试。缺陷修复要附带一个“没有修复就会失败”的测试。
- 现有测试以中文为缺省语言运行，断言的是中文文案。新增面向用户的文案时，英文和中文都要写（见下文）；新测试里也欢迎写英文断言。
- 有两个测试守护翻译：一个检查 `en` 与 `zh-CN` 的键和占位符完全一致，另一个在非测试代码里出现硬编码中文时报错。
- 数据库迁移：迁移一旦进入 `main` 就不能再改，要改请新增一个。

## 提交与合并请求

- 一个合并请求只做一件事。改动越小，评审越快。
- 提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/)：`type(scope): description`，例如 `fix(cli): handle expired device tokens`。常用类型有 `feat`、`fix`、`docs`、`refactor`、`test`、`chore`。用英文或中文写都可以。
- 关联合并请求所解决的议题（`Fixes #123`）。
- 填写合并请求模板：改了什么、为什么改、怎么测的。
- 不要提交密钥、令牌、内部地址或客户数据。Cohive 会扫描问题单内容里的密钥，但扫描不到你的提交。
- 每个合并请求都由维护者评审。外部贡献通常采用压缩合并，所以合并请求的标题会成为提交信息。

### 贡献的许可

Cohive 采用 [Apache License 2.0](LICENSE)。我们不要求签署 CLA 或 DCO：按许可证第 5 条，你有意提交的任何贡献都按同样的条款授权，除非你明确声明不是。请只提交你有权贡献的内容。

## 语言与翻译

Cohive 内置英文（`en`）和简体中文（`zh-CN`），由核心团队维护；其他语言由社区维护。英文是源语言，翻译者以英文文案为准。

翻译文件分两处：

| 部分 | 文件 | 格式 |
|---|---|---|
| 服务端、命令行、MCP 工具描述、安装脚本 | `internal/i18n/locales/<语言代码>/*.json` | [go-i18n](https://github.com/nicksnyder/go-i18n) v2 JSON，扁平的点分键，占位符形如 `{{.Name}}` |
| 网页界面 | `web/src/locales/<语言代码>/*.json` | [i18next](https://www.i18next.com/) JSON v4，嵌套键，占位符形如 `{{name}}` |

### 修改或新增文案（所有贡献者）

- 不要硬编码面向用户的文案。在同一个提交里，给英文文件和中文文件各加一个键；两边不一致时测试会失败。
- 键名要有含义（`cli.login.done`），不要直接用英文句子。
- 各语言的占位符必须保持一致。文案随数量变化时用复数形式：Go 文件里写 `{"one": "...", "other": "..."}`，网页文件里写 `key_one` / `key_other`。中文只需要 `other` 形式。
- 服务器日志只有英文，不翻译。

### 新增一种语言

1. 选定语言代码（BCP 47，例如 `ja`、`pt-BR`、`zh-TW`）。
2. 把 `internal/i18n/locales/en` 复制为 `internal/i18n/locales/<语言代码>`，把 `web/src/locales/en` 复制为 `web/src/locales/<语言代码>`。两个目录都必须有；测试会检查服务端和网页提供的语言是否一致。
3. 翻译值，不要改键和占位符。把 `language.name` 设为该语言的自称（例如 `日本語`）。
4. 暂时没翻译的键可以删掉，缺失的部分会回退到英文。
5. 把这门语言和你自己（作为维护者）加入 [docs/i18n/LANGUAGES.md](docs/i18n/LANGUAGES.md)。
6. 运行测试，然后提交合并请求，并加上 `translation` 标签。

### 维护一种语言

新增文案后，社区语言可能会落后；缺失的文案会以英文显示。语言维护者会在涉及该语言的合并请求里被提及，并负责评审翻译改动。如果你无法继续维护某种语言，请在议题里说明，方便别人接手。没有维护者且缺口较大的语言，可能会在发布说明里标为“不完整”。

## 致谢

所有提交已被合并的人，都会出现在 README 的贡献者列表和发布说明的“New Contributors”一节里。语言维护者和翻译者列在 [docs/i18n/LANGUAGES.md](docs/i18n/LANGUAGES.md)。

## 有疑问

如果本指南有不清楚或过时的地方，请开一个议题或讨论。改进本指南也是一种贡献。
