(function(){
  const inputs = Array.from(document.querySelectorAll('input, textarea'));
  const matches = inputs.filter(i => (i.placeholder || '').toLowerCase().includes('kxpms') || (i.placeholder || '').toLowerCase().includes('llm'));
  return {
    total: inputs.length,
    matching_placeholders: matches.map(i => ({tag: i.tagName, placeholder: i.placeholder, value: i.value}))
  };
})()