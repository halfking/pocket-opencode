<template>
  <div id="app">
    <!--
      ✅ 修复：用 AppLayout 包裹 router-view，让共享的 TopBar + BottomNav 全局生效。
      否则每个 view 都要自己实现顶栏/底栏，会出现重复 UI 或不一致（如之前的
      任务/会话/实例/设置 旧 4模块 Tab 遮住了设计的 5模块 BottomNav）。
    -->
    <AppLayout>
      <!-- KeepAlive 白名单只缓存列表页（LIST_CACHE_NAMES）：列表→详情→返回时
           筛选/分类/状态保留原现场；详情页不在名单内，每次进入都重新挂载拉最新。 -->
      <!-- Transition（原生顺滑度审计 P0 #1）：push=新页右滑入盖旧页视差、pop=反向
           接力右滑返回、tab=150ms fade；只动 transform/opacity。方向由
           routeTransition.beforeRouteTransition 在守卫阶段判定；离场页滚动快照、
           滑返位移接力见 onTransitionLeave。 -->
      <router-view v-slot="{ Component, route: viewRoute }">
        <Transition
          :name="transitionName"
          @enter="onTransitionEnter"
          @leave="onTransitionLeave"
          @after-leave="onAfterTransitionLeave"
        >
          <KeepAlive :include="LIST_CACHE_NAMES">
            <component :is="Component" :key="viewRoute.path" />
          </KeepAlive>
        </Transition>
      </router-view>
    </AppLayout>
    <UpdateChecker ref="updateChecker" />
    <!-- 全局确认弹窗：useConfirm().confirm() 的唯一渲染挂载点 -->
    <ConfirmDialog />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from './AppLayout.vue'
import UpdateChecker from '../components/UpdateChecker.vue'
import ConfirmDialog from '../components/base/ConfirmDialog.vue'
import { LIST_CACHE_NAMES } from '../composables/use-list-scene'
import { useSwipeBack } from '../composables/useSwipeBack'
import { useStatusBar } from '../composables/useStatusBar'
import { installKeyboardInset } from '../composables/useKeyboardInset'
import { transitionState, consumeSwipeFromPx } from './routeTransition'

const updateChecker = ref<InstanceType<typeof UpdateChecker> | null>(null)

// 全局软键盘避让（App 级单例）：键盘弹起时输入区贴键盘上沿 + 页面上滑对齐
// 聚焦字段。install 幂等，桌面浏览器 visualViewport 恒等 innerHeight 自动 no-op。
installKeyboardInset()

// Phase 4.3: 全局挂载左缘右滑返回手势（仅 route.meta.canGoBack 启用）
useSwipeBack({ edgeWidth: 24, thresholdRatio: 0.3, velocityThreshold: 0.4 })

// 状态栏控制权绑定到 App 生命周期：进入页面 start（注册主题监听），
// 卸载时 stop（清除监听），避免热更新 / 测试时累积孤儿监听器。
const statusBar = useStatusBar()
onMounted(() => {
  statusBar.start()
  console.log('Redclaw Mobile Started')
})
onBeforeUnmount(() => {
  statusBar.stop()
})

/* ── 路由转场（审计 P0 #1/#2 + P1 #9）──
   方向在 router 守卫（beforeRouteTransition）里先行写入 transitionState，
   渲染时读取即可；读 route.fullPath 建立响应依赖让 computed 每次导航重算。
   钩子只接收 (el)（不声明 done），Vue 才会继续做 CSS transition 结束自动探测。 */
const viewRoute = useRoute()
const transitionName = computed(() => {
  void viewRoute.fullPath
  switch (transitionState.direction) {
    case 'push':
      return 'nav-push'
    case 'pop':
      return 'nav-pop'
    case 'tab':
      return 'nav-tab'
    default:
      return 'nav-none'
  }
})

/** enter：首帧绘制前把滚动容器恢复到目标页记忆位置（push=0 / pop与tab=记忆值） */
function onTransitionEnter(el: Element) {
  const main = (el as HTMLElement).parentElement
  if (main && transitionState.pendingScrollTop > 0) {
    main.scrollTop = transitionState.pendingScrollTop
  }
}

/**
 * leave：离场页三件事，全部在绘制前的同一 flush 里完成，无中间帧：
 *  1. 滚动快照——shell 滚动页的 scrollTop 在 main 上，离场页转 absolute
 *     （偏移精确对齐 padding box）后把滚动搬进自身，避免转场期间旧页跳顶；
 *  2. 右滑返回接力——手势提交时 useSwipeBack 留在 main 上的拖拽位移在此清掉，
 *     视觉起点由 leave-from 的 --swipe-from 接管（原生顺滑度审计 A2）；
 *  3. main.scrollTop 收敛到目标页应处的位置。
 */
