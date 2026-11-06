package rss

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// The DB is never touched on the guarded paths, so nil is safe here and
// pins that no query is issued for rejected input.
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/rss/", HandleRSS(nil, zap.NewNop()))
	return r
}

func doGet(t *testing.T, r *gin.Engine, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// NUL bytes in the component filter must be rejected with a generic 400,
// never forwarded to the database (see issue #63).
func TestHandleRSSRejectsNulInComponent(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?mt=Global&srv=%00")

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "SQLSTATE")
	assert.NotContains(t, w.Body.String(), "UTF8")
}

func TestHandleRSSRejectsInvalidUTF8InComponent(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?mt=Global&srv=%FF%FE")

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "SQLSTATE")
	assert.NotContains(t, w.Body.String(), "UTF8")
}

// Pre-existing behavior must stay: srv without mt -> 404 with the static
// documentation hint.
func TestHandleRSSComponentWithoutRegion(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?srv=ECS")

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "errMsg")
}
