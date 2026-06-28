// HTTP 客户端封装。
//
// 后端接口约定（待 service 层实现）：
//   POST /api/test-connection   测试数据库连接（源端或目标端）
//   GET  /api/health            健康检查
//
// 当前为骨架，test-connection 等接口在下一步实现。
import axios, { type AxiosInstance } from 'axios'

const http: AxiosInstance = axios.create({
  baseURL: '/api',
  timeout: 60_000,
})

// 数据库连接配置（左侧源端 / 右侧目标端共用）
export interface DatabaseConfig {
  host: string
  port: number
  database: string
  username: string
  password: string
}

// 测试连接成功后附带的详细信息（与后端 service.ConnectionInfo 对齐）
export interface ConnectionInfo {
  // 探测出的面板类型（仅 source 有意义）：xiaov2board/v2board/xboard/ppanel/sspanel/npanel/unknown
  panel?: string
  confidence?: string
  matchTables?: string[]
  // 目标端是否为有效 NPanel 库（仅 target 有意义）
  isNPanelTarget?: boolean
  // 数据库类型与版本（源端和目标端都显示）
  dbType?: string     // mysql / mariadb / percona
  dbVersion?: string  // 完整版本号 "8.4.6"、"10.11.8-MariaDB"
  dbMajor?: string    // 主版本号 "5.7"、"8.4"、"10.11"
}

// 测试连接结果
export interface TestConnectionResult {
  ok: boolean
  message: string
  detail?: ConnectionInfo
}

// 测试数据库连接
// side: 'source'（源端面板）| 'target'（目标端 NPanel）
// lang: 当前界面语言 'zh-CN' | 'en'，用于后端错误提示本地化
export async function testConnection(
  side: 'source' | 'target',
  config: DatabaseConfig,
  lang: string,
): Promise<TestConnectionResult> {
  const { data } = await http.post<TestConnectionResult>('/test-connection', {
    side,
    lang,
    ...config,
  })
  return data
}

// 健康检查
export interface HealthResult {
  status: string
  version: string
}

export async function health(): Promise<HealthResult> {
  const { data } = await http.get<HealthResult>('/health')
  return data
}

// ---- detect 阶段 ----

export interface TableStat {
  name: string
  rows: number
  comment: string
}

export interface DetectMetrics {
  userTotal: number
  userActive: number
  userBanned: number
  balanceTotal: number // 分
  activeSubscribers: number
  planTotal: number
  planOnSale: number
  orderTotal: number
  orderCompleted: number
  nodeTotal: number
  couponEnable: number
  ticketOpen: number
}

export interface DetectData {
  panel: string
  tables: TableStat[]
  metrics: DetectMetrics
  totalRows: number
}

export interface DetectResponse {
  ok: boolean
  message: string
  report?: DetectData
}

// 执行 detect（扫描源库生成迁移前报告）
export async function detect(
  config: DatabaseConfig,
  panel?: string,
): Promise<DetectResponse> {
  const { data } = await http.post<DetectResponse>('/detect', {
    ...config,
    panel,
  })
  return data
}

// ---- dry-run 预演阶段 ----

export interface DryRunIssue {
  severity: 'error' | 'warning' | 'info'
  category: string
  message: string
  count: number
  sample?: string[]
}

export interface DryRunSummary {
  errorCount: number
  warningCount: number
  infoCount: number
  canProceed: boolean
}

export interface DryRunReport {
  panel: string
  issues: DryRunIssue[]
  summary: DryRunSummary
}

export interface DryRunResponse {
  ok: boolean
  message: string
  report?: DryRunReport
}

// 执行 dry-run（只读不写，检测迁移冲突）
export async function dryRun(
  config: DatabaseConfig,
  panel?: string,
): Promise<DryRunResponse> {
  const { data } = await http.post<DryRunResponse>('/dry-run', {
    ...config,
    panel,
  })
  return data
}

// ---- import 执行阶段 ----

export interface ImportRequest {
  sourceHost: string
  sourcePort: number
  sourceDatabase: string
  sourceUsername: string
  sourcePassword: string
  sourcePanel?: string
  targetHost: string
  targetPort: number
  targetDatabase: string
  targetUsername: string
  targetPassword: string
  batchSize?: number
  // 勾选的迁移模块（空数组=完整迁移）
  modules?: string[]
}

export interface ImportResponse {
  ok: boolean
  message: string
}

export type ImportStatus = 'idle' | 'running' | 'completed' | 'failed'

export interface ProgressSnapshot {
  status: ImportStatus
  phase: string
  phaseLabel: string
  total: number
  done: number
  errors: number
  startedAt?: string
  finishedAt?: string
  message: string
}

// 启动迁移（异步，返回任务已启动确认）
export async function startImport(req: ImportRequest): Promise<ImportResponse> {
  const { data } = await http.post<ImportResponse>('/import', req)
  return data
}

// 查询迁移进度（轮询）
export async function getProgress(): Promise<ProgressSnapshot> {
  const { data } = await http.get<ProgressSnapshot>('/progress')
  return data
}

// ---- 任务进度（detect/dry-run 异步任务）----

export interface TaskLogEntry {
  time: string
  level: 'info' | 'warn' | 'error'
  message: string
}

export interface TaskProgressSnapshot {
  status: ImportStatus
  phase: string
  phaseLabel: string
  total: number
  done: number
  errors: number
  startedAt?: string
  finishedAt?: string
  message: string
  logs: TaskLogEntry[]
  report?: DetectData | DryRunReport
}

// 异步启动 detect
export async function startDetectAsync(
  config: DatabaseConfig,
  panel?: string,
): Promise<ImportResponse> {
  const { data } = await http.post<ImportResponse>('/detect/start', {
    ...config,
    panel,
  })
  return data
}

// 异步启动 dry-run
export async function startDryRunAsync(
  config: DatabaseConfig,
  panel?: string,
): Promise<ImportResponse> {
  const { data } = await http.post<ImportResponse>('/dry-run/start', {
    ...config,
    panel,
  })
  return data
}

// 查询任务进度（含日志和完成后的报告）
export async function getTaskProgress(
  type: 'detect' | 'dryrun',
): Promise<TaskProgressSnapshot> {
  const { data } = await http.get<TaskProgressSnapshot>('/task/progress', {
    params: { type },
  })
  return data
}

export default http
