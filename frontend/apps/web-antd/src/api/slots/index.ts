import { requestClient } from '#/api/request';

export interface SlotRow {
  createdAt: string;
  branch: string;
  duration: number; // 秒
  expireAt: string;
  id: number;
  lastBuildId: null | number;
  occupied: boolean;
  occupiedAt: string;
  occupiedBy: string;
  occupiedByUid: number;
  projectId: number;
  slotName: string;
  status: string;
}

export type SlotView =
  | { occupied: false; slotName: string }
  | {
      occupied: true;
      slot: SlotRow;
      slotName: string;
    };

export async function getSlotsApi(projectId: number) {
  return requestClient.get<SlotView[]>(`/slots/${projectId}`);
}

export async function occupySlotApi(
  projectId: number,
  data: {
    branch: string;
    durationUnit: 'days' | 'hours' | 'weeks';
    durationValue: number;
    slotName: string;
  },
) {
  return requestClient.post<SlotRow>(`/slots/${projectId}/occupy`, data);
}

export async function releaseSlotApi(projectId: number, slotName: string) {
  return requestClient.post(`/slots/${projectId}/${slotName}/release`);
}

export async function renewSlotApi(
  projectId: number,
  slotName: string,
  data: { durationUnit: string; durationValue: number },
) {
  return requestClient.post<SlotRow>(
    `/slots/${projectId}/${slotName}/renew`,
    data,
  );
}
