# Flask

Flask reports its routes as `/users/<int:user_id>`, and **nothing translates that to Gravix's
`{user_id}` form for you**. The SDK's sanitizer only rewrites bare numeric and UUID segments, and
`<int:user_id>` is neither.

That is why `flask_middleware` takes a `path_fn`. The recipe supplies a six-line one. Without it your
dashboard fills with Flask-shaped templates that no other language's recipe produces, which makes
cross-service comparison quietly wrong rather than loudly broken.

## The code

`examples/recipes/flask/app.py` — this block and that file are byte-identical, and a test fails if they drift.

```python
"""Gravix recipe: Flask.

Run:
    pip install flask gravix
    GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) \
        python app.py
    curl http://localhost:5000/users/1234
"""

import os
import re
import sys

from flask import Flask

import gravix
from gravix.middleware import flask_middleware

# Flask reports routes as "/users/<int:user_id>". Gravix wants "/users/{user_id}",
# and nothing translates between the two for you: the SDK's sanitizer only
# rewrites bare numeric and UUID segments, which "<int:user_id>" is not.
_PARAM_RE = re.compile(r"<(?:[a-zA-Z_][a-zA-Z0-9_]*:)?([a-zA-Z_][a-zA-Z0-9_]*)>")


def gravix_path_fn(request):
    return _PARAM_RE.sub(r"{\1}", request.url_rule.rule)


client = gravix.Client(
    os.environ.get("GRAVIX_ENDPOINT", "http://localhost:8090"),
    os.environ.get("GRAVIX_API_KEY", ""),
    service="my-flask-app",
    # One fact per request, so anything you send shows up immediately.
    # Raise this in production to reduce request volume.
    batch_size=1,
)

app = Flask(__name__)
flask_middleware(client, path_fn=gravix_path_fn)(app)


@app.route("/users/<int:user_id>")
def get_user(user_id):
    return {"id": user_id}


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 5000
    app.run(host="127.0.0.1", port=port)
```

## Running it

```bash
cd examples/recipes/flask
pip install flask gravix
GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) python app.py
curl http://localhost:5000/users/1234
```

The fact Gravix receives has `path_template: "/users/{user_id}"`.

## See also

- [All recipes](README.md)
- [Getting started](../../README.md)
