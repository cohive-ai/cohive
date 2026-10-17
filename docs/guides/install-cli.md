# 安装 cohive 命令行

成员电脑上的 `cohive` 负责配对电脑（`cohive login`）、初始化项目（`cohive init`），并作为编程工具的本机转发器（`cohive mcp-proxy`）。它由**团队自己的协作服务**提供下载：不需要装 Go，不需要能读代码仓库，版本与服务端一致。

网页“个人中心 → 绑定新电脑”里有带本服务地址的命令，可以直接复制。下文用 `https://cohive.example.com` 代表你们的服务地址。

## 安装

**macOS、Linux**（终端里执行）：

```bash
curl -fsSL https://cohive.example.com/install.sh | sh
```

**Windows**（PowerShell 里执行）：

```powershell
irm https://cohive.example.com/install.ps1 | iex
```

装完后按提示执行下一步：

```bash
cohive login --server https://cohive.example.com
```

**一条命令装好并配对**（个人中心里给的就是这一条）：装完接着执行 `cohive login`，在打开的浏览器里点“授权”即可。

```bash
curl -fsSL https://cohive.example.com/install.sh | sh -s -- --login
```

```powershell
$env:COHIVE_LOGIN=1; irm https://cohive.example.com/install.ps1 | iex
```

没有可交互的终端时（例如在脚本里执行），只安装，并提示手动执行 `cohive login`。

升级：再执行一次安装命令即可，会原地替换。`cohive version` 查看本机版本；`cohive login`、`cohive status` 发现本机版本与服务端不一致时会提醒，并给出重装命令（只提醒，不影响使用）。

## 装到哪里

| 系统 | 位置 | PATH |
|---|---|---|
| macOS、Linux | `~/.local/bin/cohive`；可用 `COHIVE_INSTALL_DIR` 指定 | 不改 shell 配置文件。目录不在 PATH 里时，脚本会打印要加进 `~/.zshrc` 或 `~/.bashrc` 的那一行 |
| Windows | `%LOCALAPPDATA%\Programs\cohive\cohive.exe`；可用 `COHIVE_INSTALL_DIR` 指定 | 自动加入当前用户的 PATH（不需要管理员权限）；已打开的终端要重开 |

**编程工具要能在 PATH 里找到 `cohive`**：`cohive init` 写进项目配置的命令就是 `cohive`（配置要提交进仓库，不能写某个人的绝对路径）。编程工具不是从终端启动时（例如从程序坞打开桌面应用），它拿到的 PATH 可能不含 `~/.local/bin`。遇到“找不到 cohive”时，从终端启动编程工具，或把目录加进登录 shell 的 PATH。`cohive init` 发现当前 PATH 里没有 `cohive` 时也会提示。

## 支持的平台

macOS（Apple 芯片、Intel）、Linux（x86-64、arm64）、Windows（x64）。安装包在构建服务端时一并生成（`make build` 里的 `make cli-dist`），没有附带安装包的服务器会在网页和安装脚本里提示“请联系管理员”。

## 安全说明

- **只从本服务下载**：脚本里的下载地址由服务端按你这次访问的地址写入，不会指向别处；安装前核对 SHA-256，不符就删除下载的文件、不安装。
- **校验和防什么、不防什么**：校验和与程序来自同一台服务器，能发现传输中损坏、被截断、下错平台；**不能**防“服务器本身被攻破”。真正的来源认证要靠签名，等仓库公开发布时再做。
- **用 HTTPS**：经 HTTPS 访问时传输由 TLS 保护。`http://` 地址只应在内网使用，脚本会提醒。
- **不放心 `curl | sh`**：先下载、看过再执行：

  ```bash
  curl -fsSLo install.sh https://cohive.example.com/install.sh
  less install.sh
  sh install.sh
  ```

  也可以手动下载：`https://cohive.example.com/dl/cohive/<平台>`（平台为 `darwin-arm64`、`darwin-amd64`、`linux-amd64`、`linux-arm64`、`windows-amd64`），用 `https://cohive.example.com/dl/cohive/SHA256SUMS` 核对。
- **macOS 不会弹“无法验证开发者”**：用 `curl` 下载的文件不带隔离属性，未签名的程序可以直接运行；经浏览器下载的会被系统拦下，所以只推荐用安装脚本。
- **下载不需要登录**：命令行不含任何秘密，不配对什么也做不了；新同事在拿到账号前就要能装。
- **不需要管理员权限**，也不在后台常驻任何进程。

## 卸载

删除安装位置的 `cohive`（Windows 还可以从用户 PATH 里去掉那个目录），以及凭据目录 `~/.config/cohive/`。建议先在网页“个人中心 → 我的设备”里吊销这台电脑。
