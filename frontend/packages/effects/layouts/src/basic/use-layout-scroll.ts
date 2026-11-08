import type { Router } from 'vue-router';

import { getCurrentInstance, nextTick, onScopeDispose } from 'vue';
import { useRouter } from 'vue-router';

import { getLayoutScrollElement } from '@vben-core/shared/utils';

type LayoutScrollRouter = Pick<Router, 'afterEach' | 'beforeEach'>;

function getHistoryPosition() {
  if (typeof window === 'undefined') {
    return undefined;
  }
  const position = (window.history.state as null | { position?: unknown })
    ?.position;
  return typeof position === 'number' ? position : undefined;
}

function getHashTarget(scrollElement: HTMLElement, hash: string) {
  if (!hash.startsWith('#')) {
    return null;
  }

  const id = hash.slice(1);
  try {
    return scrollElement.querySelector<HTMLElement>(
      `#${CSS.escape(decodeURIComponent(id))}`,
    );
  } catch {
    return scrollElement.querySelector<HTMLElement>(`#${CSS.escape(id)}`);
  }
}

export function useLayoutScroll(router?: LayoutScrollRouter) {
  // 布局滚动位置恢复是增强能力：拿不到 router 时打印告警并优雅跳过，
  // 绝不让布局挂掉（2026-11-09 白屏事故防御层——根因是 pnpm peer 组合
  // 分裂导致 vue-router 多副本，已在 .npmrc dedupe 修复；此处兜底）。
  const activeRouter = router ?? useRouter();
  if (!activeRouter) {
    const inst = getCurrentInstance();
    console.error(
      '[useLayoutScroll] router unavailable — hasInstance:',
      !!inst,
      'appHasRouter:',
      !!inst?.appContext?.config?.globalProperties?.$router,
    );
    return;
  }
  const scrollPositions = new Map<number, number>();
  let currentHistoryPosition = getHistoryPosition();
  let isHistoryNavigation = false;

  const removeBeforeGuard = activeRouter.beforeEach(() => {
    const scrollElement = getLayoutScrollElement();
    if (scrollElement && currentHistoryPosition !== undefined) {
      scrollPositions.set(currentHistoryPosition, scrollElement.scrollTop);
    }

    const nextHistoryPosition = getHistoryPosition();
    isHistoryNavigation =
      currentHistoryPosition !== undefined &&
      nextHistoryPosition !== undefined &&
      currentHistoryPosition !== nextHistoryPosition;
  });

  const removeAfterHook = activeRouter.afterEach(async (to, _from, failure) => {
    const nextHistoryPosition = getHistoryPosition();

    if (!failure) {
      await nextTick();
      const scrollElement = getLayoutScrollElement();
      if (scrollElement) {
        const savedPosition =
          isHistoryNavigation && nextHistoryPosition !== undefined
            ? scrollPositions.get(nextHistoryPosition)
            : undefined;

        if (savedPosition === undefined) {
          const hashTarget = getHashTarget(scrollElement, to.hash);
          if (hashTarget) {
            hashTarget.scrollIntoView({ behavior: 'smooth', block: 'start' });
          } else {
            scrollElement.scrollTo({ top: 0 });
          }
        } else {
          scrollElement.scrollTo({ top: savedPosition });
        }
      }
    }

    currentHistoryPosition = nextHistoryPosition;
    isHistoryNavigation = false;
  });

  onScopeDispose(() => {
    removeBeforeGuard();
    removeAfterHook();
  });
}
