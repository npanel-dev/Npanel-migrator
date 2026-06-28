// i18n 初始化。
//
// 支持简体中文（默认）和英文，语言选择持久化到 localStorage。
// 在右上角下拉切换时，setLocale 会更新 i18n 全局状态 + 写入 localStorage。
import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN'
import en from './en'

export type Locale = 'zh-CN' | 'en'

export const supportedLocales: { value: Locale; label: string }[] = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'en', label: 'English' },
]

const STORAGE_KEY = 'npanel-migrator-locale'

// 从 localStorage 读取上次选择的语言，默认简体中文。
function loadLocale(): Locale {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'en' || saved === 'zh-CN') {
    return saved
  }
  // 浏览器语言自动探测。
  return navigator.language.startsWith('zh') ? 'zh-CN' : 'en'
}

const i18n = createI18n({
  legacy: false, // 使用 Composition API 模式
  locale: loadLocale(),
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': zhCN,
    en,
  },
})

// 切换语言（供右上角下拉调用）。
export function setLocale(locale: Locale) {
  i18n.global.locale.value = locale
  localStorage.setItem(STORAGE_KEY, locale)
  // 同步 html lang 属性。
  document.documentElement.lang = locale
}

// 初始化时同步 html lang。
document.documentElement.lang = i18n.global.locale.value

export default i18n
