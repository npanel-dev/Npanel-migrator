<template>
  <DefaultLayout>
    <el-row :gutter="20">
      <!-- 左侧：源端面板配置 -->
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><Connection /></el-icon>
              <span>{{ t('sourcePanel') }}</span>
            </div>
          </template>

          <el-form label-width="90px" label-position="right">
            <!-- 面板类型：单选，含"自动检测" -->
            <el-form-item :label="t('panelLabel')">
              <el-radio-group v-model="source.panel" :disabled="sourceLocked">
                <el-radio-button value="auto">{{ t('panels.auto') }}</el-radio-button>
                <el-radio-button
                  v-for="p in panels"
                  :key="p.value"
                  :value="p.value"
                >
                  {{ t(`panels.${p.value}`) }}
                </el-radio-button>
              </el-radio-group>
            </el-form-item>

            <el-form-item :label="t('databaseHost')">
              <el-input v-model="source.host" placeholder="127.0.0.1" />
            </el-form-item>
            <el-form-item :label="t('databasePort')">
              <el-input-number
                v-model="source.port"
                :min="1"
                :max="65535"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('databaseName')">
              <el-input v-model="source.database" placeholder="v2board" />
            </el-form-item>
            <el-form-item :label="t('username')">
              <el-input v-model="source.username" placeholder="root" />
            </el-form-item>
            <el-form-item :label="t('password')">
              <el-input
                v-model="source.password"
                type="password"
                show-password
                :placeholder="t('databasePassword')"
              />
            </el-form-item>

            <el-form-item>
              <el-button
                type="primary"
                :loading="sourceTesting"
                @click="onTestSource"
              >
                {{ t('common.testConnection') }}
              </el-button>
              <el-tag
                v-if="sourceResult"
                :type="sourceResult.ok ? 'success' : 'danger'"
                style="margin-left: 12px"
              >
                {{ sourceResult.ok ? t('common.connectionSuccess') : t('common.connectionFailed') }}
              </el-tag>
              <el-tag
                v-if="sourceResult?.ok && sourceResult.detail?.dbVersion"
                type="info"
                effect="dark"
                style="margin-left: 8px"
                :title="sourceResult.detail.dbVersion"
              >
                {{ formatDBLabel(sourceResult.detail) }}
              </el-tag>
              <el-tag
                v-if="sourceResult?.ok && sourceResult.detail?.panel"
                type="warning"
                effect="plain"
                style="margin-left: 8px"
              >
                {{ sourceResult.detail.panel }}
              </el-tag>
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>

      <!-- 右侧：目标端 NPanel 配置 -->
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <el-icon><Aim /></el-icon>
              <span>{{ t('targetPanel') }}</span>
            </div>
          </template>

          <el-form label-width="90px" label-position="right">
            <el-form-item :label="t('databaseHost')">
              <el-input v-model="target.host" placeholder="127.0.0.1" />
            </el-form-item>
            <el-form-item :label="t('databasePort')">
              <el-input-number
                v-model="target.port"
                :min="1"
                :max="65535"
                controls-position="right"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('databaseName')">
              <el-input v-model="target.database" placeholder="npanel" />
            </el-form-item>
            <el-form-item :label="t('username')">
              <el-input v-model="target.username" placeholder="root" />
            </el-form-item>
            <el-form-item :label="t('password')">
              <el-input
                v-model="target.password"
                type="password"
                show-password
                :placeholder="t('databasePassword')"
              />
            </el-form-item>

            <el-form-item>
              <el-button
                type="primary"
                :loading="targetTesting"
                @click="onTestTarget"
              >
                {{ t('common.testConnection') }}
              </el-button>
              <el-tag
                v-if="targetResult"
                :type="targetResult.ok ? 'success' : 'danger'"
                style="margin-left: 12px"
              >
                {{ targetResult.ok ? t('common.connectionSuccess') : t('common.connectionFailed') }}
              </el-tag>
              <el-tag
                v-if="targetResult?.ok && targetResult.detail?.dbVersion"
                type="info"
                effect="dark"
                style="margin-left: 8px"
                :title="targetResult.detail.dbVersion"
              >
                {{ formatDBLabel(targetResult.detail) }}
              </el-tag>
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>
    </el-row>

    <el-card v-if="sourceConnected && targetConnected" shadow="hover" class="plan-mapping">
      <template #header>
        <div class="card-header plan-mapping__header">
          <div>
            <el-icon><Connection /></el-icon>
            <span>{{ t('planMappingTitle') }}</span>
          </div>
          <el-button type="primary" plain :loading="mappingLoading" @click="onLoadPlanMappings">
            {{ t('loadPlanMappings') }}
          </el-button>
        </div>
      </template>

      <el-alert
        :title="t('planMappingNotice')"
        type="info"
        :closable="false"
        show-icon
      />
      <el-alert
        v-if="mappingOptions && !mappingOptions.sourceOrderLookupIndexed"
        :title="t('planMappingIndexWarning')"
        type="warning"
        :closable="false"
        show-icon
        class="plan-mapping__index-warning"
      />

      <el-empty v-if="!mappingOptions" :description="t('planMappingEmpty')" />

      <div v-else class="plan-mapping__content">
        <div
          v-for="sourcePlan in mappingOptions.sourcePlans"
          :key="sourcePlan.id"
          class="plan-mapping__plan"
        >
          <div class="plan-mapping__plan-row">
            <div class="plan-mapping__source">
              <strong>{{ sourcePlan.name }}</strong>
              <el-tag size="small" type="info">ID {{ sourcePlan.id }}</el-tag>
              <el-tag v-if="sourcePlan.activeUserCount > 0" size="small" type="warning">
                {{ t('activeUsersCount', { n: sourcePlan.activeUserCount }) }}
              </el-tag>
              <el-tag v-if="sourcePlan.orderCount > 0" size="small" type="info">
                {{ t('historyOrdersCount', { n: sourcePlan.orderCount }) }}
              </el-tag>
            </div>
            <el-icon><Right /></el-icon>
            <el-select
              v-model="planSelections[sourcePlan.id]"
              clearable
              filterable
              :placeholder="t('selectTargetPlan')"
              class="plan-mapping__select"
              @change="onTargetPlanChange(sourcePlan)"
            >
              <el-option
                v-for="targetPlan in mappingOptions.targetPlans"
                :key="targetPlan.id"
                :value="targetPlan.id"
                :label="`${targetPlan.name} (ID ${targetPlan.id})`"
              >
                <span>{{ targetPlan.name }} (ID {{ targetPlan.id }})</span>
                <el-tag v-if="!targetPlan.sell" size="small" type="info" style="margin-left: 8px">
                  {{ t('notForSale') }}
                </el-tag>
              </el-option>
            </el-select>
          </div>

          <div v-if="planSelections[sourcePlan.id]" class="plan-mapping__periods">
            <div
              v-for="sourceOption in sourcePlan.priceOptions"
              :key="sourceOption.sourcePeriod"
              class="plan-mapping__period-row"
            >
              <div class="plan-mapping__period-source">
                <span>{{ sourceOption.name }}</span>
                <small>
                  {{ formatDuration(sourceOption.durationUnit, sourceOption.durationValue) }}
                  · {{ formatPrice(sourceOption.priceCents) }}
                </small>
                <el-tag v-if="sourceOption.activeUserCount > 0" size="small" type="danger">
                  {{ t('activeUsersCount', { n: sourceOption.activeUserCount }) }}
                </el-tag>
                <el-tag v-if="sourceOption.orderCount > 0" size="small" type="info">
                  {{ t('historyOrdersCount', { n: sourceOption.orderCount }) }}
                </el-tag>
              </div>
              <el-icon><Right /></el-icon>
              <el-select
                v-model="periodSelections[periodKey(sourcePlan.id, sourceOption.sourcePeriod)]"
                clearable
                filterable
                :placeholder="t('selectTargetPriceOption')"
                class="plan-mapping__select"
              >
                <el-option
                  v-for="targetOption in targetPriceOptions(sourcePlan.id)"
                  :key="targetOption.id"
                  :value="targetOption.id"
                  :label="`${targetOption.name} · ${formatDuration(targetOption.durationUnit, targetOption.durationValue)}`"
                />
              </el-select>
            </div>
          </div>
        </div>

        <el-divider />

        <div class="plan-mapping__trial">
          <div>
            <strong>{{ t('trialAssignmentTitle') }}</strong>
            <p>{{ t('trialAssignmentDesc', { n: mappingOptions.inactiveUserCount }) }}</p>
          </div>
          <el-select
            v-model="trialAssignment.targetSubscribeId"
            filterable
            :placeholder="t('selectTrialPlan')"
            class="plan-mapping__select"
          >
            <el-option
              v-for="targetPlan in mappingOptions.targetPlans"
              :key="targetPlan.id"
              :value="targetPlan.id"
              :label="`${targetPlan.name} (ID ${targetPlan.id})`"
            />
          </el-select>
          <el-input-number
            v-model="trialAssignment.durationValue"
            :min="1"
            :max="9999"
            :disabled="trialAssignment.durationUnit === 'NoLimit'"
          />
          <el-select v-model="trialAssignment.durationUnit" style="width: 130px">
            <el-option value="Minute" :label="t('durationMinute')" />
            <el-option value="Hour" :label="t('durationHour')" />
            <el-option value="Day" :label="t('durationDay')" />
            <el-option value="Week" :label="t('durationWeek')" />
            <el-option value="Month" :label="t('durationMonth')" />
            <el-option value="quarter" :label="t('durationQuarter')" />
            <el-option value="half_year" :label="t('durationHalfYear')" />
            <el-option value="Year" :label="t('durationYear')" />
            <el-option value="NoLimit" :label="t('durationNoLimit')" />
          </el-select>
        </div>
      </div>
    </el-card>

    <!-- 底部：迁移模式选择 -->
    <el-card shadow="hover" class="migration-mode">
      <template #header>
        <div class="card-header">
          <el-icon><Operation /></el-icon>
          <span>{{ t('migrationMode') }}</span>
        </div>
      </template>

      <el-radio-group v-model="mode">
        <el-radio-button
          v-for="m in modes"
          :key="m.value"
          :value="m.value"
        >
          {{ t(m.labelKey) }}
        </el-radio-button>
      </el-radio-group>

      <p class="migration-mode__desc">{{ t(modes.find((m) => m.value === mode)?.descKey || '') }}</p>

      <!-- 迁移模块多选（archive 模式隐藏） -->
      <div v-if="mode !== 'archive'" class="migration-modules">
        <div class="migration-modules__header">
          <span class="migration-modules__title">{{ t('migrationModules') }}</span>
          <el-checkbox
            :model-value="selectedModules.length === allModuleValues.length"
            :indeterminate="selectedModules.length > 0 && selectedModules.length < allModuleValues.length"
            @change="toggleAllModules"
          >
            {{ t('selectAll') }}
          </el-checkbox>
        </div>
        <el-checkbox-group v-model="selectedModules" class="migration-modules__group">
          <el-checkbox
            v-for="m in moduleOptions"
            :key="m.value"
            :value="m.value"
            :disabled="m.value === 'users' && requiresUsers"
            class="migration-modules__item"
          >
            <div class="migration-modules__label">
              <span class="migration-modules__name">{{ t(m.labelKey) }}</span>
              <span class="migration-modules__desc">{{ t(m.descKey) }}</span>
            </div>
          </el-checkbox>
        </el-checkbox-group>
      </div>

      <el-divider />

      <div class="migration-mode__actions">
        <el-button
          type="success"
          plain
          :disabled="!sourceConnected"
          :loading="detecting"
          @click="onDetect"
        >
          <el-icon><DataAnalysis /></el-icon>
          {{ t('common.generateReport') }}
        </el-button>
        <el-button type="warning" plain :disabled="!ready" :loading="dryRunning" @click="onDryRun">
          {{ t('common.dryRun') }}
        </el-button>
        <el-button
          type="danger"
          :disabled="mode === 'archive' || !ready || importRunning || dryRunBlocked"
          :loading="importing"
          @click="onImport"
        >
          {{ t('common.startMigration') }}
        </el-button>
      </div>
    </el-card>

    <!-- 迁移前报告 -->
    <DetectReportCard
      v-if="detectReport"
      :report="detectReport"
      class="report-wrapper"
    />

    <!-- dry-run 预演报告 -->
    <DryRunReportCard
      v-if="dryRunReport"
      :report="dryRunReport"
      class="report-wrapper"
    />

    <!-- import 执行进度 -->
    <ImportProgressCard
      v-if="progressSnapshot && progressSnapshot.status !== 'idle'"
      :snapshot="progressSnapshot"
      class="report-wrapper"
      @cancel="onCancelImport"
      @resume="onResumeImport"
    />

    <!-- 任务进度弹窗（detect/dry-run） -->
    <TaskProgressDialog
      v-model="taskDialogVisible"
      :title="taskDialogTitle"
      :snapshot="taskSnapshot"
      @close="onTaskDialogClose"
    />
  </DefaultLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'
