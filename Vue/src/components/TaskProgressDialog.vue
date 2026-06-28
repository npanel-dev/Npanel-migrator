<template>
  <el-dialog
    v-model="visible"
    :title="title"
    width="640px"
    :close-on-click-modal="false"
    :close-on-press-escape="!running"
    :show-close="!running"
    align-center
    @close="onClose"
  >
    <!-- 当前阶段 + 进度条 -->
    <div class="task__phase">
      <el-icon v-if="running" class="task__spin"><Loading /></el-icon>
      <el-icon v-else-if="status === 'completed'" color="#67c23a"><CircleCheckFilled /></el-icon>
      <el-icon v-else-if="status === 'failed'" color="#f56c6c"><CircleCloseFilled /></el-icon>
      <span>{{ snapshot.phaseLabel || t('phaseIdle') }}</span>
    </div>
    <el-progress
      :percentage="percentage"
      :status="progressStatus"
      :stroke-width="14"
      :text-inside="true"
    />

    <!-- 统计 -->
    <div class="task__stats">
      <span>{{ t('processed') }}: {{ snapshot.done }}/{{ snapshot.total }}</span>
      <span v-if="snapshot.errors > 0" class="task__err">
        {{ t('errors') }}: {{ snapshot.errors }}
      </span>
      <span class="task__elapsed">{{ elapsed }}</span>
    </div>

    <!-- 实时日志 -->
    <div class="task__logs">
      <div class="task__logs-title">{{ t('realtimeLogs') }}</div>
      <div class="task__logs-body" ref="logsBody">
        <div
          v-for="(log, i) in snapshot.logs"
          :key="i"
          class="task__log"
          :class="`task__log--${log.level}`"
        >
          <span class="task__log-time">{{ formatTime(log.time) }}</span>
          <span class="task__log-level">[{{ log.level.toUpperCase() }}]</span>
          <span class="task__log-msg">{{ log.message }}</span>
        </div>
        <div v-if="snapshot.logs.length === 0" class="task__log-empty">{{ t('noLogs') }}</div>
      </div>
    </div>

    <!-- 完成消息 -->
    <el-alert
      v-if="snapshot.message && !running"
      :title="snapshot.message"
      :type="status === 'completed' ? 'success' : 'error'"
      :closable="false"
      show-icon
      class="task__alert"
    />

    <template #footer>
      <el-button v-if="!running" type="primary" @click="onClose">{{ t('close') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CircleCheckFilled,
  CircleCloseFilled,
  Loading,
} from '@element-plus/icons-vue'
import type { TaskProgressSnapshot } from '@/api'

const props = defineProps<{
  modelValue: boolean
  title: string
  snapshot: TaskProgressSnapshot
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'close'): void
}>()

const { t } = useI18n()
const logsBody = ref<HTMLElement>()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const status = computed(() => props.snapshot.status)
const running = computed(() => status.value === 'running')
const percentage = computed(() => {
  if (props.snapshot.total <= 0) return running.value ? 30 : 100
  return Math.min(100, Math.round((props.snapshot.done / props.snapshot.total) * 100))
})
const progressStatus = computed<'success' | 'exception' | undefined>(() => {
  if (status.value === 'completed') return 'success'
  if (status.value === 'failed') return 'exception'
  return undefined
})

const elapsed = computed(() => {
  if (!props.snapshot.startedAt) return ''
  const start = new Date(props.snapshot.startedAt).getTime()
  const end = props.snapshot.finishedAt
    ? new Date(props.snapshot.finishedAt).getTime()
    : Date.now()
  const sec = Math.floor((end - start) / 1000)
  if (sec < 60) return `${sec}s`
  return `${Math.floor(sec / 60)}m ${sec % 60}s`
})

// 日志新增时自动滚动到底部。
watch(
  () => props.snapshot.logs.length,
  async () => {
    await nextTick()
    if (logsBody.value) {
      logsBody.value.scrollTop = logsBody.value.scrollHeight
    }
  },
)

function formatTime(time: string): string {
  try {
    return new Date(time).toLocaleTimeString('zh-CN', { hour12: false })
  } catch {
    return ''
  }
}

function onClose() {
  emit('update:modelValue', false)
  emit('close')
}
</script>

<style scoped lang="scss">
.task {
  &__phase {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 15px;
    font-weight: 500;
    margin-bottom: 12px;
  }

  &__spin {
    animation: task-spin 1.5s linear infinite;
  }

  &__stats {
    display: flex;
    gap: 20px;
    margin-top: 14px;
    font-size: 13px;
    color: #606266;
  }

  &__err {
    color: #f56c6c;
  }

  &__elapsed {
    margin-left: auto;
  }

  &__logs {
    margin-top: 18px;
  }

  &__logs-title {
    font-size: 13px;
    color: #909399;
    margin-bottom: 8px;
  }

  &__logs-body {
    height: 240px;
    overflow-y: auto;
    background: #1e1e1e;
    border-radius: 6px;
    padding: 10px 12px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 12px;
    line-height: 1.7;
  }

  &__log {
    display: flex;
    gap: 6px;

    &-time {
      color: #6a6a6a;
      flex-shrink: 0;
    }

    &-level {
      flex-shrink: 0;
      width: 56px;
    }

    &-msg {
      color: #d4d4d4;
      word-break: break-all;
    }

    &--info .task__log-level {
      color: #569cd6;
    }
    &--warn .task__log-level {
      color: #dcdcaa;
    }
    &--warn .task__log-msg {
      color: #dcdcaa;
    }
    &--error .task__log-level {
      color: #f48771;
    }
    &--error .task__log-msg {
      color: #f48771;
    }
  }

  &__log-empty {
    color: #6a6a6a;
    font-style: italic;
  }

  &__alert {
    margin-top: 16px;
  }
}

@keyframes task-spin {
  from { transform: rotate(0); }
  to { transform: rotate(360deg); }
}
</style>
