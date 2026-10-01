// 前置动作：点「收起」把分诊区折叠掉，让真实任务列表进可视区。
// 用法：POCKET_HIER_PRE_JS=scripts/pre-collapse-triage.js node scripts/hier-dump.mjs '#/tasks'
(function () {
  var bs = Array.prototype.slice.call(document.querySelectorAll('button'))
  var hit = 0
  for (var i = 0; i < bs.length; i++) {
    if ((bs[i].textContent || '').trim() === '收起') { bs[i].click(); hit++; break }
  }
  return 'collapse=' + hit
})()
