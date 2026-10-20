"""Disposable byte-counting upstream and real Nginx upload regression client."""
import http.client
import http.server
import sys
import time

MIB = 1 << 20
LIMIT = 1025 * MIB


class Upstream(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        count = 0
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            while True:
                size = int(self.rfile.readline().split(b";", 1)[0], 16)
                if not size:
                    assert self.rfile.readline() == b"\r\n"
                    break
                remaining = size
                while remaining:
                    data = self.rfile.read(min(remaining, MIB))
                    if not data:
                        raise EOFError("truncated chunk")
                    remaining -= len(data)
                    count += len(data)
                assert self.rfile.read(2) == b"\r\n"
        else:
            remaining = int(self.headers["Content-Length"])
            while remaining:
                data = self.rfile.read(min(remaining, MIB))
                if not data:
                    raise EOFError("truncated body")
                count += len(data)
                remaining -= len(data)
        body = str(count).encode()
        self.send_response(201)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def upload(file_size, chunked=False):
    prefix = (b"--upload-check\r\nContent-Disposition: form-data; "
              b'name="file"; filename="large.bin"\r\n'
              b"Content-Type: application/octet-stream\r\n\r\n")
    suffix = b"\r\n--upload-check--\r\n"
    total = len(prefix) + file_size + len(suffix)

    def parts():
        yield prefix
        block = bytes(MIB)
        remaining = file_size
        while remaining:
            size = min(remaining, len(block))
            yield block[:size]
            remaining -= size
        yield suffix

    headers = {"Content-Type": "multipart/form-data; boundary=upload-check"}
    if not chunked:
        headers["Content-Length"] = str(total)
    conn = http.client.HTTPConnection("web", 5656, timeout=120)
    conn.request("POST", "/api/uploads", parts(), headers, encode_chunked=chunked)
    response = conn.getresponse()
    body = response.read()
    assert response.status == 201, (response.status, body[:200])
    assert int(body) == total, (body, total)
    conn.close()
    print(f"PASS: {file_size} file bytes forwarded ({'chunked' if chunked else 'Content-Length'})", flush=True)


if sys.argv[1] == "serve":
    http.server.ThreadingHTTPServer(("0.0.0.0", 5656), Upstream).serve_forever()
else:
    for attempt in range(50):
        try:
            conn = http.client.HTTPConnection("web", 5656, timeout=2)
            conn.request("GET", "/healthz")
            # The byte-counting stub deliberately has no GET implementation.
            assert conn.getresponse().status == 501
            conn.close()
            break
        except (OSError, AssertionError):
            if attempt == 49:
                raise
            time.sleep(0.2)
    upload(1 << 30)
    upload(106 * MIB, chunked=True)
    conn = http.client.HTTPConnection("web", 5656, timeout=10)
    conn.putrequest("POST", "/api/uploads")
    conn.putheader("Content-Length", str(LIMIT + 1))
    conn.endheaders()
    response = conn.getresponse()
    assert response.status == 413, response.status
    response.read()
    conn.close()
    print("PASS: request above 1025 MiB rejected before body transmission", flush=True)
