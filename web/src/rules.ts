// 页面里用到的判断规则，单独放出来便于测试。
import { ApiError } from "./api";
import i18n from "./i18n";

// 轮询（例如等待 Telegram 连接结果）出错时是否继续：只有网络错误与服务端 5xx 值得重试，
// 其余 4xx（请求不存在、无权访问等）重试也不会好。
export function shouldRetryPoll(err: unknown): boolean {
  if (!(err instanceof ApiError)) return true;
  return err.status === 0 || err.status >= 500;
}

// 连接 Telegram 时只打开机器人链接，服务端返回别的地址一律不打开。
const telegramLink = /^https:\/\/t\.me\/[A-Za-z0-9_]{5,32}\?start=ln_[A-Za-z0-9_-]{1,64}$/;
export function isTelegramLink(url: unknown): url is string {
  return typeof url === "string" && telegramLink.test(url);
}

// 注册页查询邀请失败时的处理：invalid 表示邀请本身无效；busy 表示请求太频繁（稍后重试即可）；
// error 表示网络或服务问题（可重试）。
export function inviteLookupFailure(err: unknown): "invalid" | "busy" | "error" {
  if (!(err instanceof ApiError)) return "error";
  if (err.status === 429) return "busy";
  if (err.status >= 400 && err.status < 500) return "invalid";
  return "error";
}

// 名称列表的展示：中文用顿号，英文用逗号
export const joinNames = (names: string[]) =>
  new Intl.ListFormat(i18n.language, { type: "conjunction", style: "narrow" }).format(names);

// 吊销自己设备时的提示：唯一在用的设备吊销后，编程工具全都用不了，再配对还要管理员批准。
export function revokeDeviceMessage(name: string, onlyActive: boolean): string {
  const n = name || i18n.t("me:devices.unnamed");
  return onlyActive ? i18n.t("me:devices.revokeOnlyMessage", { name: n }) : i18n.t("me:devices.revokeMessage", { name: n });
}

// 设备授权页上说明“以哪个账号授权”，避免登错账号时把设备授权给别人。
export function authorizeAsText(account: { name: string; display_name?: string }): string {
  const who =
    account.display_name && account.display_name !== account.name
      ? i18n.t("auth:device.accountWho", { name: account.name, display: account.display_name })
      : account.name;
  return i18n.t("auth:device.authorizeAs", { who });
}

// 设备授权页上说明批准后这台电脑能用于哪些工作空间
export function deviceScopeText(workspaces: string[] | null | undefined): string {
  if (!workspaces || workspaces.length === 0) return i18n.t("auth:device.scopeNone");
  return i18n.t("auth:device.scopeList", { workspaces: joinNames(workspaces) });
}

// 管理操作只接受账号密码登录：免密登录（cohive web）打开管理页时先提示改用账号密码登录。
export function adminNeedsPasswordLogin(me: { login_method?: string }): boolean {
  return me.login_method !== "password";
}

// 安装命令行的命令：只从本服务下载（安装脚本会核对校验和）。origin 形如 https://cohive.example.com
// login 为 true 时装完接着执行 cohive login 配对这台电脑。
// 地址带 ?lang= 让服务器按当前界面语言渲染安装脚本；地址必须加单引号，否则 zsh 会把 ? 当通配符。
export function installCommands(origin: string, login = false): { unix: string; windows: string } {
  const o = origin.replace(/\/+$/, "");
  const lang = encodeURIComponent(i18n.language);
  const sh = `'${o}/install.sh?lang=${lang}'`;
  const ps = `'${o}/install.ps1?lang=${lang}'`;
  if (login) {
    return {
      unix: `curl -fsSL ${sh} | sh -s -- --login`,
      windows: `$env:COHIVE_LOGIN=1; irm ${ps} | iex`,
    };
  }
  return { unix: `curl -fsSL ${sh} | sh`, windows: `irm ${ps} | iex` };
}

// 在项目目录执行的初始化命令：只在一个工作空间时不用指定，多个时每个工作空间一条。
export function initCommands(workspaces: string[]): string[] {
  if (workspaces.length <= 1) return ["cohive init"];
  return workspaces.map((w) => `cohive init --workspace ${w}`);
}
