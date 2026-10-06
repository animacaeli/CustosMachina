import type { Ref } from 'vue';

import { onBeforeUnmount, ref } from 'vue';

/**
 * P8-M2（#33 销账）：环境抽屉"分页列表 + 条件轮询"的通用 composable。
 * build/cron 等抽屉原本各自维护 items/page/timer/document.hidden 守卫，
 * 形态同构——收敛于此。release-drawer 轮询语义特殊（发布互斥+活跃色双
 * 计时器）不强抽。
 */
export interface PagedPollingOptions<T> {
  /** 拉取一页（抛错由内部吞掉并停轮询防雪崩） */
  fetch: (
    page: number,
    size: number,
  ) => Promise<{ items?: T[]; total?: number }>;
  /** 轮询条件（默认 false = 不轮询；如"列表含 running"） */
  shouldPoll?: (items: T[]) => boolean;
  /** 轮询间隔 ms（默认 3000） */
  interval?: number;
  /** 页大小（默认 20） */
  size?: number;
  /** 抽屉等可见性开关：false 时暂停轮询 */
  active: () => boolean;
  /** 静默刷新（轮询不触发 loading；手动 load 触发） */
  onPageChange?: () => void;
}

export function usePagedListPolling<T>(options: PagedPollingOptions<T>) {
  const items: Ref<T[]> = ref([]);
  const total = ref(0);
  const page = ref(1);
  const loading = ref(false);
  const size = options.size ?? 20;
  let timer: null | ReturnType<typeof setInterval> = null;

  function stopPoll() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  function maybePoll() {
    stopPoll();
    if (!options.active() || !options.shouldPoll?.(items.value)) return;
    timer = setInterval(async () => {
      // 页面不可见或宿主已不可见（抽屉关闭）都暂停——只判 document.hidden
      // 会在关抽屉后继续每 interval 请求一次（v0.12.1 复核）；
      // 宿主不可见时直接停表，重开由调用方 load() 恢复（v0.12.2 复核）
      if (!options.active()) {
        stopPoll();
        return;
      }
      if (document.hidden) return;
      try {
        const res = await options.fetch(page.value, size);
        items.value = res.items ?? [];
        total.value = res.total ?? 0;
        if (!options.shouldPoll?.(items.value)) stopPoll();
      } catch {
        stopPoll(); // 网络异常停止，避免雪崩
      }
    }, options.interval ?? 3000);
  }

  async function load() {
    loading.value = true;
    try {
      const res = await options.fetch(page.value, size);
      items.value = res.items ?? [];
      total.value = res.total ?? 0;
    } finally {
      loading.value = false;
    }
    maybePoll();
  }

  function turnPage(p: number) {
    page.value = p;
    options.onPageChange?.();
    load();
  }

  onBeforeUnmount(stopPoll);

  return { items, total, page, loading, load, turnPage, stopPoll, maybePoll };
}