import { Aim, Connection, DataAnalysis, Operation, Right } from '@element-plus/icons-vue'
import DefaultLayout from '@/layouts/DefaultLayout.vue'
import DetectReportCard from '@/components/DetectReportCard.vue'
import DryRunReportCard from '@/components/DryRunReportCard.vue'
import ImportProgressCard from '@/components/ImportProgressCard.vue'
import TaskProgressDialog from '@/components/TaskProgressDialog.vue'
import message from '@/utils/message'
import {
  apiErrorMessage,
  cancelImport,
  getProgress,
  getPlanMappingOptions,
  getTaskProgress,
  listMigrationJobs,
  startDetectAsync,
  startDryRunAsync,
  startImport,
  testConnection,
  type DatabaseConfig,
  type DetectData,
  type DryRunReport,
  type ImportRequest,
  type MigrationJobSummary,
  type ProgressSnapshot,
  type PlanMappingOptionsResponse,
  type SourcePlanMappingOption,
  type TargetPriceOption,
  type TaskProgressSnapshot,
  type TestConnectionResult,
} from '@/api'

const { t, locale } = useI18n()

// 支持的源面板（不含 auto，auto 是 radio 独立选项）。
const panels = [
  { value: 'xiaov2board' },
  { value: 'v2board' },
  { value: 'xboard' },
  { value: 'ppanel' },
  { value: 'sspanel' },
]

