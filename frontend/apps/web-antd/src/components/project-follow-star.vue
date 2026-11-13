<script setup lang="ts">
import { ref } from 'vue';

import { StarFilled, StarOutlined } from '@ant-design/icons-vue';

import { setProjectFollowApi } from '#/api/projects';

const props = defineProps<{ following: boolean; projectId: number }>();
const emit = defineEmits<{ change: [following: boolean] }>();
const saving = ref(false);

async function toggle() {
  if (saving.value) return;
  saving.value = true;
  try {
    const result = await setProjectFollowApi(props.projectId, !props.following);
    emit('change', result.following);
  } catch {
    // 请求拦截器显示错误。
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <a-tooltip :title="following ? '取消关注' : '关注项目'">
    <a-button
      :aria-pressed="following"
      aria-label="关注项目"
      :disabled="saving"
      size="small"
      type="text"
      @click.stop="toggle"
    >
      <StarFilled v-if="following" class="text-amber-500" />
      <StarOutlined v-else />
    </a-button>
  </a-tooltip>
</template>
