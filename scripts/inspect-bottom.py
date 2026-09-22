import json
import urllib.request
import websocket

req = urllib.request.urlopen('http://localhost:9222/json')
pages = json.loads(req.read())
page = pages[0]
page_id = page['id']

ws = websocket.create_connection(
    f"ws://localhost:9222/devtools/page/{page_id}",
    suppress_origin=True
)

# Element at the bottom of the viewport
expr = """
(function() {
  var el = document.elementFromPoint(360, 800);
  var rect = el ? el.getBoundingClientRect() : null;
  var bg = el ? getComputedStyle(el).backgroundColor : null;
  return JSON.stringify({
    tag: el ? el.tagName : null,
    cls: el ? el.className : null,
    rect: rect ? {top: rect.top, bottom: rect.bottom, height: rect.height} : null,
    bg: bg,
    parentTag: el && el.parentElement ? el.parentElement.tagName : null,
    parentCls: el && el.parentElement ? el.parentElement.className : null,
    parentBg: el && el.parentElement ? getComputedStyle(el.parentElement).backgroundColor : null
  });
})()
"""
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{'expression':expr,'returnByValue':True}}))
print(ws.recv())
ws.close()