// 迁移模式（labelKey/descKey 指向 i18n 文案 key）。
const modes = [
  { value: 'full', labelKey: 'modeFull', descKey: 'modeFullDesc' },
  { value: 'partial', labelKey: 'modePartial', descKey: 'modePartialDesc' },
  { value: 'archive', labelKey: 'modeArchive', descKey: 'modeArchiveDesc' },
  { value: 'custom', labelKey: 'modeCustom', descKey: 'modeCustomDesc' },
]

// 迁移模块定义（按功能分组）。
const moduleOptions = [
  { value: 'users', labelKey: 'moduleUsers', descKey: 'moduleUsersDesc' },
  { value: 'plans', labelKey: 'modulePlans', descKey: 'modulePlansDesc' },
  { value: 'orders', labelKey: 'moduleOrders', descKey: 'moduleOrdersDesc' },
  { value: 'subscriptions', labelKey: 'moduleSubscriptions', descKey: 'moduleSubscriptionsDesc' },
  { value: 'nodes', labelKey: 'moduleNodes', descKey: 'moduleNodesDesc' },
  { value: 'coupons', labelKey: 'moduleCoupons', descKey: 'moduleCouponsDesc' },
  { value: 'notices', labelKey: 'moduleNotices', descKey: 'moduleNoticesDesc' },
  { value: 'tickets', labelKey: 'moduleTickets', descKey: 'moduleTicketsDesc' },
]
const allModuleValues = moduleOptions.map((m) => m.value)

