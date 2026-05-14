#!/usr/bin/env python3
"""
OpenClaw 프록시: 빈 content 메시지 필터링 후 실제 OpenClaw로 전달.

voicechat-server의 FilterMessages가 이미 빈 content를 거르지만, defense-in-depth로
프록시에서도 한 번 더 필터. /v1/chat/completions의 SSE 응답은 라인 단위로 즉시 forward
(`resp.readline()` + `wfile.flush()`).

설치 위치: /opt/voicechat/oc_proxy.py
systemd unit: oc-proxy.service (listen 127.0.0.1:18790 → upstream localhost:18789)
"""
import json
import http.server
import urllib.request
import urllib.error

REAL_OPENCLAW_URL = "http://localhost:18789"
PROXY_PORT = 18790


class ProxyHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass  # systemd journal 노이즈 최소화

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")
        self.end_headers()

    def proxy_request(self):
        body = None
        content_length = int(self.headers.get("Content-Length", 0))
        if content_length > 0:
            body = self.rfile.read(content_length)

        if self.path == "/v1/chat/completions" and body:
            try:
                payload = json.loads(body)
                if "messages" in payload:
                    original_count = len(payload["messages"])
                    payload["messages"] = [
                        m for m in payload["messages"]
                        if m.get("content", "").strip() != ""
                    ]
                    filtered = original_count - len(payload["messages"])
                    if filtered > 0:
                        print(f"[Proxy] Filtered {filtered} empty-content message(s)")
                body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            except Exception as e:
                print(f"[Proxy] Parse error: {e}")

        headers = {}
        for k, v in self.headers.items():
            if k.lower() not in ("host", "content-length", "transfer-encoding"):
                headers[k] = v
        if body:
            headers["Content-Length"] = str(len(body))

        url = REAL_OPENCLAW_URL + self.path
        req = urllib.request.Request(url, data=body, headers=headers, method=self.command)

        try:
            resp = urllib.request.urlopen(req, timeout=120)
            self.send_response(resp.status)
            for k, v in resp.headers.items():
                if k.lower() not in ("transfer-encoding",):
                    self.send_header(k, v)
            self.end_headers()
            # SSE 라인 단위 즉시 forward (스트리밍 응답이 클라이언트까지 실시간 전달)
            while True:
                line = resp.readline()
                if not line:
                    break
                self.wfile.write(line)
                self.wfile.flush()
        except urllib.error.HTTPError as e:
            self.send_response(e.code)
            for k, v in e.headers.items():
                if k.lower() not in ("transfer-encoding",):
                    self.send_header(k, v)
            self.end_headers()
            self.wfile.write(e.read())
        except Exception as e:
            print(f"[Proxy] Request error: {e}")
            self.send_response(502)
            self.end_headers()
            self.wfile.write(b"Proxy error")

    def do_GET(self): self.proxy_request()
    def do_POST(self): self.proxy_request()
    def do_PUT(self): self.proxy_request()
    def do_PATCH(self): self.proxy_request()
    def do_DELETE(self): self.proxy_request()


if __name__ == "__main__":
    server = http.server.HTTPServer(("127.0.0.1", PROXY_PORT), ProxyHandler)
    print(f"[Proxy] Listening on port {PROXY_PORT} → {REAL_OPENCLAW_URL}")
    server.serve_forever()
