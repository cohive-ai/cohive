// 页面里用到的判断规则，单独放出来便于测试。
import { ApiError } from "./api";

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
