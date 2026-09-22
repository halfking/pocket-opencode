(function(){
  const ta = document.querySelector('textarea.uc-input');
  if (!ta) return { ok: false, err: 'no-textarea' };
  // Try multiple submit paths
  const results = {};

  // 1. Press Enter on textarea (with shift modifier disabled — default form submit)
  // First dispatch a 'change' and 'blur' to flush Vue state
  ta.dispatchEvent(new Event('change', { bubbles: true }));

  // 2. Try calling Vue's submit method via composer ref (look in app instance)
  const app = document.querySelector('#app')?.__vue_app__;
  results.hasApp = !!app;
  if (app) {
    // Try to find the component with composerRef
    const findSubmit = (instance, depth = 0) => {
      if (depth > 10) return null;
      const refs = instance?.refs || {};
      if (refs.composerRef && refs.composerRef.submit) {
        return refs.composerRef;
      }
      // Check direct setupState
      const setup = instance?.setupState || {};
      for (const k of Object.keys(setup)) {
        if (setup[k]?.submit) return setup[k];
        if (setup[k]?.value?.submit) return setup[k].value;
      }
      if (instance?.subTree) {
        // Try children
      }
      return null;
    };
    // Walk all components via app._instance
    let composer = null;
    const walk = (inst) => {
      if (!inst) return;
      const r = findSubmit(inst);
      if (r) { composer = r; return; }
      if (inst.subTree && inst.subTree.component) walk(inst.subTree.component);
      if (inst.subTree && inst.subTree.children) {
        for (const c of inst.subTree.children) {
          if (c && c.component) walk(c.component);
        }
      }
    };
    walk(app._instance);
    if (composer && composer.submit) {
      results.path = 'composer.submit()';
      composer.submit();
      return { ok: true, path: results.path };
    }
  }

  // 3. Fallback: click submit button via DOM
  const btn = document.querySelector('button.send-btn:not(.stop)');
  if (btn && !btn.disabled) {
    btn.click();
    results.path = 'btn.click()';
    return { ok: true, path: results.path };
  }
  return { ok: false, results };
})()