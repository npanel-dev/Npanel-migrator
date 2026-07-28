<template>
  <el-card shadow="hover" class="progress-card">
    <template #header>
      <div class="progress-card__header">
        <el-icon>
          <Loading v-if="status === 'running'" class="is-loading" />
          <CircleCheckFilled v-else-if="status === 'completed'" />
          <CircleCloseFilled v-else-if="status === 'failed'" />
          <VideoPause v-else-if="status === 'cancelled'" />
          <RemoveFilled v-else />
        </el-icon>
        <span>{{ t('importProgress') }}</span>
        <el-tag :type="statusTagType" size="small">{{ statusLabel }}</el-tag>
      </div>
    </template>

    <!-- 当前阶段 + 进度条 -->
    <div class="progress-card__phase">{{ snapshot.phaseLabel || t('phaseIdle') }}</div>
    <el-progress
      :percentage="percentage"
      :status="progressStatus"
      :stroke-width="18"
      :text-inside="true"
    />

    <!-- 统计 -->
    <el-row :gutter="16" class="progress-card__stats">
      <el-col :xs="12" :sm="6">
        <div class="progress-card__stat">
          <span class="progress-card__stat-label">{{ t('processed') }}</span>
          <span class="progress-card__stat-value">{{ formatNumber(snapshot.done) }} / {{ formatNumber(snapshot.total) }}</span>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="progress-card__stat">
          <span class="progress-card__stat-label">{{ t('errors') }}</span>
          <span class="progress-card__stat-value" :class="{ 'is-error': snapshot.errors > 0 }">
            {{ formatNumber(snapshot.errors) }}
          </span>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="progress-card__stat">
          <span class="progress-card__stat-label">{{ t('elapsed') }}</span>
          <span class="progress-card__stat-value">{{ elapsed }}</span>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="progress-card__stat">
          <span class="progress-card__stat-label">{{ t('speedAndEta') }}</span>
          <span class="progress-card__stat-value progress-card__stat-value--compact">
            {{ speed }} · {{ eta }}
          </span>
        </div>
      </el-col>
    </el-row>

    <div v-if="status === 'running' || snapshot.resumable" class="progress-card__actions">
      <el-button
        v-if="status === 'running'"
        type="warning"
        plain
        :disabled="snapshot.cancelRequested"
        @click="emit('cancel')"
      >
        {{ snapshot.cancelRequested ? t('cancelPending') : t('safeCancel') }}
      </el-button>
      <el-button
        v-if="snapshot.resumable && status !== 'running'"
        type="primary"
        @click="emit('resume')"
      >
        {{ t('resumeMigration') }}
      </el-button>
    </div>

    <!-- 完成消息 -->
    <el-alert
      v-if="snapshot.message && ['completed', 'failed', 'cancelled'].includes(status)"
      :title="snapshot.message"
      :type="status === 'completed' ? 'success' : status === 'cancelled' ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="progress-card__message"
    />
  </el-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CircleCheckFilled,
  CircleCloseFilled,
  Loading,
  RemoveFilled,
  VideoPause,
} from '@element-plus/icons-vue'
import type { ProgressSnapshot } from '@/api'

const props = defineProps<{ snapshot: ProgressSnapshot }>()
const emit = defineEmits<{
  cancel: []
  resume: []
}>()
const { t } = useI18n()

const status = computed(() => props.snapshot.status)
const percentage = computed(() => {
  if (props.snapshot.total <= 0) return 0
  return Math.min(100, Math.round((props.snapshot.done / props.snapshot.total) * 100))
})

const progressStatus = computed<'success' | 'exception' | undefined>(() => {
  if (status.value === 'completed') return 'success'
  if (status.value === 'failed') return 'exception'
  return undefined
})

const statusTagType = computed<'success' | 'danger' | 'warning' | 'info'>(() => {
  if (status.value === 'completed') return 'success'
  if (status.value === 'failed') return 'danger'
  if (status.value === 'cancelled') return 'warning'
  if (status.value === 'running') return 'warning'
  return 'info'
})

const statusLabel = computed(() => t(`importStatus.${status.value}`))

const elapsed = computed(() => {
  if (!props.snapshot.startedAt) return '-'
  const start = new Date(props.snapshot.startedAt).getTime()
  const end = props.snapshot.finishedAt
    ? new Date(props.snapshot.finishedAt).getTime()
    : Date.now()
  const sec = Math.floor((end - start) / 1000)
  if (sec < 60) return `${sec}s`
  return `${Math.floor(sec / 60)}m ${sec % 60}s`
})

const speed = computed(() => {
  if (!props.snapshot.ratePerSecond || props.snapshot.ratePerSecond <= 0) return '-'
  return `${props.snapshot.ratePerSecond.toFixed(1)}/s`
})

const eta = computed(() => {
  const seconds = props.snapshot.etaSeconds
  if (!seconds || seconds <= 0) return t('etaCalculating')
  if (seconds < 60) return `ETA ${seconds}s`
  if (seconds < 3600) return `ETA ${Math.ceil(seconds / 60)}m`
  return `ETA ${Math.floor(seconds / 3600)}h ${Math.ceil((seconds % 3600) / 60)}m`
})

function formatNumber(n: number): string {
  return n.toLocaleString('zh-CN')
}
</script>

<style scoped lang="scss">
.progress-card {
  margin-top: 20px;

  &__header {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
  }

  &__phase {
    margin-bottom: 12px;
    color: #303133;
    font-size: 15px;
    font-weight: 500;
  }

  &__stats {
    margin-top: 20px;
  }

  &__stat {
    text-align: center;

    &-label {
      display: block;
      font-size: 12px;
      color: #909399;
      margin-bottom: 4px;
    }

    &-value {
      font-size: 18px;
      font-weight: 600;
      color: #303133;

      &.is-error {
        color: #f56c6c;
      }

      &--compact {
        font-size: 15px;
      }
    }
  }

  &__actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 16px;
  }

  &__message {
    margin-top: 16px;
  }
}

.is-loading {
  animation: rotating 1.5s linear infinite;
}

@keyframes rotating {
  from { transform: rotate(0); }
  to { transform: rotate(360deg); }
}
</style>
