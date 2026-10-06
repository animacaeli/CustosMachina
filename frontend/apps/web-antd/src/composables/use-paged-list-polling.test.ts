import { ref } from 'vue';

import { describe, expect, it, vi } from 'vitest';

import { usePagedListPolling } from './use-paged-list-polling';

/**
 * use-paged-list-polling 行为测试（v0.12.3 复核：前端业务测试此前仅 1 文件）。
 * 用假定时器驱动轮询，断言三条关键契约：
 * ①宿主不可见（active()=false）时轮询停表——v0.12.1 复核修的缺陷有回归防线；
 * ②shouldPoll 为真时按 interval 拉取；
 * ③手动 load 重置 loading 且写入 items/total。
 */
describe('usePagedListPolling', () => {
  it('active=false 的轮询周期内直接停表且不再请求', async () => {
    vi.useFakeTimers();
    const open = ref(false);
    const fetcher = vi.fn().mockResolvedValue({ items: [{ id: 1 }], total: 1 });
    const c = usePagedListPolling({
      active: () => open.value,
      fetch: fetcher,
      interval: 3000,
      shouldPoll: () => true,
    });

    open.value = true;
    await c.load(); // 触发 maybePoll
    expect(fetcher).toHaveBeenCalledTimes(1);

    // 关闭宿主：下一个周期应停表（不再发请求）
    open.value = false;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetcher).toHaveBeenCalledTimes(1);

    vi.useRealTimers();
  });

  it('宿主可见且 shouldPoll 命中时按 interval 持续拉取', async () => {
    vi.useFakeTimers();
    const open = ref(true);
    const fetcher = vi.fn().mockResolvedValue({ items: [], total: 0 });
    const c = usePagedListPolling({
      active: () => open.value,
      fetch: fetcher,
      interval: 3000,
      shouldPoll: () => true,
    });
    await c.load();
    await vi.advanceTimersByTimeAsync(3000);
    await vi.advanceTimersByTimeAsync(3000);
    expect(fetcher.mock.calls.length).toBeGreaterThanOrEqual(3); // load + 2 轮询
    vi.useRealTimers();
  });

  it('load 写入 items/total 并复位 loading', async () => {
    const open = ref(true);
    const c = usePagedListPolling<{ id: number }>({
      active: () => open.value,
      fetch: async () => ({ items: [{ id: 7 }], total: 42 }),
    });
    await c.load();
    expect(c.items.value).toEqual([{ id: 7 }]);
    expect(c.total.value).toBe(42);
    expect(c.loading.value).toBe(false);
  });
});
