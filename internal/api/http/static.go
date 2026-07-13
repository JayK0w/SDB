package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// spaHandler : sert le frontend embarqué. Route inconnue hors /api/ →
// index.html (les routes côté client survivent au rechargement) ;
// /api/ inconnu → 404 JSON.
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
				c.Request.URL.Path = "/" // fallback SPA
			}
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
