<template>
  <el-card shadow="hover" class="dryrun">
    <template #header>
      <div class="dryrun__header">
        <el-icon><CircleCheck v-if="report.summary.canProceed" /><Warning v-else /></el-icon>
        <span>{{ t('dryRunReport') }}</span>
        <el-tag :type="report.summary.canProceed ? 'success' : 'danger'" size="small">
          {{ report.summary.canProceed ? t('canProceed') : t('hasBlockingIssues') }}
        </el-tag>
      </div>
    </template>

    <!-- 问题统计概览 -->
    <div class="dryrun__summary">
      <el-tag type="danger" effect="dark">{{ t('errorCount', { n: report.summary.errorCount }) }}</el-tag>
      <el-tag type="warning" effect="dark">{{ t('warningCount', { n: report.summary.warningCount }) }}</el-tag>
      <el-tag type="info" effect="dark">{{ t('infoCount', { n: report.summary.infoCount }) }}</el-tag>
    </div>

    <!-- 问题列表 -->
    <el-table :data="report.issues" stripe size="small" max-height="400" class="dryrun__table">
      <el-table-column :label="t('severityCol')" width="100">
        <template #default="{ row }">
          <el-tag :type="severityType(row.severity)" size="small">
            {{ t(`severity.${row.severity}`) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('issueCol')" min-width="280">
        <template #default="{ row }">
          <div class="dryrun__issue-msg">{{ row.message }}</div>
          <div v-if="row.sample && row.sample.length" class="dryrun__issue-sample">
            <el-icon><DocumentCopy /></el-icon>
            {{ row.sample.join('，') }}
          </div>
        </template>
      </el-table-column>
      <el-table-column :label="t('affectedCol')" width="110" align="right">
        <template #default="{ row }">
          <span :class="{ 'dryrun__count--zero': row.count === 0 }">
            {{ formatNumber(row.count) }}
          </span>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  CircleCheck,
  DocumentCopy,
  Warning,
} from '@element-plus/icons-vue'
import type { DryRunReport } from '@/api'

defineProps<{ report: DryRunReport }>()
const { t } = useI18n()

function severityType(severity: string): 'danger' | 'warning' | 'info' {
  if (severity === 'error') return 'danger'
  if (severity === 'warning') return 'warning'
  return 'info'
}

function formatNumber(n: number): string {
  return n.toLocaleString('zh-CN')
}
</script>

<style scoped lang="scss">
.dryrun {
  margin-top: 20px;

  &__header {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
  }

  &__summary {
    display: flex;
    gap: 12px;
    margin-bottom: 16px;
  }

  &__table {
    width: 100%;
  }

  &__issue-msg {
    line-height: 1.5;
  }

  &__issue-sample {
    margin-top: 4px;
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: #909399;
  }

  &__count--zero {
    color: #c0c4cc;
  }
}
</style>
