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
