// adb-prereq.mjs —— 真机门禁的前置检测：**设备不在**要能说清「没跑到被检查对象」。
//
// 为什么需要它（2026-10-05）
// --------------------------
// check-unlock-focus / check-cdp-pid-strict / check-fts-triggers-device 三道
// 过去都是 `execFileSync(adb, ...)` 裸调。设备不在时 execFileSync 抛异常，
// 顶层没有任何 catch ⇒ node 打出**未捕获异常 + 15 行栈**，退出码 1。
//
// 问题不在于「它红」，而在于 **退出码 1 同时表示三件完全不同的事**：
//   1. 这道门判红了（真的发现缺陷）
//   2. 设备没连上（根本没跑到被检查对象）
//   3. 脚本自己有 bug
// 一次全量门禁扫描里，1 和 3 值得追，2 应该被跳过。三者混在一起时，
// 人只会盯着「红」看，于是要么去查一个根本没跑过的判定，要么把真红一起放过。
//
// 退出码约定（与 scripts/run-gates.mjs 头注释一致）：**≥3 = 拒绝给结论**，
// 不是普通断言失败。所以这里用 3，扫描器一眼就能把它和真判红分开。
//
// ⚠️ 只降级「设备确实不在」这一类错误。其它 adb 失败（命令拼错、超时、
//    shell 报错、权限被拒）**照原样抛**：把它们一并降级成「前置缺失」，
//    等于把「我的观测手段坏了」说成「环境没准备好」，方向正好反了。
import { execFileSync } from 'node:child_process'

export const ADB_BIN = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
export const ADB_SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'

// adb 在「目标设备不在」时会说这些话。逐条实测抄下来的，不是猜的。
const DEVICE_ABSENT_PATTERNS = [
  /device '.*' not found/i, // adb.exe: device '192.168.31.19:5555' not found
  /no devices\/emulators found/i,
  /device offline/i,
  /device unauthorized/i,
  /more than one device/i, // 没带 -s，多设备歧义
  /error:\s*cannot connect to daemon/i,
]

/** 这条 adb 失败是不是「设备不在」？导出以便单独自测。 */
export function isDeviceAbsent(err) {
  const blob = `${(err && err.stderr) || ''} ${(err && err.message) || ''} ${String(err)}`
  return DEVICE_ABSENT_PATTERNS.some((re) => re.test(blob))
}

/**
 * 跑一条 adb 命令；**设备不在**时打印一句人话并 exit 3，其它错误照抛。
 *
 * @param {string[]} args  adb 参数（不含 adb 可执行文件本身）
 * @param {{serial?:string|null, timeout?:number, bin?:string, label?:string}} [opts]
 *        serial 传 null 表示调用方自己已经把 -s 放进 args（fts 那道就是）。
 * @returns {string} stdout
 */
export function adbOrExit(args, opts = {}) {
  const { serial = ADB_SERIAL, timeout = 60000, bin = ADB_BIN, label = '' } = opts
  const argv = serial ? ['-s', serial, ...args] : args
  try {
    return execFileSync(bin, argv, { encoding: 'utf8', timeout, maxBuffer: 33554432 })
  } catch (e) {
    if (isDeviceAbsent(e)) {
      console.error(
        `[前置缺失] ${label || '本门禁'} 没跑到被检查对象：adb 上没有可用设备` +
          `${serial ? `（serial=${serial}）` : ''}。\n` +
          '  这是「无法判定」，不是「判定为不通过」——所以退出码是 3 而不是 1。\n' +
          '  接上设备后重跑；不要把这一条当成门禁判红去排查。',
      )
      process.exit(3)
    }
    throw e
  }
}
