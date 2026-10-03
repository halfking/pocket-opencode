/**
 * useAutoGrowTextarea — 多行输入「内容驱动高度」的实现。
 *
 * ## 为什么要有它
 *
 * 用户诉求：「所有有多行文本输入的区域，输入的内容展示要尽可能地完整，
 * 区域足够大，让人感觉舒服，不要太小或看不完整。」
 *
 * `<textarea rows="3">` 是**静态**的：写到第 4 行，内容就藏到内部滚动条
 * 后面去了。移动端最常见的输入挫败感就来自这里——用户盯着自己刚打的字
 * 看不到。微信 / Telegram / Messages 都是内容驱动高度（长到上限才转滚动），
 * 这是被验证过的交互，本模块把它变成一行调用。
 *
 * ## 怎么做的（以及为什么是这三行）
 *
 *   el.style.height = 'auto'            // ① 先归零
 *   el.style.height = el.scrollHeight   // ② 再量内容真实高度
 *
 * ①是**必须的**，不是保险：不清零的话高度只会单向变大，删字之后框不会缩
 * 回去——用户删掉一大段，框还留着一大片空白。
 *
 * ②依赖 `box-sizing: border-box`（本项目 `styles.css` 顶部 `*` 已全局设过）。
 * 若哪天改成 content-box，`height = scrollHeight` 会把 padding+border 再加
 * 一遍，每敲一次多长几像素、无限增长。所以这里显式写死 border-box。
 *
 * ②的边界：border-box 下 `height` 覆盖的是「padding 盒 + 上下边框」，而
 * `scrollHeight` 是「内容 + padding、**不含边框**」。直接写 `height =
 * scrollHeight` 会让内容盒比内容矮一个上下边框之和——实测 1px 边框的
 * textarea 写入长文后 `scrollHeight 121 / clientHeight 119`，**最后一行被
 * 裁掉 2px**。这正是用户说的「内容看不完整」，所以要把上下边框补回去。
 * 负控见 `__tests__/useAutoGrowTextarea.test.mjs` 的「border-box 下补边框」一条。
 *
 * 上限不在 JS 里复制：max-height 交给 CSS（.uc-input 是 40vh），超过后由
 * `overflow-y: auto` 接管滚动。两段行为一次到位，也省得 JS 与 CSS 两处
 * 上限数字漂移。
 *
 * ## 调用点必须覆盖三类来源
 *
 * 手动输入、程序化改值（STT 转写 / AI 优化 / 模板插入 / 提交后清空）、
 * 首次挂载。只挂 `@input` 会漏掉后两类——那些路径不经过 input 事件，
 * 文本凭空出现而高度不动。`useAutoGrowTextarea` 把这三类一起接上。
 */

import { nextTick, onMounted, ref, watch } from 'vue'

/**
 * 把目标 textarea 的高度对齐到内容高度。
 *
 * 幂等：连续调用安全。传空值时按已绑定的 ref 找，都拿不到就静默返回
 * （组件卸载后 watcher 仍可能触发，不该抛）。
 */
export function autoGrow(el?: HTMLTextAreaElement | null): void {
  const target = el
  if (!target) return
  // box-sizing 见文件头：不锁死就会每敲一次多长 padding
  target.style.boxSizing = 'border-box'
  // ① 先归零，让浏览器按内容重新排版
  target.style.height = 'auto'
  // ② 再写回内容真实高度。scrollHeight 已含 padding、不含 border，而
  //    border-box 的 height 覆盖 padding 盒 + 上下边框，所以必须把边框补回去，
  //    否则内容盒比内容矮 2×边框，最后一行被裁（实测 1px 边框 → 裁 2px）。
  //    经 ownerDocument 取而不是全局 getComputedStyle：SSR / Node 单测里
  //    没有该全局函数，退化成 0 边框（无边框时本就无需补偿）。
  const view = target.ownerDocument?.defaultView
  const cs = view?.getComputedStyle ? view.getComputedStyle(target) : null
  const borderY =
    (parseFloat(cs?.borderTopWidth ?? '') || 0) + (parseFloat(cs?.borderBottomWidth ?? '') || 0)
  target.style.height = `${target.scrollHeight + borderY}px`
}

/**
 * 绑一个模板 ref + 当前值，返回随输入事件调用 autoGrow 的处理函数。
 *
 * @param modelValue 受控值。变化时（含程序化改值）自动重算高度。
 * @param boxRef     模板 ref：`ref="boxRef"`，指向那个 textarea。
 */
export function useAutoGrowTextarea(
  modelValue: () => string,
  boxRef: { value: HTMLTextAreaElement | null },
) {
  /** 挂在 `<textarea @input="onInput">` 上。 */
  function onInput(e: Event) {
    autoGrow(e.target as HTMLTextAreaElement)
  }

  // 程序化改值：STT 转写 / AI 优化 / 模板插入 / 提交后清空
  watch(modelValue, () => {
    nextTick(() => autoGrow(boxRef.value))
  })

  // 首帧：初次挂载时的内容（路由带参预填、草稿恢复）也要算高度
  onMounted(() => {
    nextTick(() => autoGrow(boxRef.value))
  })

  return { onInput, autoGrow: () => autoGrow(boxRef.value) }
}

/** 单元素便捷入口：只需要「这个 textarea 自适应」时用。 */
export function useAutoGrow(el: { value: HTMLTextAreaElement | null }) {
  const holder = ref<HTMLTextAreaElement | null>(el.value)
  return {
    register(node: HTMLTextAreaElement | null) {
      holder.value = node
      nextTick(() => autoGrow(node))
    },
    autoGrow: () => autoGrow(holder.value),
  }
}
