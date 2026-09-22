(function(){
  const ta = document.querySelector('textarea.uc-input');
  if (!ta) return {ok:false, err:'no-textarea'};
  const prompt = [
    '用 Kotlin 实现一个 O(1) 时间复杂度的 LRU 缓存。要求：',
    '1. get / put 操作必须严格 O(1)',
    '2. 当缓存满时自动淘汰最久未使用的条目',
    '3. 容量可在构造时指定，并支持运行期动态调整',
    '4. 用例：演示 1e6 次 get/put 混合调用，验证热点数据命中率 > 90%',
    '5. 单元测试覆盖以下边界 case：',
    '   (a) 容量为 0 / 1 / N 的退化场景',
    '   (b) put 已存在 key 的更新语义',
    '   (c) get 不存在的 key 不抛异常',
    '   (d) 并发场景下不会出现脏数据（用 ReentrantLock 或 synchronized 块）',
    '6. 解释为什么 HashMap + 双向链表的组合在严格 O(1) 实现上优于 LinkedHashMap 内部默认 accessOrder=false 改造的方案。',
    '请给出完整代码 + 测试 + 简短复杂度分析。'
  ].join('\n');
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
  setter.call(ta, prompt);
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  return { ok: true, len: ta.value.length, sample: ta.value.substring(0, 60) };
})()