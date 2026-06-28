<template>
  <el-card shadow="hover" class="report">
    <template #header>
      <div class="report__header">
        <el-icon><DataAnalysis /></el-icon>
        <span>{{ t('detectReport') }}</span>
        <el-tag type="success" size="small">{{ report.panel }}</el-tag>
        <el-tag type="info" size="small">
          {{ t('coreTablesTotalRows', { rows: formatNumber(report.totalRows) }) }}
        </el-tag>
      </div>
    </template>

    <!-- 关键业务指标 -->
    <h4 class="report__subtitle">{{ t('businessMetrics') }}</h4>
    <el-row :gutter="16">
      <el-col :span="6" v-for="m in metricCards" :key="m.label">
        <el-statistic :title="m.label" :value="m.value" :formatter="m.formatter">
          <template v-if="m.suffix" #suffix>
            <span class="report__suffix">{{ m.suffix }}</span>
          </template>
        </el-statistic>
      </el-col>
    </el-row>

    <el-divider />

    <!-- 数据表分布：左侧饼图 + 右侧明细表 -->
    <div class="report__tables-section">
      <h4 class="report__subtitle">{{ t('tableRowsDetail') }}</h4>

      <el-row :gutter="20">
        <!-- 左侧：饼图 -->
        <el-col :span="10">
          <div class="report__chart" ref="chartRef"></div>
        </el-col>

        <!-- 右侧：明细表 -->
        <el-col :span="14">
          <el-table
            :data="report.tables"
            stripe
            size="small"
            max-height="320"
            :row-class-name="rowClassName"
          >
            <el-table-column :label="t('tableName')" min-width="170">
              <template #default="{ row }">
                <span>{{ row.name }}</span>
              </template>
            </el-table-column>
            <el-table-column :label="t('tableComment')" min-width="90" />
            <el-table-column :label="t('rowCount')" width="130" align="right">
              <template #default="{ row }">
                <span class="report__count">
                  <el-icon
                    v-if="row.name === maxTableName"
                    class="report__top-icon"
                    :title="t('largestTable')"
                  >
                    <TrophyBase />
                  </el-icon>
                  <span :class="{ 'report__top-count': row.name === maxTableName }">
                    {{ formatNumber(row.rows) }}
                  </span>
                </span>
              </template>
            </el-table-column>
          </el-table>
        </el-col>
      </el-row>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as echarts from 'echarts/core'
import { PieChart } from 'echarts/charts'
import { TitleComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { DataAnalysis, TrophyBase } from '@element-plus/icons-vue'
import type { DetectData } from '@/api'

echarts.use([PieChart, TitleComponent, TooltipComponent, LegendComponent, CanvasRenderer])

const props = defineProps<{ report: DetectData }>()
const { t } = useI18n()

const chartRef = ref<HTMLElement>()
let chart: echarts.ECharts | null = null

// 行数最多的表名（用于饼图高亮 + 表格标星）。
const maxTableName = computed(() => {
  if (!props.report.tables.length) return ''
  return props.report.tables.reduce((max, cur) =>
    cur.rows > max.rows ? cur : max,
  ).name
})

// 关键指标卡片。
const metricCards = computed(() => {
  const m = props.report.metrics
  return [
    { label: t('metricUserTotal'), value: m.userTotal, formatter: formatNumber },
    {
      label: t('metricUserActive'),
      value: m.userActive,
      suffix: t('bannedSuffix', { n: formatNumber(m.userBanned) }),
      formatter: formatNumber,
    },
    {
      label: t('metricBalanceTotal'),
      value: m.balanceTotal / 100,
      suffix: '',
      formatter: formatMoney,
    },
    { label: t('metricActiveSubscribers'), value: m.activeSubscribers, formatter: formatNumber },
    {
      label: t('metricPlanTotal'),
      value: m.planTotal,
      suffix: t('onSaleSuffix', { n: formatNumber(m.planOnSale) }),
      formatter: formatNumber,
    },
    {
      label: t('metricOrderTotal'),
      value: m.orderTotal,
      suffix: t('completedSuffix', { n: formatNumber(m.orderCompleted) }),
      formatter: formatNumber,
    },
    { label: t('metricNodeTotal'), value: m.nodeTotal, formatter: formatNumber },
    {
      label: t('metricCouponTicket'),
      value: m.couponEnable,
      suffix: t('ticketSuffix', { n: formatNumber(m.ticketOpen) }),
      formatter: formatNumber,
    },
  ]
})

// 饼图配置。
function renderChart() {
  if (!chartRef.value) return
  if (!chart) {
    chart = echarts.init(chartRef.value)
  }

  // 只取行数 > 0 的表，避免空表污染饼图。
  const data = props.report.tables
    .filter((t) => t.rows > 0)
    .map((t) => ({
      name: t.comment || t.name,
      value: t.rows,
      tableName: t.name,
    }))

  // 最大表高亮。
  const maxName = maxTableName.value
  data.forEach((d) => {
    d.name = d.tableName === maxName ? `🏆 ${d.name}` : d.name
  })

  chart.setOption({
    tooltip: {
      trigger: 'item',
      formatter: '{b}: {c} ({d}%)',
    },
    legend: {
      type: 'scroll',
      orient: 'vertical',
      right: 0,
      top: 'middle',
      textStyle: { fontSize: 11 },
    },
    series: [
      {
        name: t('tableRowsDetail'),
        type: 'pie',
        radius: ['40%', '70%'],
        center: ['35%', '50%'],
        avoidLabelOverlap: false,
        itemStyle: {
          borderRadius: 4,
          borderColor: '#fff',
          borderWidth: 2,
        },
        label: { show: false },
        emphasis: {
          label: {
            show: true,
            fontSize: 13,
            fontWeight: 'bold',
          },
        },
        data,
      },
    ],
  })
}

// 行数最多的表行高亮。
function rowClassName({ row }: { row: { name: string } }) {
  return row.name === maxTableName.value ? 'report__top-row' : ''
}

function formatNumber(n: number): string {
  return n.toLocaleString('zh-CN')
}
function formatMoney(n: number): string {
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

onMounted(() => {
  renderChart()
  window.addEventListener('resize', handleResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  chart?.dispose()
})
watch(() => props.report, renderChart, { deep: true })

function handleResize() {
  chart?.resize()
}
</script>

<style scoped lang="scss">
.report {
  margin-top: 20px;

  &__header {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
  }

  &__subtitle {
    margin: 0 0 12px;
    color: #303133;
  }

  &__suffix {
    font-size: 13px;
    color: #909399;
  }

  &__chart {
    height: 320px;
    width: 100%;
  }

  &__count {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  &__top-icon {
    color: #e6a23c;
    font-size: 14px;
  }

  &__top-count {
    color: #e6a23c;
    font-weight: 600;
  }

  :deep(.report__top-row) {
    background-color: #fdf6ec !important;
  }
}
</style>
