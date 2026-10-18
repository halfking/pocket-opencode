/**
 * download 编码 helpers（Phase 9.4 抽出单测覆盖）。
 *
 * 抽离原因：
 *   - utils/download.ts 顶层 import pocket-native，node ESM 跑单测时会触发
 *     '@capacitor/core' 模块解析（即便 ESM 解析顺序允许），不利于纯函数契约测试。
 *   - 抽到独立模块，download.ts 引它（带 .ts 后缀）；单测文件引它（带 .ts 后缀），
 *     不经过 pocket-native 的 cascade import。
 *
 * 契约：
 *   - utf8ToBase64: UTF-8 安全（中文/emoji 不抛 InvalidCharacterError）
 *   - blobToBase64: 纯 base64（去除 data:... 前缀，与 PocketFilesystem.writeFile 对齐）
 *   - arrayBufferToBase64: chunk 拆分 0x8000 防止 btoa 超参
 */

/**
 * UTF-8 安全 base64 编码（避免 btoa 对中文字符抛 InvalidCharacterError）。
 */
// §891：实现搬进 utils/base64.ts（这一族曾有 3 份同义实现），公共 API 不变。
export { utf8ToBase64 } from './base64.ts'

// §888：原来这里另外实现了一份 blobToBase64（公共实现在 base64.ts，
//   两份都已有 null 处理与 reject，行为等价）⇒ 改为 re-export，公共 API 不变。
// §889：收编到公共实现（三份同义实现收为一份）。
export { blobToBase64, arrayBufferToBase64 } from './base64.ts'