const source = reactive<DatabaseConfig & { panel: string }>({
  panel: 'auto',
  host: '127.0.0.1',
  port: 3306,
  database: 'v2board',
  username: 'root',
  password: '',
})

const target = reactive<DatabaseConfig>({
  host: '127.0.0.1',
  port: 3306,
  database: 'npanel',
  username: 'root',
  password: '',
})

const sourceLocked = ref(false)
const sourceTesting = ref(false)
const targetTesting = ref(false)
const sourceResult = ref<TestConnectionResult | null>(null)
const targetResult = ref<TestConnectionResult | null>(null)
const mode = ref('full')
// 选中的迁移模块（完整迁移默认全选）。
const selectedModules = ref<string[]>([...allModuleValues])
const userDependentModules = ['orders', 'subscriptions', 'tickets']
const requiresUsers = computed(
  () => selectedModules.value.some((module) => userDependentModules.includes(module)),
)

const mappingLoading = ref(false)
const mappingOptions = ref<PlanMappingOptionsResponse | null>(null)
const planSelections = reactive<Record<number, number | undefined>>({})
const periodSelections = reactive<Record<string, number | undefined>>({})
const trialAssignment = reactive({
  targetSubscribeId: undefined as number | undefined,
  durationUnit: 'Day',
  durationValue: 7,
})

// 监听模式切换：选 full 时全选模块，选 archive 时清空（归档不写入）。
watch(mode, (newMode) => {
  if (newMode === 'full' || newMode === 'partial') {
    selectedModules.value = [...allModuleValues]
  } else if (newMode === 'archive') {
    selectedModules.value = []
  }
})

// 模块勾选变化时：如果与全选不一致且当前非 custom，自动切到 custom。
watch(selectedModules, (val) => {
  if (mode.value === 'archive') return
  if (val.some((module) => userDependentModules.includes(module)) && !val.includes('users')) {
    selectedModules.value = [...val, 'users']
    return
  }
  const isAll = val.length === allModuleValues.length &&
    allModuleValues.every((m) => val.includes(m))
  if (isAll && mode.value !== 'full') {
    mode.value = 'full'
  } else if (!isAll && mode.value !== 'custom') {
    mode.value = 'custom'
  }
}, { deep: true })

function toggleAllModules() {
  if (selectedModules.value.length === allModuleValues.length) {
    selectedModules.value = []
  } else {
    selectedModules.value = [...allModuleValues]
  }
}

// detect 阶段状态
const detecting = ref(false)
const detectReport = ref<DetectData | null>(null)

// dry-run 阶段状态
const dryRunning = ref(false)
const dryRunReport = ref<DryRunReport | null>(null)

// import 阶段状态
const importing = ref(false)
const progressSnapshot = ref<ProgressSnapshot | null>(null)
const resumableJob = ref<MigrationJobSummary | null>(null)
const activeJobId = ref(localStorage.getItem('npanel-migrator-job-id') ?? '')
let progressTimer: ReturnType<typeof setInterval> | null = null

// 任务进度弹窗（detect/dry-run 共用）
const taskDialogVisible = ref(false)
const taskDialogTitle = ref('')
const taskSnapshot = ref<TaskProgressSnapshot>({
  status: 'idle', phase: '', phaseLabel: '', total: 0, done: 0, errors: 0, message: '', logs: [],
})
let taskTimer: ReturnType<typeof setInterval> | null = null
let currentTaskType: 'detect' | 'dryrun' = 'detect'

