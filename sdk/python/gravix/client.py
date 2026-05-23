"""Gravix Python client with batching and retry."""

import json
import logging
import threading
import time
import uuid
from datetime import datetime, timezone
from typing import Callable, List, Optional

import urllib3

from gravix.sanitize import sanitize_path
from gravix.types import RequestFact, ServiceEvent

logger = logging.getLogger("gravix")


class Client:
    """Sends request facts and service events to the Gravix ingestion API.

    Args:
        base_url: Ingestion service URL (e.g., "http://localhost:8090").
        api_key: X-API-Key value for authentication.
        service: Default service name applied to all facts/events.
        batch_size: Max facts buffered before auto-flush. Default 100.
        flush_interval: Seconds between auto-flushes. Default 5.0.
        max_retries: Max retry attempts for failed requests. Default 3.
        auto_sanitize: Auto-sanitize path templates. Default True.
        on_error: Callback for background flush errors.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        service: str = "",
        batch_size: int = 100,
        flush_interval: float = 5.0,
        max_retries: int = 3,
        auto_sanitize: bool = True,
        on_error: Optional[Callable[[Exception], None]] = None,
    ):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.service = service
        self.batch_size = batch_size
        self.flush_interval = flush_interval
        self.max_retries = max_retries
        self.auto_sanitize = auto_sanitize
        self.on_error = on_error

        self._http = urllib3.PoolManager(num_pools=2, maxsize=4, retries=False)
        self._buffer: List[dict] = []
        self._lock = threading.Lock()
        self._closed = False
        self._timer: Optional[threading.Timer] = None
        self._start_timer()

    def record_fact(self, fact: RequestFact) -> None:
        """Queue a request fact for batched submission."""
        self._enrich_fact(fact)
        d = fact.to_dict()
        with self._lock:
            self._buffer.append(d)
            if len(self._buffer) >= self.batch_size:
                self._flush_locked()

    def record_event(self, event: ServiceEvent) -> None:
        """Send a service event synchronously."""
        self._enrich_event(event)
        body = json.dumps(event.to_dict()).encode()
        self._do_request("POST", "/api/v1/events", body)

    def send_fact(self, fact: RequestFact) -> None:
        """Send a single fact synchronously (bypassing batcher)."""
        self._enrich_fact(fact)
        body = json.dumps(fact.to_dict()).encode()
        self._do_request("POST", "/api/v1/facts", body)

    def flush(self) -> None:
        """Force flush of all buffered facts."""
        with self._lock:
            self._flush_locked()

    def close(self) -> None:
        """Flush remaining facts and shut down."""
        self._closed = True
        if self._timer:
            self._timer.cancel()
        self.flush()

    def _enrich_fact(self, fact: RequestFact) -> None:
        if not fact.event_id:
            fact.event_id = str(uuid.uuid4())
        if not fact.event_time:
            fact.event_time = datetime.now(timezone.utc).isoformat()
        if not fact.service and self.service:
            fact.service = self.service
        if self.auto_sanitize and fact.path_template:
            fact.path_template = sanitize_path(fact.path_template)

    def _enrich_event(self, event: ServiceEvent) -> None:
        if not event.event_id:
            event.event_id = str(uuid.uuid4())
        if not event.event_time:
            event.event_time = datetime.now(timezone.utc).isoformat()
        if not event.service and self.service:
            event.service = self.service

    def _flush_locked(self) -> None:
        """Flush buffer while holding lock."""
        if not self._buffer:
            return
        batch = self._buffer[:]
        self._buffer.clear()

        # Build JSONL payload
        lines = [json.dumps(d) for d in batch]
        body = ("\n".join(lines) + "\n").encode()

        try:
            self._do_request("POST", "/api/v1/facts/batch", body)
        except Exception as e:
            logger.error("gravix: flush error: %s", e)
            if self.on_error:
                self.on_error(e)

    def _do_request(self, method: str, path: str, body: bytes) -> None:
        """Execute HTTP request with retries."""
        url = self.base_url + path
        last_err = None

        for attempt in range(self.max_retries + 1):
            try:
                resp = self._http.request(
                    method,
                    url,
                    body=body,
                    headers={
                        "Content-Type": "application/json",
                        "X-API-Key": self.api_key,
                    },
                    timeout=10.0,
                )
                if resp.status in (200, 201):
                    return
                if resp.status in (429, 500, 502, 503, 504) and attempt < self.max_retries:
                    delay = min(0.5 * (2 ** attempt), 10.0)
                    time.sleep(delay)
                    continue
                raise Exception(f"HTTP {resp.status}: {resp.data.decode()[:200]}")
            except urllib3.exceptions.HTTPError as e:
                last_err = e
                if attempt < self.max_retries:
                    delay = min(0.5 * (2 ** attempt), 10.0)
                    time.sleep(delay)
                    continue
                raise

        if last_err:
            raise last_err

    def _start_timer(self) -> None:
        """Start periodic flush timer."""
        if self._closed:
            return
        self._timer = threading.Timer(self.flush_interval, self._timer_flush)
        self._timer.daemon = True
        self._timer.start()

    def _timer_flush(self) -> None:
        """Timer callback: flush and restart."""
        self.flush()
        self._start_timer()
