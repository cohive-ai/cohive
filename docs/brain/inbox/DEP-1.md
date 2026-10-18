# DEP-1 交回：升级到 Go 1.26，合入依赖升级（cohive-ai/cohive#2）

- 分支：`chore/go-1.26`（基于 origin/main `64169f3`）
- 提交：
  - 合并机器人分支 `2867b40`：机器人的提交 `f90fdea` 原样保留，合入 main 后 #2 会显示为已合并；
  - 镜像与文档 `d631b98`；
  - 工具链 `1c27dde`；
  - 本交回单独一个提交，在它们之后。
- 未合入 main，未推送到 main，测试服没有动。

## 做了什么

1. **合入 #2**：用不快进合并的方式合入 `origin/dependabot/go_modules/go-102e503fc9`，没有重写机器人的提交。
   - 依赖：x/crypto 0.42.0 升到 0.58.0；x/sys 0.49.0、x/text 0.43.0、x/sync 0.24.0 一起升；
   - `go` 指令从 1.25.0 升到 1.26.0。
2. **Dockerfile**：构建镜像改为 `golang:1.26-alpine`。
3. **文档里的“Go 1.25”改为 1.26**，只改版本号那一行：`README.md`、`README.en.md`、`CONTRIBUTING.md`、`CONTRIBUTING.zh-CN.md`、`docs/guides/single-host.md`、`docs/guides/single-host.en.md`、`docs/guides/m1-walkthrough.md`。
   - 全仓再搜一遍，只剩两处没改：
     - `docs/brain/ledger.md`：台账只读，写的是派活内容；
     - `docs/superpowers/plans/2026-10-18-i18n-community.md`：多语言会话的计划，属于历史描述。
   - `.github/workflows/ci.yml` 用的是 `go-version-file`，没改。
4. **派活之外加的一项：`go.mod` 指定工具链 `go1.26.9`**，见下一节“发现的问题”。

## 发现的问题：Go 1.26.0 本身有 29 个会被调用到的标准库漏洞

- 本机原有的 Go 是 1.25.0，自动切换工具链时，按 `go 1.26.0` 下载的是 **1.26.0**。
- 用 govulncheck（最新版，用 1.26 编译）扫描：
  - **Go 1.26.0 下**：代码会调用到的标准库漏洞有 29 个，涉及 net/url、crypto/x509、os 等；钉钉长连接、网页登录、HTTP 服务都在调用链上。
  - **修复情况**：都在 1.26.1 到 1.26.9 之间修复。目前 1.26 的最新补丁版是 **1.26.9**（另有 1.27.2）。
- 处理：在 `go.mod` 加 `toolchain go1.26.9`。
  - 最低要求仍是 `go 1.26.0`，没有抬高；
  - 自动切换工具链的机器（本机、开发者）会改用 1.26.9。
- **复扫（1.26.9）**：
  - 代码会调用到的漏洞：**0 个**。
  - 依赖模块里只剩 1 个：GO-2026-5932，是 x/crypto/openpgp 子包“已停止维护、不安全”，**没有修复版本**。我们没有导入这个子包，不受影响。
  - 按二进制扫描 `bin/cohive-server`，结论相同。
- **Docker 镜像不受影响**：`golang:1.26-alpine` 当前就是 1.26.9，镜像里的程序实测是 go1.26.9 编译的。
- **CI 未核实**：CI 用 `actions/setup-go@v7` 加 `go-version-file: go.mod`。会不会按 `toolchain` 行装 1.26.9，我没有核实。这个分支的推送不触发 CI（只在合并请求和 main 上跑），合入前开合并请求时可以看 CI 日志里的 Go 版本。

## 验证

| 项 | 结果 | 实际 Go 版本 |
|---|---|---|
| `go vet ./...` | 通过 | 1.26.0（加 toolchain 行前），1.26.9（加之后），两遍都跑了 |
| `go test -count=1 ./...`（真实 Postgres `localhost:55432`） | 19 个包全部通过 | 1.26.0，1.26.9，两遍都过 |
| `make build`（含网页 16 个测试文件、74 条用例，五个平台的命令行安装包） | 通过 | 1.26.9，`go version bin/cohive-server` 为 go1.26.9 |
| 本机 `docker build` | 成功，加 toolchain 行前后各构建一次 | 镜像里的程序是 go1.26.9 编译的（从镜像里取出程序用 `go version` 核对） |
| `docker compose up` | 成功 | — |

compose 的细节：
- 用独立的项目名 `cohive-dep1`、端口 8796、临时环境文件，Postgres 17 加服务。
- 启动日志正常：迁移完成、初始管理员已生成、五个平台的安装包、版本号与提交号正确，健康检查返回 ok。
- 用完已执行 `docker compose down -v`，容器、网络、两个卷（`cohive-dep1_data`、`cohive-dep1_pgdata`）都删了；走查镜像 `cohive:dep1-try` 和为核对版本拉下来的 `golang:1.26-alpine` 也删了；临时环境文件已删。

## 告警覆盖与编译进去的子包

- **告警**：Go 侧 15 条告警（编号 #1 到 #15），全是 golang.org/x/crypto。
  - #1、#2 首个修复版本 0.45.0；
  - #3 到 #15 首个修复版本 0.52.0；
  - 升级后是 0.58.0，**全部覆盖**。
  - 依赖机器人会在合入 main 后自动关闭它们，我没有手动去关。
- **编译进去的 x/crypto 子包**（`go list -deps`）：
  - `cohive-server`：来自 x/crypto 模块的仍然**只有 bcrypt、blowfish**。
  - 列表里另有 `vendor/golang.org/x/crypto/...`（cryptobyte、chacha20、chacha20poly1305、poly1305 等），是 Go 标准库自带的副本，供 TLS 使用，随 Go 版本走，不是这个模块；成员本机的 `cohive` 命令行也只有这类标准库副本。

## 与派活要求的出入

1. **加了 `toolchain go1.26.9`**：派活里没有。理由见上：不加的话，自动切换工具链会用 1.26.0，编出来的程序（包括 `make build` 打包的命令行安装包）带着 29 个已修复的标准库漏洞。如果不想在 go.mod 里钉补丁版，可以去掉这一行，改在文档里要求“1.26.9 以上”，但自动切换工具链就不会帮忙升级了。
2. **Dockerfile 仍用浮动的 `golang:1.26-alpine`**，没有钉到 `1.26.9-alpine`：浮动标签能自动跟上以后的补丁版，目前就是 1.26.9。
3. CI 会不会按 `toolchain` 行装 1.26.9，未核实，见上。
