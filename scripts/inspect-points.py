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

# Check various points and check window.devicePixelRatio
expr = """
(function() {
  var points = [
    {x: 100, y: 100, label: 'p1'},
    {x: 100, y: 200, label: 'p2'},
    {x: 100, y: 400, label: 'p3'},
    {x: 100, y: 600, label: 'p4'},
    {x: 100, y: 760, label: 'p5'},
    {x: 100, y: 770, label: 'p6'},
    {x: 100, y: 780, label: 'p7'}
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
      cls: el ? (typeof el.className === 'string' ? el.className.substring(0,60) : null) : null,
      id: el ? el.id : null
    });
  }
  return JSON.stringify({
    dpr: window.devicePixelRatio,
    innerWidth: window.innerWidth,
    innerHeight: window.innerHeight,
    points: results
  });
})()
"""
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{'expression':expr,'returnByValue':True}}))
print(ws.recv())
ws.close()
