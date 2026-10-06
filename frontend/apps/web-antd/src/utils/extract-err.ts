/**
 * 从 axios/fetch 错误中提取后端 message（v0.12.3 复核残留项：
 * 此前 19 处各自手写 `e?.response?.data?.message ?? 兜底`，口径不一）。
 * 后端错误约定见 httpx：Body{code, message}。
 */
export function extractErrMsg(error: unknown, fallback = '请求失败'): string {
  const err = error as
    | undefined
    | { message?: string; response?: { data?: { message?: string } } };
  // 空串视为未命中（多数原站点用 ||，后端空 message 应回退到通用文案）
  return err?.response?.data?.message || err?.message || fallback;
}
