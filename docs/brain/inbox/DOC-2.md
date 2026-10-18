# DOC-2 交回：开源文档三项定稿

- 分支：`docs/readme-english-default`（基于 origin/main `c74c96e`）
- 提交：改动与交回 `d78940f`，交回里改名历史说明的更正单独一个提交
- 未合入 main，未推送到 main

## D13：README 默认语言改为英文

- 文件改名：
  - `README.md`（中文）改名为 `README.zh-CN.md`；
  - `README.en.md` 改名为 `README.md`。
  - 都用 `git mv`。中文版用 `git log --follow README.zh-CN.md` 能接上原来 `README.md` 的历史。
  - 英文版占了 `README.md` 这个路径：在 Git 看来，这个路径还是原来那个文件，只是内容换成了英文，所以按路径查到的是原中文版的历史；英文版改名前的历史要查 `README.en.md`。这是两个文件对调名字必然出现的情况，不影响使用。
- 顶部语言切换链接：
  - 英文版 `README.md`：`[中文](README.zh-CN.md)`；
  - 中文版 `README.zh-CN.md`：`[English](README.md)`。
- 截图：中文版仍用 `docs/images/board.png`、`issue.png`（中文界面），英文版仍用 `board.en.png`、`issue.en.png`（英文界面）。改名不影响相对路径，两份逐个核对过。
- 全仓搜指向 `README.md`、`README.en.md` 的链接：
  - 除了两份 README 顶部互相指向的那一行，没有别处链接到这两个文件名。CONTRIBUTING、议题模板、合并请求模板、`cohive init`、安装脚本、Go 与网页代码、测试里都没有。
  - CONTRIBUTING 里两处“README 的贡献者列表”只是文字提及，不是链接，不用改。
  - 另有 `docs/superpowers/plans/2026-10-18-i18n-community.md` 和台账里提到旧文件名，是计划与记录里的历史描述，没改。
- 两份 README 里所有相对链接（指南、图片、许可证等）都检查过，目标文件都存在。

## D15：不签 CLA、DCO

- 核对 `CONTRIBUTING.md` 与 `CONTRIBUTING.zh-CN.md` 第 93 行：两版意思一致，都是“不要求签署 CLA 或 DCO，按 Apache 2.0 许可证第 5 条，有意提交的贡献按同样条款授权”。没有改。

## D18：行为准则举报邮箱

- `CODE_OF_CONDUCT.md` 第 41 行：
  - 占位的 “(reporting email to be announced)” 换成 `lee@anormlylee.com`，写成可点击的邮件链接；
  - 那条“举报邮箱待定（D18）”的注释已删除。
- 全仓再搜 “to be announced”“举报邮箱待定”，没有遗留。行为准则只有英文一份，没有中文版要同步。
- `SECURITY.md` 没有改，仍然走 GitHub 私密漏洞报告。

## 验证

- `go vet ./...`、`go test -count=1 ./...`（真实 Postgres，Go 1.26.9）、`make build`（含网页 17 个测试文件、80 条用例）全部通过。
- 守卫类测试都通过。仓库里没有测试引用 README 的文件名，改名不影响测试。

## 与派活要求的出入

无。
