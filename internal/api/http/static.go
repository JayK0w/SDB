package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// spaHandler serves the embedded frontend. Unknown non-API paths fall
// back to index.html so client-side routes survive a full page reload;
// unknown /api/ paths keep returning a JSON 404.
func (s *Server) spaHandler(root fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(root))
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(root, path); err != nil {
				c.Request.URL.Path = "/" // SPA fallback
			}
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
