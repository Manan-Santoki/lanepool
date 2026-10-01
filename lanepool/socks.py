"""Just enough SOCKS5 to fetch a plain-HTTP URL through a lane (used for exit-IP checks)."""

from __future__ import annotations

import socket
import struct
import time
import urllib.parse


class SocksError(OSError):
    pass


def _recv_exact(sock: socket.socket, n: int) -> bytes:
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise SocksError("connection closed by proxy")
        buf += chunk
    return buf


def _handshake(sock: socket.socket, host: str, port: int, user: str, password: str) -> None:
    methods = b"\x02" if user else b"\x00"
    sock.sendall(b"\x05\x01" + methods)
    version, method = _recv_exact(sock, 2)
    if version != 5 or method == 0xFF:
        raise SocksError("proxy rejected authentication methods")
    if method == 0x02:
        u, p = user.encode(), password.encode()
        sock.sendall(b"\x01" + bytes([len(u)]) + u + bytes([len(p)]) + p)
        if _recv_exact(sock, 2)[1] != 0:
            raise SocksError("proxy authentication failed")

    h = host.encode("idna")
    sock.sendall(b"\x05\x01\x00\x03" + bytes([len(h)]) + h + struct.pack(">H", port))
    reply = _recv_exact(sock, 4)
    if reply[1] != 0:
        raise SocksError(f"proxy CONNECT failed with code {reply[1]}")
    atyp = reply[3]
    if atyp == 1:
        _recv_exact(sock, 4 + 2)
    elif atyp == 4:
        _recv_exact(sock, 16 + 2)
    elif atyp == 3:
        _recv_exact(sock, _recv_exact(sock, 1)[0] + 2)
    else:
        raise SocksError("bad address type in proxy reply")


def http_get_via_socks(
    proxy_host: str,
    proxy_port: int,
    url: str,
    user: str = "",
    password: str = "",
    timeout: float = 15,
) -> tuple[str, float]:
    """GET a plain http:// URL through a SOCKS5 proxy. Returns (body, seconds)."""
    parts = urllib.parse.urlsplit(url)
    if parts.scheme != "http" or not parts.hostname:
        raise ValueError("only http:// URLs are supported")
    port = parts.port or 80
    path = parts.path or "/"
    if parts.query:
        path += "?" + parts.query

    started = time.monotonic()
    with socket.create_connection((proxy_host, proxy_port), timeout=timeout) as sock:
        sock.settimeout(timeout)
        _handshake(sock, parts.hostname, port, user, password)
        sock.sendall(
            f"GET {path} HTTP/1.1\r\nHost: {parts.hostname}\r\n"
            "User-Agent: lanepool\r\nConnection: close\r\n\r\n".encode()
        )
        data = b""
        while len(data) < 65536:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data += chunk
    elapsed = time.monotonic() - started

    head, _, body = data.partition(b"\r\n\r\n")
    status_line = head.split(b"\r\n", 1)[0].decode(errors="replace")
    status = status_line.split(" ")
    if len(status) < 2 or status[1] != "200":
        raise SocksError(f"unexpected response: {status_line!r}")
    if b"transfer-encoding: chunked" in head.lower():
        body = _dechunk(body)
    return body.decode(errors="replace").strip(), elapsed


def _dechunk(body: bytes) -> bytes:
    out = b""
    while body:
        size_line, _, rest = body.partition(b"\r\n")
        size = int(size_line.split(b";")[0] or b"0", 16)
        if size == 0:
            break
        out += rest[:size]
        body = rest[size + 2 :]
    return out
