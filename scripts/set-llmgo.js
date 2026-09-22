(function(){
  // Get current localStorage state
  const before = {
    pocket_api_base: localStorage.getItem('pocket_api_base'),
    all_storage_keys: Object.keys(localStorage)
  };

  // The ai-chat store pulls /api/llm/models via the resolved API base.
  // The LLM gateway URL lives in the per-user settings under namespace 'llm_gateway',
  // key 'default' — set by SettingsLLMGateway.vue saveSettingLocalFirst.
  // We update both:
  //   1. The ai-chat local cache (if any)
  //   2. The settings 'llm_gateway:default' payload.baseURL

  // Settings are stored via @capacitor/preferences or sqlite-web; localStorage only
  // holds the API base override. The actual LLM gateway is read at request time.

  // Set the API base override to the placeholder pocket entry. The backend will proxy.
  const newApiBase = 'https://llmgo.kxpms.cn/v1';
  // Note: this is a "what if the WebView talked directly to llmgo" test; in production
  // the api base should be the pocket backend URL, not the LLM gateway itself.
  // We intentionally do NOT set pocket_api_base to llmgo here because that bypasses
  // pocket's BFF layer (/api/llm/stream) and breaks the existing chat architecture.

  // Verify current state instead
  const out = {
    before,
    note: 'LLM gateway URL is server-side via POCKET_LLM_GATEWAY_URL env (backend), or per-user via Settings → AI 模型. localStorage does not store the gateway URL directly.',
    current_settings_store_count: 'unknown — uses @capacitor/preferences or sqlite-web'
  };

  return out;
})()