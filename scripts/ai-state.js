(function(){
  const url = location.href;
  const ta = document.querySelector('textarea.uc-input');
  const taValue = ta ? ta.value.length : -1;
  const sendBtn = document.querySelector('button.send-btn:not(.stop)');
  const sendDisabled = sendBtn ? sendBtn.disabled : null;
  const stopBtn = document.querySelector('button.send-btn.stop');
  const stopVisible = stopBtn ? !!stopBtn.offsetParent : false;
  const bubbles = document.querySelectorAll('[class*="message"], [class*="msg-"], .uc-msg, .ai-bubble, .user-bubble, .chat-message, .conv-message, .markdown-body');
  const bubbleTexts = Array.from(bubbles).slice(0, 6).map(b => (b.textContent || '').substring(0, 80));
  return { url, taValue, sendDisabled, stopVisible, bubbleCount: bubbles.length, bubbleTexts };
})()