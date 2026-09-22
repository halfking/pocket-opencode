import json, sys
import urllib.request

# Get the page id
req = urllib.request.urlopen('http://localhost:9222/json')
pages = json.loads(req.read())
page = pages[0]
page_id = page['id']
print(f"page_id: {page_id}")
print(f"page dimensions from CDP: {page.get('description', 'n/a')}")

# Use raw websockets without origin restriction
import websocket
ws = websocket.create_connection(
    f"ws://localhost:9222/devtools/page/{page_id}",
    suppress_origin=True
)
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{
    'expression':"JSON.stringify({innerHeight: window.innerHeight, vvHeight: window.visualViewport ? window.visualViewport.height : null, docHeight: document.documentElement.scrollHeight, bodyHeight: document.body.scrollHeight, appHeight: document.getElementById('app') ? document.getElementById('app').offsetHeight : null, appLayoutHeight: document.querySelector('.app-layout') ? document.querySelector('.app-layout').offsetHeight : null, loginViewHeight: document.querySelector('.login-view') ? document.querySelector('.login-view').offsetHeight : null, htmlBg: getComputedStyle(document.documentElement).backgroundColor, bodyBg: getComputedStyle(document.body).backgroundColor, appBg: document.getElementById('app') ? getComputedStyle(document.getElementById('app')).backgroundColor : null, appLayoutBg: document.querySelector('.app-layout') ? getComputedStyle(document.querySelector('.app-layout')).backgroundColor : null, loginViewBg: document.querySelector('.login-view') ? getComputedStyle(document.querySelector('.login-view')).backgroundColor : null, kbInset: getComputedStyle(document.documentElement).getPropertyValue('--kb-inset'), safeTop: getComputedStyle(document.documentElement).getPropertyValue('--app-safe-top'), safeBottom: getComputedStyle(document.documentElement).getPropertyValue('--app-safe-bottom'), androidSafeTop: getComputedStyle(document.documentElement).getPropertyValue('--android-safe-top')})",
    'returnByValue':True
}}))
result = ws.recv()
print(result)
ws.close()
