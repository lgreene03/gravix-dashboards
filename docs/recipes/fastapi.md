# FastAPI

FastAPI is the least work of the six: Starlette already exposes the matched route in `{name}`
form, so `ASGIMiddleware` reads it directly and nothing has to be translated.

`app.add_middleware(ASGIMiddleware, client=client)` is the whole integration.

## The code

`examples/recipes/fastapi/main.py` — this block and that file are byte-identical, and a test fails if they drift.

```python
"""Gravix recipe: FastAPI.

Run:
    pip install fastapi uvicorn gravix
    GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) \
        python main.py
    curl http://localhost:8000/users/1234
"""

import os
import sys

import uvicorn
from fastapi import FastAPI

import gravix
from gravix.middleware import ASGIMiddleware

client = gravix.Client(
    os.environ.get("GRAVIX_ENDPOINT", "http://localhost:8090"),
    os.environ.get("GRAVIX_API_KEY", ""),
    service="my-fastapi-app",
    # One fact per request, so anything you send shows up immediately.
    # Raise this in production to reduce request volume.
    batch_size=1,
)

app = FastAPI()

# Starlette already exposes the matched route in {name} form, so the fact is
# recorded as /users/{user_id} with no translation needed here.
app.add_middleware(ASGIMiddleware, client=client)


@app.get("/users/{user_id}")
def get_user(user_id: int):
    return {"id": user_id}


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8000
    uvicorn.run(app, host="127.0.0.1", port=port, log_level="warning")
```

## Running it

```bash
cd examples/recipes/fastapi
pip install fastapi uvicorn gravix
GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) python main.py
curl http://localhost:8000/users/1234
```

The fact Gravix receives has `path_template: "/users/{user_id}"` — the route as declared.

## See also

- [All recipes](README.md)
- [Getting started](../../README.md)
