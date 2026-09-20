import { requestClient } from '#/api/request';

/** 平台用户（后端 identity.User，passwordHash 已剔除） */
export interface PlatformUser {
  createdAt: string;
  displayName: string;
  id: number;
  isLocalAdmin: boolean;
  roles: string;
  status: 'active' | 'disabled';
  username: string;
}

/** 用户列表 */
export async function getUserListApi() {
  return requestClient.get<PlatformUser[]>('/users');
}

/** 创建用户 */
export async function createUserApi(data: {
  displayName: string;
  roles?: string;
  username?: string;
}) {
  return requestClient.post<PlatformUser>('/users', data);
}

/** 调整用户角色 */
export async function updateUserRolesApi(id: number, roles: string) {
  return requestClient.put<PlatformUser>(`/users/${id}/roles`, { roles });
}
