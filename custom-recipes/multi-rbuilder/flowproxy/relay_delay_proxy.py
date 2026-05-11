#!/usr/bin/env python3
import http.client
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


UPSTREAM = urlparse(os.environ.get("UPSTREAM", "http://mev-boost-relay:5555"))
LATENCY_SECONDS = int(os.environ.get("LATENCY_MS", "50")) / 1000
LISTEN_HOST = os.environ.get("LISTEN_HOST", "0.0.0.0")
LISTEN_PORT = int(os.environ.get("LISTEN_PORT", "5555"))


class RelayDelayProxy(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        self._proxy()

    def do_POST(self):
        self._proxy()

    def do_PUT(self):
        self._proxy()

    def do_DELETE(self):
        self._proxy()

    def log_message(self, fmt, *args):
        print("%s - %s" % (self.address_string(), fmt % args), flush=True)

    def _proxy(self):
        if self.path == "/health":
            self._send(200, b"ok", {"content-type": "text/plain"})
            return

        time.sleep(LATENCY_SECONDS)

        body_len = int(self.headers.get("content-length", "0") or "0")
        body = self.rfile.read(body_len) if body_len else None
        headers = {
            k: v
            for k, v in self.headers.items()
            if k.lower() not in {"host", "connection", "proxy-connection", "content-length"}
        }
        if body is not None:
            headers["content-length"] = str(len(body))
        headers["host"] = UPSTREAM.netloc

        conn = http.client.HTTPConnection(UPSTREAM.hostname, UPSTREAM.port or 80, timeout=30)
        try:
            path = self.path
            conn.request(self.command, path, body=body, headers=headers)
            upstream_response = conn.getresponse()
            response_body = upstream_response.read()
            response_headers = {
                k: v
                for k, v in upstream_response.getheaders()
                if k.lower() not in {"connection", "transfer-encoding", "content-length"}
            }
            self._send(upstream_response.status, response_body, response_headers)
        except Exception as exc:
            self._send(502, str(exc).encode(), {"content-type": "text/plain"})
        finally:
            conn.close()

    def _send(self, status, body, headers):
        self.send_response(status)
        for key, value in headers.items():
            self.send_header(key, value)
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    print(
        f"relay-delay-proxy listening on {LISTEN_HOST}:{LISTEN_PORT}, "
        f"upstream={UPSTREAM.geturl()}, latency_ms={LATENCY_SECONDS * 1000:.0f}",
        flush=True,
    )
    ThreadingHTTPServer((LISTEN_HOST, LISTEN_PORT), RelayDelayProxy).serve_forever()