const sourceConnected = computed(() => sourceResult.value?.ok ?? false)
const targetConnected = computed(
  () => Boolean(targetResult.value?.ok && targetResult.value.detail?.isNPanelTarget),
)
const mappingNeeded = computed(
  () => mode.value !== 'archive' &&
    selectedModules.value.some((module) => ['plans', 'orders', 'subscriptions'].includes(module)),
)
const mappingReady = computed(() => {
  if (!mappingNeeded.value) return true
  const options = mappingOptions.value
  if (!options) return false
  for (const sourcePlan of options.sourcePlans) {
    if (sourcePlan.activeUserCount <= 0) continue
    if (!planSelections[sourcePlan.id]) return false
    for (const sourceOption of sourcePlan.priceOptions) {
      if (
        sourceOption.activeUserCount > 0 &&
        !periodSelections[periodKey(sourcePlan.id, sourceOption.sourcePeriod)]
      ) {
        return false
      }
    }
  }
  if (options.inactiveUserCount > 0) {
    return Boolean(
      trialAssignment.targetSubscribeId &&
      trialAssignment.durationUnit &&
      trialAssignment.durationValue > 0,
    )
  }
  return true
})
const ready = computed(
  () => sourceConnected.value && targetConnected.value && mappingReady.value,
)

// import 运行中时禁用"开始迁移"按钮，避免重复触发。
const importRunning = computed(
  () => progressSnapshot.value?.status === 'running',
)
// 已执行预演且存在阻断问题时，前端直接禁用迁移；后端仍会再次强制预演，
// 防止绕过 UI 或使用过期报告。
const dryRunBlocked = computed(
  () => selectedModules.value.includes('users') &&
    dryRunReport.value?.summary.canProceed === false,
)

// 挂载时查询一次进度（若上次有未完成的任务，恢复显示）。
onMounted(async () => {
  try {
    const snap = await getProgress()
    progressSnapshot.value = snap
    if (snap.jobId) {
      activeJobId.value = snap.jobId
      localStorage.setItem('npanel-migrator-job-id', snap.jobId)
    }
    if (snap.status === 'running') {
      startProgressPolling()
    }
  } catch {
    // 忽略：可能是服务未启动
  }
})

onUnmounted(() => {
  stopProgressPolling()
  stopTaskPolling()
})

// 进度轮询：每 1 秒拉取一次，任务结束自动停止。
function startProgressPolling() {
  stopProgressPolling()
  progressTimer = setInterval(async () => {
    try {
      const snap = await getProgress()
      progressSnapshot.value = snap
      if (['completed', 'failed', 'cancelled'].includes(snap.status)) {
        stopProgressPolling()
        importing.value = false
        if (snap.status === 'completed') {
          resumableJob.value = null
          localStorage.removeItem('npanel-migrator-job-id')
          message.success(snap.message)
        } else if (snap.status === 'cancelled') {
          message.warning(snap.message || t('msgImportCancelled'))
        } else {
          message.error(snap.message)
        }
      }
    } catch {
      // 轮询失败不中断，继续下次
    }
  }, 1000)
}

function stopProgressPolling() {
  if (progressTimer) {
    clearInterval(progressTimer)
    progressTimer = null
  }
}

async function onTestSource() {
  sourceTesting.value = true
  sourceResult.value = null
  try {
    sourceResult.value = await testConnection('source', {
      host: source.host,
      port: source.port,
      database: source.database,
      username: source.username,
      password: source.password,
    }, locale.value)
    if (sourceResult.value.ok) {
      sourceLocked.value = true
      message.success(sourceResult.value.message || t('msgSourceConnected'))
      // 探测到面板类型时同步到单选（auto 保持 auto，让用户看到探测结果）。
      const detected = sourceResult.value.detail?.panel
      if (detected && detected !== 'unknown') {
        // 若用户选的是 auto，用探测结果填充；否则保留用户选择。
        if (source.panel === 'auto') {
          // 仅当探测到的面板在已知列表中才同步，避免未知值。
          if (panels.some((p) => p.value === detected)) {
            source.panel = detected
          }
        }
      }
      if (targetConnected.value) {
        await Promise.all([onLoadPlanMappings(), loadResumableJobs()])
      }
    } else {
      message.error(sourceResult.value.message || t('msgSourceConnectFailed'))
    }
  } catch (e) {
    sourceResult.value = { ok: false, message: String(e) }
    message.error(t('msgSourceTestFailed', { err: String(e) }))
  } finally {
    sourceTesting.value = false
  }
}

async function onTestTarget() {
  targetTesting.value = true
  targetResult.value = null
  try {
    targetResult.value = await testConnection('target', {
      host: target.host,
      port: target.port,
      database: target.database,
      username: target.username,
      password: target.password,
    }, locale.value)
    if (targetResult.value.ok) {
      if (targetResult.value.detail?.isNPanelTarget) {
        message.success(targetResult.value.message || t('msgTargetConnected'))
        if (sourceConnected.value) {
          await Promise.all([onLoadPlanMappings(), loadResumableJobs()])
        }
      } else {
        message.warning(targetResult.value.message || t('msgTargetNotNPanel'))
      }
    } else {
      message.error(targetResult.value.message || t('msgTargetConnectFailed'))
    }
  } catch (e) {
    targetResult.value = { ok: false, message: String(e) }
    message.error(t('msgTargetTestFailed', { err: String(e) }))
  } finally {
    targetTesting.value = false
  }
}

