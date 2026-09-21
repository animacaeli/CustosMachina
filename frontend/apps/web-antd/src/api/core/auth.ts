import { baseRequestClient, requestClient } from '#/api/request';

export namespace AuthApi {
  /** 登录接口参数（超管账密登录） */
  export interface LoginParams {
    password?: string;
    username?: string;
  }

  /** 后端原始返回：双 token + user */
  export interface BackendLoginResult {
    accessToken: string;
    refreshToken: string;
    user: Record<string, any>;
  }

  /** vben 约定的登录返回 */
  export interface LoginResult {
    accessToken: string;
    refreshToken?: string;
  }

  /** GET /auth/permissions 返回 */
  export interface PermissionResult {
    permissions: string[];
    roles: string[];
  }
}

/**
 * 超管账密登录（扫码登录经回调落地页直接注入 token，不经此接口）
 */
export async function loginApi(data: AuthApi.LoginParams) {
  const result = await requestClient.post<AuthApi.BackendLoginResult>(
    '/auth/login',
    data,
  );
  return {
    accessToken: result.accessToken,
    refreshToken: result.refreshToken,
  } as AuthApi.LoginResult;
}

/**
 * 登出：吊销 refresh token（后端 FR2.3）。
 * /auth/logout 需认证，但必须用无拦截器的 baseRequestClient 手动带头：
 * 若走 requestClient，登出请求自身的 401（access 已过期场景）会再次触发
 * 刷新→失败→登出 的拦截器链路，形成无限循环。
 */
export async function logoutApi() {
  const { useAccessStore } = await import('@vben/stores');
  const accessStore = useAccessStore();
  const { accessToken, refreshToken } = accessStore;
  if (!accessToken || !refreshToken) {
    return;
  }
  try {
    await baseRequestClient.post(
      '/auth/logout',
      { refreshToken },
      { headers: { Authorization: `Bearer ${accessToken}` } },
    );
  } catch {
    // 吊销失败不阻塞前端登出（token 服务端自然过期兜底）
  }
}

/**
 * 刷新 token：用未过期的 refresh token 换新的 token 对（轮换）
 */
export async function refreshTokenApi(): Promise<{
  accessToken: string;
  refreshToken: string;
}> {
  const { useAccessStore } = await import('@vben/stores');
  const refreshToken = useAccessStore().refreshToken;
  if (!refreshToken) {
    throw new Error('无 refresh token');
  }
  return requestClient.post('/auth/refresh', { refreshToken });
}

/**
 * 获取用户权限码（FR3.3 后端下发）
 */
export async function getAccessCodesApi() {
  const result =
    await requestClient.get<AuthApi.PermissionResult>('/auth/permissions');
  return result.permissions;
}
