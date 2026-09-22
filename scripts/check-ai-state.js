(function(){
  // Try to inspect the Pinia stores to understand AI state
  const app = document.querySelector('#app')?.__vue_app__;
  if (!app) return { ok: false, err: 'no app' };
  const piniaState = app.config.globalProperties.$pinia?.state?.value || {};
  const stores = Object.keys(piniaState);
  const summary = {};
  for (const k of stores) {
    const s = piniaState[k];
    summary[k] = Object.keys(s).slice(0, 25).reduce((acc, key) => {
      const v = s[key];
      if (typeof v === 'function' || v === null || v === undefined) return acc;
      if (Array.isArray(v)) return { ...acc, [key]: `array(${v.length})` };
      if (typeof v === 'object') return { ...acc, [key]: `object{${Object.keys(v).slice(0, 8).join(',')}}` };
      return { ...acc, [key]: typeof v === 'string' ? v.substring(0, 50) : v };
    }, {});
  }
  return { ok: true, stores, summary };
})()