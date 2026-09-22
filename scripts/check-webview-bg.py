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

# Check actual pixel colors at different points
expr = """
(function() {
  var canvas = document.createElement('canvas');
  canvas.width = 1;
  canvas.height = 1;
  var ctx = canvas.getContext('2d');
  // Get the computed style of html element
  var htmlStyle = getComputedStyle(document.documentElement);
  return JSON.stringify({
    bodyOverflow: getComputedStyle(document.body).overflow,
    bodyHeight: document.body.offsetHeight,
    appHeight: document.getElementById('app').offsetHeight,
    appBottom: document.getElementById('app').getBoundingClientRect().bottom,
    htmlComputedHeight: htmlStyle.height,
    htmlComputedMinHeight: htmlStyle.minHeight,
    bodyComputedHeight: getComputedStyle(document.body).height,
    bodyComputedMinHeight: getComputedStyle(document.body).minHeight,
    bodyBg: getComputedStyle(document.body).backgroundColor,
    appOverflow: getComputedStyle(document.getElementById('app')).overflow,
    appLayoutBottom: document.querySelector('.app-layout').getBoundingClientRect().bottom,
    loginViewBottom: document.querySelector('.login-view').getBoundingClientRect().bottom,
    allBgsAtBottom: (function() {
      var result = [];
      var y = 800;
      var els = document.elementsFromPoint(100, y);
      for (var i = 0; i < Math.min(5, els.length); i++) {
        result.push({
          tag: els[i].tagName,
          cls: typeof els[i].className === 'string' ? els[i].className.substring(0,40) : '',
          bg: getComputedStyle(els[i]).backgroundColor
        });
      }
      return result;
    })()
  });
})()
"""
ws.send(json.dumps({'id':1,'method':'Runtime.evaluate','params':{'expression':expr,'returnByValue':True}}))
print(ws.recv())
ws.close()
