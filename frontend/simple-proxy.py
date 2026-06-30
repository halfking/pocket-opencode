#!/usr/bin/env python3
from http.server import HTTPServer, BaseHTTPRequestHandler
import urllib.request
import json

class ProxyHandler(BaseHTTPRequestHandler):
    TARGET = 'http://14.103.169.56:8088'
    
    def do_GET(self):
        try:
            url = self.TARGET + self.path
            print(f"GET {self.path} -> {url}")
            
            req = urllib.request.Request(url)
            with urllib.request.urlopen(req, timeout=10) as response:
                data = response.read()
                
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Access-Control-Allow-Origin', '*')
                self.end_headers()
                self.wfile.write(data)
                
        except Exception as e:
            print(f"Error: {e}")
            self.send_error(502, f'Proxy Error: {e}')
    
    def do_POST(self):
        try:
            content_length = int(self.headers.get('Content-Length', 0))
            body = self.rfile.read(content_length) if content_length > 0 else b''
            
            url = self.TARGET + self.path
            print(f"POST {self.path} -> {url}")
            
            req = urllib.request.Request(url, data=body, method='POST')
            req.add_header('Content-Type', 'application/json')
            
            with urllib.request.urlopen(req, timeout=10) as response:
                data = response.read()
                
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Access-Control-Allow-Origin', '*')
                self.end_headers()
                self.wfile.write(data)
                
        except Exception as e:
            print(f"Error: {e}")
            self.send_error(502, f'Proxy Error: {e}')
    
    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type, Authorization')
        self.end_headers()
    
    def log_message(self, format, *args):
        print(f"{self.address_string()} - {format % args}")

if __name__ == '__main__':
    server = HTTPServer(('0.0.0.0', 8088), ProxyHandler)
    print('✅ Python 代理服务器运行在: http://192.168.31.41:8088')
    print('   转发到: http://14.103.169.56:8088')
    print('\n等待连接...\n')
    server.serve_forever()
