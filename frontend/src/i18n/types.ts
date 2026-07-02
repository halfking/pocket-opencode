// TypeScript 类型定义文件
// 用于 vue-i18n 的类型支持

import type zhCN from '../locales/zh-CN.json'

// 定义消息模式类型
export type MessageSchema = typeof zhCN

// 扩展 vue-i18n 模块
declare module 'vue-i18n' {
  // 定义 useI18n 的返回类型
  export interface DefineLocaleMessage extends MessageSchema {}
  
  // 定义日期时间格式
  export interface DefineDateTimeFormat {}
  
  // 定义数字格式
  export interface DefineNumberFormat {}
}
