import { requestClient } from '#/api/request';

/** AI 技能（P6-M3 /命令触发；声明式提示词模板 + runbook） */

export interface AiSkill {
  createdAt: string;
  description: string;
  enabled: boolean;
  id: number;
  name: string; // /name 触发名
  prompt: string; // {{q}} = 用户输入
  roles: string; // 逗号分隔 allowlist；空 = 全部角色
  runbook: string;
  title: string;
}

/** 可用技能列表（普通请求=本人角色可用；admin=1 管理视图全量） */
export async function listSkillsApi(admin = false) {
  return requestClient.get<AiSkill[]>('/ai/skills', {
    params: admin ? { admin: 1 } : undefined,
  });
}

export async function saveSkillApi(
  id: null | number,
  data: {
    description?: string;
    enabled?: boolean;
    name: string;
    prompt: string;
    roles?: string;
    runbook?: string;
    title: string;
  },
) {
  return id
    ? requestClient.put<AiSkill>(`/ai/skills/${id}`, data)
    : requestClient.post<AiSkill>('/ai/skills', data);
}

export async function deleteSkillApi(id: number) {
  return requestClient.delete(`/ai/skills/${id}`);
}
