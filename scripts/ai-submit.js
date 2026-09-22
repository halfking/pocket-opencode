(function(){
  const btns = document.querySelectorAll('button.uc-submit, .send-btn:not(.stop)');
  const list = Array.from(btns).map(b => ({
    cls: b.className,
    disabled: b.disabled,
    visible: !!b.offsetParent
  }));
  // Click first enabled submit
  const btn = Array.from(btns).find(b => !b.disabled && b.offsetParent);
  if (btn) {
    btn.click();
    return { ok: true, clicked: btn.className, list };
  }
  // Fallback: try dispatching submit via the composerRef
  return { ok: false, list };
})()