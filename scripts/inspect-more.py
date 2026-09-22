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

# Inspect various points
expr = """
(function() {
  var points = [
    {x: 360, y: 0, label: 'top'},
    {x: 360, y: 400, label: 'middle'},
    {x: 360, y: 780, label: 'near bottom'},
    {x: 360, y: 800, label: 'bottom edge'},
    {x: 360, y: 810, label: 'past bottom'},
    {x: 360, y: 819, label: 'very bottom'}
  ];
  var results = [];
  for (var i = 0; i < points.length; i++) {
    var p = points[i];
    var el = document.elementFromPoint(p.x, p.y);
    results.push({
      label: p.label,
      x: p.x,
      y: p.y,
      tag: el ? el.tagName : null,
      cls: el ? (typeof el.className === 'string' ? el.className.substring(0,40) : null) : null
    });
  }
  return JSON.stringify(results);
})()
"""
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{'expression':expr,'returnByValue':True}}))
print(ws.recv())

# Get WebView body and html full info
expr2 = """
JSON.stringify({
  bodyRect: document.body.getBoundingClientRect(),
  htmlRect: document.documentElement.getBoundingClientRect(),
  bodyPaddingTop: getComputedStyle(document.body).paddingTop,
  bodyPaddingBottom: getComputedStyle(document.body).paddingBottom,
  bodyMargin: getComputedStyle(document.body).margin,
  htmlOverflow: getComputedStyle(document.documentElement).overflow
})
"""
ws.send(json.dumps({'id':2,'method':'Runtime.evaluate','params':{'expression':expr2,'returnByValue':True}}))
print(ws.recv())
ws.close()