function onTransitionLeave(el: Element) {
  if (transitionState.direction === 'none') return
  const hEl = el as HTMLElement
  const main = hEl.parentElement
  if (!main) return

  const leavingTop = main.scrollTop
  const swipePx = consumeSwipeFromPx()
  if (transitionState.direction === 'pop' && swipePx != null) {
    hEl.style.setProperty('--swipe-from', `${swipePx}px`)
  }

  const cs = getComputedStyle(main)
  hEl.style.position = 'absolute'
  hEl.style.top = cs.paddingTop
  hEl.style.right = cs.paddingRight
  hEl.style.bottom = cs.paddingBottom
  hEl.style.left = cs.paddingLeft
  hEl.style.overflowY = 'auto'
  hEl.scrollTop = leavingTop

  main.style.transition = ''
  main.style.transform = ''
  main.style.opacity = ''
  main.scrollTop = transitionState.pendingScrollTop
}

/** after-leave：清掉快照期内联样式（KeepAlive 缓存的根节点会复用，必须还原） */
function onAfterTransitionLeave(el: Element) {
  const hEl = el as HTMLElement
  hEl.style.removeProperty('--swipe-from')
  hEl.style.position = ''
  hEl.style.top = ''
  hEl.style.right = ''
  hEl.style.bottom = ''
  hEl.style.left = ''
  hEl.style.overflowY = ''
  hEl.scrollTop = 0
}
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html {
  height: 100%;
}

body {
  /* 显式 CJK 字体名理由见 styles.css body 注释（iOS 26.3 模拟器 fallback 缺陷） */
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
    "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei",
    "Helvetica Neue", Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  /* 走 token 以跟随亮/暗皮肤（styles.css 有同款规则，此处去硬编码） */
  background: var(--bg-base, #f7f7fa);
  color: var(--text-primary, #0a0a0a);
  height: 100%;
  overflow: hidden;
}

#app {
  /* 软键盘避让：--kb-inset 由 useKeyboardInset 实时写入（visualViewport
     高度差）。键盘弹起时根布局收缩，flex 停靠的输入区贴住键盘上沿，
     滚动容器随之收缩供聚焦字段对齐到输入区上沿之上。 */
  height: calc(100% - var(--kb-inset, 0px));
  min-height: 0;
  overflow: hidden;
}

input, textarea, select, button {
  font-family: inherit;
}

/* 多行输入的全局舒适基线（2026-10-03 审计）
 *
 * 诉求：「所有有多行文本输入的区域，输入的内容展示要尽可能地完整，
 * 区域足够大，让人感觉舒服，不要太小或看不完整。」
 *
 * 现状是 21 处裸 <textarea> 各自手写尺寸：有的 rows="1"、有的没写 rows
 * （浏览器默认 2 行）、min-height 有 60px 有 80px 有 112px 也有干脆没有。
 * 逐个改是一锤子定音不了的——下一个人加个 textarea 又回到老样子。
 *
 * 所以把「下限」提到全局，只给下限、不给上限：
 *   - min-height 72px ≈ 3 行，任何多行输入都不是一条缝；
 *   - resize: vertical 让用户能自己拖大（这是移动端唯一可靠的手动手段，
 *     iOS/Android 键盘上都没有放大控件）。
 *
 * **紧凑型豁免**：`.textarea-compact` 覆盖这条下限。它给「列表里每项都带
 * 一个」的场景用——例如 ApprovalPanel 每张待批卡片一个 rows=1 的备注框，
 * 5 张卡片 × 96px 就是 480px 空白，列表直接被撑垮。这些框的正确做法不是
 * 垫高，而是**平时 1 行、用户真写了才长高**（useAutoGrowTextarea），
 * 两者互补：紧凑型用动态长高，表单型用固定下限。
 *
 * 下限必须能被单点覆盖：UnifiedComposer 自适应增高（高度由 JS 写 inline
 * style，优先级高于本规则）、转写回显等各有自己的 class 规则。 */
textarea:not(.textarea-compact) {
  min-height: 72px;
  resize: vertical;
}

/* 同一处规则也写进 styles.css：App.vue 与 styles.css 的注入顺序无关，
   两条都留着，避免其中一个没加载时基线整条消失（与 #app 高度那条同理）。 */

input:focus, textarea:focus, select:focus {
  outline: none;
}

/* 滚动条样式 */
::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

::-webkit-scrollbar-thumb {
  background: var(--border-strong, #d1d5db);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: var(--text-muted, #a3a3a3);
}

/* 触摸反馈：与 styles.css 的全局按压态同款（审计 A5——scale 微缩替代纯降透明度，
   双写保证两处样式表注入顺序无关） */
button:active {
  opacity: 0.85;
  transform: scale(0.97);
}
</style>
