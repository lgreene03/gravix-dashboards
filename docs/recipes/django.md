# Django

There is no Django middleware in the Python SDK, so this recipe writes one — about twenty lines
against `client.record_fact`.

Two details are easy to get wrong. `request.resolver_match` is only populated *after* the URL has
been resolved, so the fact must be recorded after `get_response(request)`, not before. And Django's
route strings (`users/<int:user_id>/`) need the same translation Flask's do, for the same reason.

The example is a single file using `settings.configure()` so the instrumentation is visible without
reading a scaffold. In a real project `GravixMiddleware` lives in its own module and its dotted path
goes in `MIDDLEWARE`.

## The code

`examples/recipes/django/app.py` — this block and that file are byte-identical, and a test fails if they drift.

```python
"""Gravix recipe: Django.

A single file rather than a startproject scaffold, so the instrumentation is
visible without reading five other files. In a real project, GravixMiddleware
goes in its own module and its path goes in MIDDLEWARE.

Run:
    pip install django gravix
    GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) \
        python app.py
    curl http://localhost:8001/users/1234/
"""

import os
import re
import sys
import time
from wsgiref.simple_server import make_server

import django
from django.conf import settings
from django.http import JsonResponse
from django.urls import path

import gravix
from gravix.types import RequestFact

# Django reports routes as "users/<int:user_id>/". Gravix wants
# "/users/{user_id}", and nothing translates between the two for you.
_PARAM_RE = re.compile(r"<(?:[a-zA-Z_][a-zA-Z0-9_]*:)?([a-zA-Z_][a-zA-Z0-9_]*)>")

client = gravix.Client(
    os.environ.get("GRAVIX_ENDPOINT", "http://localhost:8090"),
    os.environ.get("GRAVIX_API_KEY", ""),
    service="my-django-app",
    # One fact per request, so anything you send shows up immediately.
    # Raise this in production to reduce request volume.
    batch_size=1,
)


class GravixMiddleware:
    """Records one fact per request, after the view has produced a response."""

    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        start = time.monotonic()
        response = self.get_response(request)

        # resolver_match is only populated once the URL has been resolved, so
        # this must run after get_response, not before it.
        match = getattr(request, "resolver_match", None)
        route = match.route if match else request.path.lstrip("/")
        template = "/" + _PARAM_RE.sub(r"{\1}", route).strip("/")

        client.record_fact(
            RequestFact(
                service="my-django-app",
                method=request.method,
                path_template=template,
                status_code=response.status_code,
                latency_ms=int((time.monotonic() - start) * 1000),
            )
        )
        return response


def get_user(request, user_id):
    return JsonResponse({"id": user_id})


urlpatterns = [path("users/<int:user_id>/", get_user)]

settings.configure(
    DEBUG=False,
    SECRET_KEY="gravix-recipe-not-a-real-secret",
    ROOT_URLCONF=__name__,
    ALLOWED_HOSTS=["*"],
    MIDDLEWARE=[__name__ + ".GravixMiddleware"],
)
django.setup()

if __name__ == "__main__":
    from django.core.wsgi import get_wsgi_application

    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8001
    make_server("127.0.0.1", port, get_wsgi_application()).serve_forever()
```

## Running it

```bash
cd examples/recipes/django
pip install django gravix
GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) python app.py
curl http://localhost:8001/users/1234/
```

The fact Gravix receives has `path_template: "/users/{user_id}"`.

## See also

- [All recipes](README.md)
- [Getting started](../../README.md)
