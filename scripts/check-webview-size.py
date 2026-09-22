import json, websocket
ws = websocket.create_connection('ws://localhost:9222/devtools/page/CF2CCAB1BC7BFFABFD214ED04E3A82D4')
expr = """
JSON.stringify({
  innerHeight: window.innerHeight,
  vvHeight: window.visualViewport ? window.visualViewport.height : null,
  docHeight: document.documentElement.scrollHeight,
  bodyHeight: document.body.scrollHeight,
  appHeight: document.getElementById('app') ? document.getElementById('app').offsetHeight : null,
  appLayoutHeight: document.querySelector('.app-layout') ? document.querySelector('.app-layout').offsetHeight : null,
  loginViewHeight: document.querySelector('.login-view') ? document.querySelector('.login-view').offsetHeight : null,
  htmlBg: getComputedStyle(document.documentElement).backgroundColor,
  bodyBg: getComputedStyle(document.body).backgroundColor,
  appBg: document.getElementById('app') ? getComputedStyle(document.getElementById('app')).backgroundColor : null,
  appLayoutBg: document.querySelector('.app-layout') ? getComputedStyle(document.querySelector('.app-layout')).backgroundColor : null,
  loginViewBg: document.querySelector('.login-view') ? getComputedStyle(document.querySelector('.login-view')).backgroundColor : null,
  kbInset: getComputedStyle(document.documentElement).getPropertyValue('--kb-inset'),
  safeTop: getComputedStyle(document.documentElement).getPropertyValue('--app-safe-top'),
  safeBottom: getComputedStyle(document.documentElement).getPropertyValue('--app-safe-bottom')
})
"""
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{'expression':expr,'returnByValue':True}}))
print(ws.recv())
