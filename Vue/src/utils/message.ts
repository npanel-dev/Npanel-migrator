// 统一的消息提示工具。
//
// Element Plus 的 ElMessage 默认 duration=3000ms，会自动消失。
// 这里封装一层，显式统一为 3 秒 + 可手动关闭，避免各处调用风格不一致。
import { ElMessage } from 'element-plus'

const DURATION = 3000 // 3 秒后自动消失

export function success(message: string) {
  ElMessage.success({ message, duration: DURATION, showClose: true })
}

export function warning(message: string) {
  ElMessage.warning({ message, duration: DURATION, showClose: true })
}

export function error(message: string) {
  ElMessage.error({ message, duration: DURATION, showClose: true })
}

export function info(message: string) {
  ElMessage.info({ message, duration: DURATION, showClose: true })
}

export default { success, warning, error, info }
