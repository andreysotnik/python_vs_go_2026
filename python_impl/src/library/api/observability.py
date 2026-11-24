"""Request logging + Prometheus metrics as a pure ASGI middleware (cheap, no BaseHTTPMiddleware)."""

from __future__ import annotations

import logging
import os
import time
import uuid

from prometheus_client import CollectorRegistry, Counter, Histogram, generate_latest, multiprocess
from prometheus_client.registry import REGISTRY
from starlette.types import ASGIApp, Message, Receive, Scope, Send

log = logging.getLogger("library.access")

REQUESTS = Counter("http_requests_total", "HTTP requests", ["method", "route", "status"])
LATENCY = Histogram(
    "http_request_duration_seconds",
    "HTTP request latency",
    ["method", "route"],
    buckets=(0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
)


def render_metrics() -> bytes:
    if os.environ.get("PROMETHEUS_MULTIPROC_DIR"):
        registry = CollectorRegistry()
        multiprocess.MultiProcessCollector(registry)  # type: ignore[no-untyped-call]
        return generate_latest(registry)
    return generate_latest(REGISTRY)


class ObservabilityMiddleware:
    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        start = time.perf_counter()
        status = 500
        headers = dict(scope["headers"])
        request_id = headers.get(b"x-request-id", b"").decode("latin-1") or uuid.uuid4().hex

        async def send_wrapper(message: Message) -> None:
            nonlocal status
            if message["type"] == "http.response.start":
                status = message["status"]
                message.setdefault("headers", []).append((b"x-request-id", request_id.encode("latin-1")))
            await send(message)

        try:
            await self.app(scope, receive, send_wrapper)
        finally:
            elapsed = time.perf_counter() - start
            route_obj = scope.get("route")
            route = getattr(route_obj, "path", None) or "<unmatched>"
            method = scope["method"]
            REQUESTS.labels(method, route, str(status)).inc()
            LATENCY.labels(method, route).observe(elapsed)
            if route not in ("/healthz", "/metrics"):
                log.info(
                    "request",
                    extra={
                        "request_id": request_id,
                        "method": method,
                        "path": scope["path"],
                        "route": route,
                        "status": status,
                        "duration_ms": round(elapsed * 1000, 2),
                        "user_id": headers.get(b"x-user-id", b"").decode("latin-1") or None,
                    },
                )