function migrationConnectionRequest() {
  return {
    sourceHost: source.host,
    sourcePort: source.port,
    sourceDatabase: source.database,
    sourceUsername: source.username,
    sourcePassword: source.password,
    sourcePanel: source.panel === 'auto'
      ? sourceResult.value?.detail?.panel
      : source.panel,
    targetHost: target.host,
    targetPort: target.port,
    targetDatabase: target.database,
    targetUsername: target.username,
    targetPassword: target.password,
  }
}

async function loadResumableJobs() {
  if (!sourceConnected.value || !targetConnected.value || importRunning.value) return
  try {
    const response = await listMigrationJobs(migrationConnectionRequest())
    const job = response.jobs.find((candidate) => candidate.resumable) ?? null
    resumableJob.value = job
    if (!job) return

    activeJobId.value = job.id
    localStorage.setItem('npanel-migrator-job-id', job.id)
    progressSnapshot.value = {
      jobId: job.id,
      status: job.status === 'cancelled' ? 'cancelled' : 'failed',
      phase: job.phase,
      phaseLabel: job.phase,
      total: job.total,
      done: job.done,
      errors: job.errors,
      startedAt: job.startedAt,
      finishedAt: job.finishedAt,
      message: job.lastError || t('msgResumeAvailable'),
      ratePerSecond: 0,
      etaSeconds: 0,
      resumable: true,
      cancelRequested: false,
    }
    message.info(t('msgResumeAvailable'))
  } catch (e) {
    // 任务查询不影响连接和全新迁移；仅在控制台留下诊断信息。
    console.warn('load resumable migration jobs failed', e)
  }
}

async function onLoadPlanMappings() {
  if (!sourceConnected.value || !targetConnected.value) return
  mappingLoading.value = true
  try {
    const response = await getPlanMappingOptions(
      {
        host: source.host, port: source.port, database: source.database,
        username: source.username, password: source.password,
        panel: source.panel === 'auto' ? undefined : source.panel,
      },
      target,
    )
    if (!response.ok) {
      message.error(response.message)
      return
    }
    mappingOptions.value = response
    clearMappingSelections()
    trialAssignment.targetSubscribeId = response.targetPlans.some(
      (plan) => plan.id === response.trialDefaults.subscribeId,
    )
      ? response.trialDefaults.subscribeId
      : undefined
    trialAssignment.durationUnit = response.trialDefaults.timeUnit || 'Day'
    trialAssignment.durationValue = response.trialDefaults.timeValue > 0
      ? response.trialDefaults.timeValue
      : 7

    for (const sourcePlan of response.sourcePlans) {
      const sameNamePlans = response.targetPlans.filter(
        (plan) => normalizeName(plan.name) === normalizeName(sourcePlan.name),
      )
      if (sameNamePlans.length === 1) {
        planSelections[sourcePlan.id] = sameNamePlans[0].id
        onTargetPlanChange(sourcePlan)
      }
    }
    message.success(response.message)
  } catch (e) {
    mappingOptions.value = null
    message.error(t('msgPlanMappingFailed', { err: apiErrorMessage(e) }))
  } finally {
    mappingLoading.value = false
  }
}

function clearMappingSelections() {
  Object.keys(planSelections).forEach((key) => delete planSelections[Number(key)])
  Object.keys(periodSelections).forEach((key) => delete periodSelections[key])
}

function normalizeName(value: string): string {
  return value.trim().toLocaleLowerCase().replace(/\s+/g, '')
}

function periodKey(sourcePlanId: number, sourcePeriod: string): string {
  return `${sourcePlanId}:${sourcePeriod}`
}

function targetPriceOptions(sourcePlanId: number): TargetPriceOption[] {
  const targetPlanId = planSelections[sourcePlanId]
  const targetPlan = mappingOptions.value?.targetPlans.find((plan) => plan.id === targetPlanId)
  return targetPlan?.priceOptions ?? []
}

function onTargetPlanChange(sourcePlan: SourcePlanMappingOption) {
  for (const sourceOption of sourcePlan.priceOptions) {
    const key = periodKey(sourcePlan.id, sourceOption.sourcePeriod)
    delete periodSelections[key]
    const candidates = targetPriceOptions(sourcePlan.id).filter((targetOption) => {
      if (targetOption.optionType !== sourceOption.optionType) return false
      if (sourceOption.optionType !== 'duration') return true
      return durationSignature(targetOption.durationUnit, targetOption.durationValue) ===
        durationSignature(sourceOption.durationUnit, sourceOption.durationValue)
    })
    if (candidates.length === 1) {
      periodSelections[key] = candidates[0].id
    }
  }
}

