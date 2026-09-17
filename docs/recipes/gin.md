# Gin

The Go SDK's `HTTPMiddleware` is written for `net/http` and has to guess at templates from raw
paths. Gin does not need that: `c.FullPath()` returns the route exactly as registered, so this recipe
writes a small `gin.HandlerFunc` that converts Gin's `:id` syntax to Gravix's `{id}` and records the
result. No guessing, no sanitizer.

`c.FullPath()` is empty when no route matched. The middleware reports `/unmatched` rather than an
empty template, so 404s are countable without inventing a route that does not exist.

## The code

`examples/recipes/gin/main.go` — this block and that file are byte-identical, and a test fails if they drift.

```go
// Gravix recipe: Gin
//
// Run:
//
//	go mod tidy
//	GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) go run .
//	curl http://localhost:8080/users/1234
package main

import (
	"context"
	"os"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	gravix "github.com/gravix-io/gravix-go"
)

// ginParamRe matches one Gin route parameter segment: :name or *name.
var ginParamRe = regexp.MustCompile(`[:*]([A-Za-z0-9_]+)`)

// ginPathToTemplate converts Gin's route syntax to Gravix's {name} syntax.
// ginPathToTemplate("/users/:id") == "/users/{id}"
func ginPathToTemplate(fullPath string) string {
	return ginParamRe.ReplaceAllString(fullPath, "{$1}")
}

// gravixGinMiddleware records one fact per request.
//
// It uses c.FullPath(), which is the route as registered rather than the URL
// as requested, so the template is exact and no sanitizer has to guess.
func gravixGinMiddleware(client *gravix.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		template := ginPathToTemplate(c.FullPath())
		if template == "" {
			// No route matched: report the 404 without inventing a template.
			template = "/unmatched"
		}

		_ = client.RecordFact(context.Background(), gravix.RequestFact{
			Method:       c.Request.Method,
			PathTemplate: template,
			StatusCode:   c.Writer.Status(),
			LatencyMs:    int(time.Since(start).Milliseconds()),
		})
	}
}

func newGinExampleRouter(client *gravix.Client) *gin.Engine {
	router := gin.New()
	router.Use(gravixGinMiddleware(client))
	router.GET("/users/:id", func(c *gin.Context) {
		c.JSON(200, gin.H{"id": c.Param("id")})
	})
	return router
}

func main() {
	endpoint := os.Getenv("GRAVIX_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8090"
	}

	client := gravix.New(endpoint, os.Getenv("GRAVIX_API_KEY"),
		gravix.WithService("my-gin-app"),
		// One fact per request, so anything you send shows up immediately.
		// Raise this in production to reduce request volume.
		gravix.WithBatchSize(1),
	)
	defer client.Close()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	_ = newGinExampleRouter(client).Run("127.0.0.1:" + port)
}
```

## Running it

```bash
cd examples/recipes/gin
go mod tidy
GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) go run .
curl http://localhost:8080/users/1234
```

The fact Gravix receives has `path_template: "/users/{id}"`.

This example is its own Go module so that Gin never enters the main module's dependency graph.

## See also

- [All recipes](README.md)
- [Getting started](../../README.md)
