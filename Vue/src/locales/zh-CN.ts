// 简体中文语言包
export default {
  common: {
    title: 'NPanel 数据迁移工具',
    testConnection: '测试连接',
    connecting: '连接中...',
    connectionSuccess: '连接成功',
    connectionFailed: '连接失败',
    generateReport: '生成迁移报告',
    dryRun: '预演（Dry-Run）',
    startMigration: '开始迁移',
    generating: '生成中...',
  },

  panels: {
    auto: '自动检测',
    xiaov2board: 'xiaov2board',
    v2board: 'v2board',
    xboard: 'Xboard',
    ppanel: 'PPanel',
    sspanel: 'SSPanel-UIM',
  },

  // 面板选择分组标签
  panelLabel: '面板类型',
  selectPanelPlaceholder: '选择迁移源面板',

  // 源端面板
  sourcePanel: '源端面板',
  databaseHost: '连接地址',
  databasePort: '端口',
  databaseName: '数据库名',
  username: '账号',
  password: '密码',
  databasePassword: '数据库密码',

  // 目标端
  targetPanel: '目标端 NPanel',

  // 迁移模式
  migrationMode: '迁移模式',
  modeFull: '完整迁移',
  modeFullDesc:
    '迁移全部核心数据：用户、套餐、订阅、订单、节点、优惠券、工单、公告等。适合全新 NPanel 库的首次迁移。',
  modePartial: '部分迁移',
  modePartialDesc:
    '仅迁移勾选的数据模块（如只迁用户+订阅）。适合已有 NPanel 数据、只需补充部分实体的场景。',
  modeArchive: '仅归档（Archive-only）',
  modeArchiveDesc:
    '只读取源库生成迁移报告，不写入目标库。用于迁移前评估数据量、检测冲突。',
  modeCustom: '自定义',
  modeCustomDesc: '根据下方勾选的模块迁移对应数据。',
  migrationModules: '迁移模块',
  selectAll: '全选',
  moduleUsers: '用户',
  moduleUsersDesc: '用户账号、邮箱认证、邀请关系',
  modulePlans: '套餐',
  modulePlansDesc: '套餐配置、价格档位（周期拆分）',
  moduleOrders: '订单',
  moduleOrdersDesc: '历史订单记录（不触发开通）',
  moduleSubscriptions: '订阅',
  moduleSubscriptionsDesc: '用户当前订阅、流量、到期时间',
  moduleNodes: '节点',
  moduleNodesDesc: '服务器节点（VMess/Trojan/Shadowsocks 等协议）',
  moduleCoupons: '优惠券',
  moduleCouponsDesc: '优惠码、折扣规则',
  moduleNotices: '公告',
  moduleNoticesDesc: '站点公告、通知',
  moduleTickets: '工单',
  moduleTicketsDesc: '工单及工单消息记录',

  // 检测报告
  detectReport: '迁移前报告（Detect）',
  coreTablesTotalRows: '核心表总行数：{rows}',
  businessMetrics: '关键业务指标',
  tableRowsDetail: '数据表行数明细',
  largestTable: '数据量最大的表',
  tableName: '表名',
  tableComment: '业务含义',
  rowCount: '行数',
  metricUserTotal: '用户总数',
  metricUserActive: '有效用户',
  metricBalanceTotal: '用户余额总和',
  metricActiveSubscribers: '有效订阅数',
  metricPlanTotal: '套餐总数',
  metricOrderTotal: '订单总数',
  metricNodeTotal: '节点总数',
  metricCouponTicket: '优惠券/工单',
  onSaleSuffix: '/ 在售 {n}',
  bannedSuffix: '/ 封禁 {n}',
  completedSuffix: '/ 完成 {n}',
  ticketSuffix: '/ 工单 {n}',

  // dry-run 预演报告
  dryRunReport: '预演报告（Dry-Run）',
  canProceed: '可以开始迁移',
  hasBlockingIssues: '存在阻断问题',
  errorCount: '错误 {n}',
  warningCount: '警告 {n}',
  infoCount: '提示 {n}',
  severityCol: '级别',
  issueCol: '问题描述',
  affectedCol: '受影响数',
  severity: {
    error: '错误',
    warning: '警告',
    info: '提示',
  },
  msgDryRunCompleted: '预演完成',
  msgDryRunFailed: '预演失败',
  msgDryRunGenerateFailed: '预演失败：{err}',

  // import 执行进度
  importProgress: '迁移进度',
  phaseIdle: '等待开始',
  processed: '已处理',
  errors: '错误',
  elapsed: '耗时',
  importStatus: {
    idle: '空闲',
    running: '运行中',
    completed: '已完成',
    failed: '失败',
  },
  msgImportStarted: '迁移任务已启动',
  msgImportBusy: '已有迁移任务运行中',
  msgImportFailed: '启动迁移失败',
  confirmStartMigration: '即将开始正式迁移（写入数据），此操作不可撤销。确认继续？',
  cancel: '取消',
  close: '关闭',
  realtimeLogs: '实时日志',
  noLogs: '暂无日志',
  detectingTitle: '正在预读数据库...',
  dryRunningTitle: '正在预演检测...',
  importingTitle: '正在执行迁移...',

  // 消息提示
  msgSourceConnected: '源端连接成功',
  msgSourceConnectFailed: '源端连接失败',
  msgSourceTestFailed: '源端连接测试失败：{err}',
  msgTargetConnected: '目标端连接成功',
  msgTargetNotNPanel: '目标端连接成功，但非 NPanel 库',
  msgTargetConnectFailed: '目标端连接失败',
  msgTargetTestFailed: '目标端连接测试失败：{err}',
  msgReportGenerated: '迁移报告生成成功',
  msgReportFailed: '生成报告失败',
  msgReportGenerateFailed: '生成报告失败：{err}',

  // 语言
  language: '语言',

  // 底部
  footer: {
    producedBy: '由 {brand} 出品',
    github: 'GitHub 仓库',
  },
}