function durationSignature(unit: string, value: number): string {
  switch (unit.trim().toLocaleLowerCase()) {
    case 'year':
      return `month:${value * 12}`
    case 'month':
      return `month:${value}`
    case 'quarter':
      return `month:${value * 3}`
    case 'half_year':
      return `month:${value * 6}`
    case 'nolimit':
    case 'no_limit':
      return 'nolimit'
    default:
      return `${unit.trim().toLocaleLowerCase()}:${value}`
  }
}

function formatDuration(unit: string, value: number): string {
  if (unit.toLocaleLowerCase() === 'nolimit') return t('durationNoLimit')
  return `${value} ${unit}`
}

function formatPrice(cents: number): string {
  return (cents / 100).toFixed(2)
}

// 生成迁移前报告（异步 detect + 进度弹窗）
async function onDetect() {
  detecting.value = true
  detectReport.value = null
  try {
    const resp = await startDetectAsync(
      {
        host: source.host, port: source.port, database: source.database,
        username: source.username, password: source.password,
      },
      source.panel === 'auto' ? undefined : source.panel,
    )
    if (resp.ok) {
      currentTaskType = 'detect'
      taskDialogTitle.value = t('detectingTitle')
      taskDialogVisible.value = true
      startTaskPolling()
    } else {
      message.error(resp.message || t('msgReportFailed'))
      detecting.value = false
    }
  } catch (e) {
    message.error(t('msgReportGenerateFailed', { err: String(e) }))
    detecting.value = false
  }
}

// dry-run 预演（异步 + 进度弹窗）
async function onDryRun() {
  dryRunning.value = true
  dryRunReport.value = null
  try {
    const resp = await startDryRunAsync(
      {
        host: source.host, port: source.port, database: source.database,
        username: source.username, password: source.password,
      },
      source.panel === 'auto' ? undefined : source.panel,
    )
    if (resp.ok) {
      currentTaskType = 'dryrun'
      taskDialogTitle.value = t('dryRunningTitle')
      taskDialogVisible.value = true
      startTaskPolling()
    } else {
      message.error(resp.message || t('msgDryRunFailed'))
      dryRunning.value = false
    }
  } catch (e) {
    message.error(t('msgDryRunGenerateFailed', { err: String(e) }))
    dryRunning.value = false
  }
}

// 任务进度轮询（detect/dry-run 共用）
function startTaskPolling() {
  stopTaskPolling()
  taskTimer = setInterval(async () => {
    try {
      const snap = await getTaskProgress(currentTaskType)
      taskSnapshot.value = snap
      // 任务结束时停止轮询并处理报告。
      if (snap.status === 'completed' || snap.status === 'failed') {
        stopTaskPolling()
        if (snap.status === 'completed' && snap.report) {
          if (currentTaskType === 'detect') {
            detectReport.value = snap.report as DetectData
            detecting.value = false
            message.success(snap.message || t('msgReportGenerated'))
          } else {
            dryRunReport.value = snap.report as DryRunReport
            dryRunning.value = false
            const r = snap.report as DryRunReport
            if (r.summary.canProceed) {
              message.success(t('msgDryRunCompleted') + ' · ' + t('canProceed'))
            } else {
              message.warning(t('msgDryRunCompleted') + ' · ' + t('hasBlockingIssues'))
            }
          }
        } else if (snap.status === 'failed') {
          detecting.value = false
          dryRunning.value = false
          message.error(snap.message)
        }
      }
    } catch {
      // 轮询失败不中断
    }
  }, 800)
}

function stopTaskPolling() {
  if (taskTimer) {
    clearInterval(taskTimer)
    taskTimer = null
  }
}

function onTaskDialogClose() {
  stopTaskPolling()
}

function buildImportRequest(resumeJobId?: string): ImportRequest {
  return {
    ...migrationConnectionRequest(),
    modules: mode.value === 'archive' ? [] : selectedModules.value,
    planMappings: (mappingOptions.value?.sourcePlans ?? [])
      .filter((sourcePlan) => Boolean(planSelections[sourcePlan.id]))
      .map((sourcePlan) => ({
        sourcePlanId: sourcePlan.id,
        targetSubscribeId: planSelections[sourcePlan.id] as number,
        periodMappings: sourcePlan.priceOptions
          .filter((sourceOption) => Boolean(
            periodSelections[periodKey(sourcePlan.id, sourceOption.sourcePeriod)],
          ))
          .map((sourceOption) => ({
            sourcePeriod: sourceOption.sourcePeriod,
            targetPriceOptionId: periodSelections[
              periodKey(sourcePlan.id, sourceOption.sourcePeriod)
            ] as number,
          })),
      })),
    trialAssignment: {
      targetSubscribeId: trialAssignment.targetSubscribeId ?? 0,
      durationUnit: trialAssignment.durationUnit,
      durationValue: trialAssignment.durationValue,
    },
    resumeJobId,
  }
}

