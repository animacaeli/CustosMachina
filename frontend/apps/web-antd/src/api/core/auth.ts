import { baseRequestClient, requestClient } from '#/api/request';

export namespace AuthApi {
  /** 登录接口参数 */
  export interface LoginParams {
    password?: string;
    username?: string;
  }

  /** 后端原始返回：{token, user} */
  export interface BackendLoginResult {
    token: string;
    user: Record<string, any>;
  }

  /** vben 约定的登录返回 */
  export interface LoginResult {
    accessToken: string;
  }

  /** GET /auth/permissions 返回 */
  export interface PermissionResult {
    permissions: string[];
    roles: string[];
  }
}

/**
 * 登录（后端返回 {token,user}，映射为 vben 约定的 accessToken）
 */
export async function loginApi(data: AuthApi.LoginParams) {
  const result = await requestClient.post<AuthApi.BackendLoginResult>(
    '/auth/login',
    data,
  );
  return { accessToken: result.token } as AuthApi.LoginResult;
}

/**
 * 登出：后端为无状态 JWT，由前端清空会话即可（此请求失败会被 store 吞掉）
 */
export async function logoutApi() {
  return baseRequestClient.post('/auth/logout');
}

/**
 * 刷新 token：后端暂未实现（无状态 JWT，过期重登录），
 * 保留签名以满足 request.ts 引用，调用即抛错。
 */
export async function refreshTokenApi(): Promise<{ data: string }> {
  throw new Error('token 刷新未启用');
}

/**
 * 获取用户权限码（FR3.3 后端下发）
 */
export async function getAccessCodesApi() {
  const result =
    await requestClient.get<AuthApi.PermissionResult>('/auth/permissions');
  return result.permissions;
}
