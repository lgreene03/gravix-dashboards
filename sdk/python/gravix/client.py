"""Gravix client with automatic batching, retry, and path sanitization."""

from __future__ import annotations

import json
import threading
import time
import random
from typing import Callable, List, Optional

import requests

from gravix.sanitize import sanitize_path
from gravix.types import (
    APIError,
    BatchResult,
    RequestFact,
    ServiceEvent,
    _generate_event_id,
    _now_iso,
)

# Status codes that are safe to retry.
_RETRYABLE = {429, 500, 502, 503, 504}


class GravixClient:
    """Client for the Gravix ingestion API.

    Facts are buffered and flushed in batches. Events are sent synchronously.

    Args:
        base_url: Ingestion service URL (e.g. ``http://localhost:8090``).
        api_key: The ``X-API-Key`` value.
        service: Default service name applied to facts/events when not set.
        batch_size: Max facts buffered before auto-flush (default 100).
        flush_interval: Seconds between auto-flushes (default 5.0).
        auto_sanitize: Auto-replace UUIDs/IDs in paths (default True).
        max_retries: Max retry attempts on failure (default 3).
        timeout: HTTP request timeout in seconds (default 10).
        on_error: Callback invoked when a background flush fails.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        service: str = "",
        batch_size: int = 100,
        flush_interval: float = 5.0,
        auto_sanitize: bool = True,
        max_retries: int = 3,
        timeout: float = 10.0,
        on_error: Optional[Callable[[Exception], None]] = None,
    ):
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._service = service
        self._batch_size = batch_size
        self._flush_interval = flush_interval
        self._auto_sanitize = auto_sanitize
        self._max_retries = max_retries
        self._timeout = timeout
        self._on_error = on_error

        self._session = requests.Session()
        self._session.headers.update(
            {
                "Content-Type": "application/json",
                "X-API-Key": api_key,
            }
        )

        self._buffer: List[dict] = []
        self._lock = threading.Lock()
        self._closed = False
        self._stop_event = threading.Event()
        self._flush_thread = threading.Thread(
            target=self._flush_loop, daemon=True, name="gravix-flush"
        )
        self._flush_thread.start()

    # --- Public API ---

    def record_fact(self, fact: RequestFact) -> None:
        """Queue a request fact for batched submission.

        Auto-generates ``event_id`` and ``event_time`` if empty.
        Applies path sanitization if ``auto_sanitize`` is enabled.
        """
        self._enrich_fact(fact)
        d = fact.to_dict()

        need_flush = False
        with self._lock:
            self._buffer.append(d)
            if len(self._buffer) >= self._batch_size:
                need_flush = True

        if need_flush:
            self.flush()

    def record_event(self, event: ServiceEvent) -> None:
        """Send a service event synchronously.

        Raises :class:`APIError` on failure.
        """
        self._enrich_event(event)
        body = json.dumps(event.to_dict())
        self._do_with_retry("POST", "/api/v1/events", body)

    def send_fact(self, fact: RequestFact) -> None:
        """Send a single fact synchronously (bypasses the batcher).

        Raises :class:`APIError` on failure.
        """
        self._enrich_fact(fact)
        body = json.dumps(fact.to_dict())
        self._do_with_retry("POST", "/api/v1/facts", body)

    def flush(self) -> None:
        """Force an immediate flush of all buffered facts."""
        with self._lock:
            if not self._buffer:
                return
            batch = self._buffer
            self._buffer = []

        # Build JSONL payload.
        lines = [json.dumps(d) for d in batch]
        body = "\n".join(lines) + "\n"

        try:
            self._do_with_retry("POST", "/api/v1/facts/batch", body)
        except Exception as exc:
            if self._on_error:
                self._on_error(exc)
            else:
                raise

    def close(self) -> None:
        """Flush remaining facts and shut down the background thread."""
        if self._closed:
            return
        self._closed = True
        self._stop_event.set()
        self._flush_thread.join(timeout=10)
        try:
            self.flush()
        except Exception:
            pass
        self._session.close()

    # --- Context manager ---

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()

    # --- Internals ---

    def _enrich_fact(self, fact: RequestFact) -> None:
        if not fact.event_id:
            fact.event_id = _generate_event_id()
        if not fact.event_time:
            fact.event_time = _now_iso()
        if not fact.service and self._service:
            fact.service = self._service
        if self._auto_sanitize and fact.path_template:
            fact.path_template = sanitize_path(fact.path_template)

    def _enrich_event(self, event: ServiceEvent) -> None:
        if not event.event_id:
            event.event_id = _generate_event_id()
        if not event.event_time:
            event.event_time = _now_iso()
        if not event.service and self._service:
            event.service = self._service

    def _do_with_retry(self, method: str, path: str, body: str) -> requests.Response:
        url = self._base_url + path
        last_exc: Optional[Exception] = None

        for attempt in range(self._max_retries + 1):
            if attempt > 0 and last_exc is not None:
                delay = self._retry_delay(attempt - 1)
                time.sleep(delay)

            try:
                resp = self._session.request(
                    method,
                    url,
                    data=body.encode("utf-8"),
                    timeout=self._timeout,
                )
            except requests.RequestException as exc:
                last_exc = exc
                continue

            if resp.status_code in (200, 201):
                return resp

            # Parse error.
            try:
                err_data = resp.json()
                msg = err_data.get("error", f"HTTP {resp.status_code}")
            except (ValueError, KeyError):
                msg = f"HTTP {resp.status_code}"

            api_err = APIError(msg, resp.status_code)

            if resp.status_code in _RETRYABLE and attempt < self._max_retries:
                last_exc = api_err
                # Respect Retry-After header.
                retry_after = resp.headers.get("Retry-After")
                if retry_after:
                    try:
                        time.sleep(int(retry_after))
                        continue
                    except ValueError:
                        pass
                continue

            raise api_err

        if last_exc is not None:
            raise last_exc
        raise APIError("max retries exceeded", 0)

    @staticmethod
    def _retry_delay(attempt: int) -> float:
        """Exponential backoff with full jitter."""
        base = 0.5 * (2 ** attempt)
        cap = min(base, 10.0)
        return random.uniform(0, cap)

    def _flush_loop(self) -> None:
        while not self._stop_event.wait(timeout=self._flush_interval):
            try:
                self.flush()
            except Exception:
                pass
