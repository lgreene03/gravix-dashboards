# Framework recipes

One runnable example per framework, each showing the smallest integration that produces correct
`path_template` values.

| Framework | Language | SDK | Recipe |
|---|---|---|---|
| [Express](express.md) | JavaScript | `@gravix/sdk` | Built-in middleware |
| [FastAPI](fastapi.md) | Python | `gravix` | Built-in ASGI middleware |
| [Flask](flask.md) | Python | `gravix` | Built-in middleware + a `path_fn` |
| [Django](django.md) | Python | `gravix` | Middleware written in the recipe |
| [Gin](gin.md) | Go | `gravix-go` | Handler written in the recipe |
| [Rails](rails.md) | Ruby | **none** | Plain `Net::HTTP` |

## The thing every recipe is really about

All six do the same small job: report the **route**, not the URL. `/users/{id}`, never `/users/1234`.

That is the difference between a dimension with one value and a dimension with one value per user,
and it is the constraint the whole cost model rests on. Each framework makes it easy or hard in its
own way:

- **FastAPI** hands you the route already in `{name}` form. Nothing to do.
- **Gin** hands you the route in `:name` form. One regex.
- **Express** hands you the raw URL, and the SDK sanitizes segments of four or more digits. Shorter
  ids are not rewritten — if yours are, translate explicitly.
- **Flask and Django** hand you `<int:user_id>`, which no sanitizer touches. Both recipes include
  the translation; without it your templates are framework-shaped and cross-service comparison goes
  quietly wrong.
- **Rails** hands you nothing, because there is no gem. You do all of it, including generating a
  UUIDv7.

## Every example is executed by CI

The code block in each recipe is byte-identical to its file under `examples/recipes/`, enforced by a
test. Five of the six are then run for real against a stub ingestion server, and the recorded
`path_template` is asserted — so a recipe cannot document one thing and do another.

Rails is the exception: it is verified statically, because adding a Ruby toolchain to CI for one
example was judged out of proportion. See `docs/oss/specs/GRVX-909-framework-recipes.md` §3.