async function executeImport(resumeJobId?: string) {
  importing.value = true
  try {
    const resp = await startImport(buildImportRequest(resumeJobId))
    if (resp.ok) {
      activeJobId.value = resp.jobId ?? resumeJobId ?? ''
      if (activeJobId.value) {
        localStorage.setItem('npanel-migrator-job-id', activeJobId.value)
      }
      resumableJob.value = null
      message.success(t('msgImportStarted'))
      const snap = await getProgress()
      progressSnapshot.value = snap
      startProgressPolling()
    } else {
      message.error(resp.message || t('msgImportBusy'))
      importing.value = false
    }
  } catch (e) {
    message.error(t('msgImportFailed') + ': ' + apiErrorMessage(e))
    importing.value = false
  }
}

// 启动正式迁移（带二次确认）
async function onImport() {
  // 二次确认（写入操作不可撤销）。
  try {
    await ElMessageBox.confirm(t('confirmStartMigration'), t('common.startMigration'), {
      confirmButtonText: t('common.startMigration'),
      cancelButtonText: t('cancel') || 'Cancel',
      type: 'warning',
    })
  } catch {
    return // 用户取消
  }

  await executeImport()
}

async function onResumeImport() {
  const jobId = resumableJob.value?.id || progressSnapshot.value?.jobId || activeJobId.value
  if (!jobId) return
  try {
    await ElMessageBox.confirm(t('confirmResumeMigration'), t('resumeMigration'), {
      confirmButtonText: t('resumeMigration'),
      cancelButtonText: t('cancel'),
      type: 'warning',
    })
  } catch {
    return
  }
  await executeImport(jobId)
}

async function onCancelImport() {
  const jobId = progressSnapshot.value?.jobId || activeJobId.value
  try {
    const response = await cancelImport(jobId)
    if (!response.ok) {
      message.error(response.message)
      return
    }
    if (progressSnapshot.value) {
      progressSnapshot.value.cancelRequested = true
    }
    message.warning(response.message || t('msgCancelRequested'))
  } catch (e) {
    message.error(apiErrorMessage(e))
  }
}

// 格式化数据库类型标签：把 dbType + dbMajor 组合成 "MySQL 8.4" / "MariaDB 10.11"。
// 完整版本号通过 title 属性悬停查看。
function formatDBLabel(detail: { dbType?: string; dbMajor?: string }): string {
  const typeMap: Record<string, string> = {
    mysql: 'MySQL',
    mariadb: 'MariaDB',
    percona: 'Percona',
  }
  const typeLabel = detail.dbType ? (typeMap[detail.dbType] || detail.dbType) : ''
  if (detail.dbMajor) {
    return `${typeLabel} ${detail.dbMajor}`
  }
  return typeLabel
}
</script>

<style scoped lang="scss">
.card-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.plan-mapping {
  margin-top: 20px;

  &__header {
    justify-content: space-between;

    > div {
      display: flex;
      align-items: center;
      gap: 8px;
    }
  }

  &__content {
    margin-top: 16px;
  }

  &__index-warning {
    margin-top: 12px;
  }

  &__plan {
    padding: 14px;
    border: 1px solid #e4e7ed;
    border-radius: 6px;

    & + & {
      margin-top: 12px;
    }
  }

  &__plan-row,
  &__period-row,
  &__trial {
    display: grid;
    grid-template-columns: minmax(220px, 1fr) 24px minmax(300px, 1fr);
    align-items: center;
    gap: 12px;
  }

  &__source,
  &__period-source {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }

  &__periods {
    margin-top: 12px;
    padding: 12px;
    background: #fafafa;
    border-radius: 4px;
  }

  &__period-row + &__period-row {
    margin-top: 10px;
  }

  &__period-source small {
    color: #909399;
  }

  &__select {
    width: 100%;
  }

  &__trial {
    grid-template-columns: minmax(260px, 1fr) minmax(280px, 1fr) 150px 130px;

    p {
      margin: 4px 0 0;
      color: #606266;
      font-size: 13px;
    }
  }
}

.migration-mode {
  margin-top: 20px;

  &__desc {
    margin: 12px 0 0;
    color: #606266;
    font-size: 13px;
    line-height: 1.6;
  }

  &__actions {
    display: flex;
    gap: 12px;
    justify-content: flex-end;
  }
}

.migration-modules {
  margin-top: 16px;
  padding: 16px;
  background: #fafafa;
  border-radius: 6px;
  border: 1px solid #ebeef5;

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
  }

  &__title {
    font-weight: 600;
    color: #303133;
  }

  &__group {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 12px;
  }

  &__item {
    margin-right: 0;
    height: auto;
    padding: 10px 12px;
    background: #fff;
    border: 1px solid #e4e7ed;
    border-radius: 4px;
    transition: all 0.2s;

    &:hover {
      border-color: #409eff;
    }

    :deep(.el-checkbox__label) {
      padding-left: 8px;
      width: 100%;
    }
  }

  &__label {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  &__name {
    font-weight: 600;
    color: #303133;
    font-size: 14px;
  }

  &__desc {
    font-size: 12px;
    color: #909399;
    line-height: 1.4;
  }
}

.report-wrapper {
  margin-top: 20px;
}
</style>
