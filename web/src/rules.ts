// 页面里用到的判断规则，单独放出来便于测试。
import { ApiError } from "./api";

// 轮询（例如等待 Telegram 连接结果）出错时是否继续：只有网络错误与服务端 5xx 值得重试，
// 其余 4xx（请求不存在、无权访问等）重试也不会好。
export function shouldRetryPoll(err: unknown): boolean {
  if (!(err instanceof ApiError)) return true;
  return err.status === 0 || err.status >= 500;
}
