import { ref, watch } from 'vue';

/**
 * usePanel 抽屉/弹窗面板的开关 + 打开时加载数据（P6-M6 #33 composable 抽取）。
 * 覆盖各页面重复的「xxxOpen + watch(open) → load」三行式：
 *
 *   const panel = usePanel(loadFn)
 *   <a-drawer :open="panel.open.value" @close="panel.close" />
 *
 * open 为 ref（v-model 场景用 panel.openModel）；打开沿触发一次 load（不节流：
 * 每次打开都刷新是抽屉类面板的期望语义）。
 */
export function usePanel(loadFn?: () => Promise<unknown> | unknown) {
  const open = ref(false);

  function show() {
    open.value = true;
  }

  function close() {
    open.value = false;
  }

  watch(open, (v) => {
    if (v && loadFn) {
      Promise.resolve(loadFn()).catch(() => {
        // 加载失败由 loadFn 内部提示（各页面已有 message 处理习惯）；这里兜底防未捕获
      });
    }
  });

  return { close, open, show };
}
