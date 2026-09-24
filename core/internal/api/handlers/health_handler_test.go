package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLivezNeverTouchesDependencies(t *testing.T) {
	h := NewHealthHandler(nil, nil, logger.New("error")) // no DB at all
	w := httptest.NewRecorder()
	h.Livez(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("livez = %d, want 200", w.Code)
	}
}

func TestReadyz(t *testing.T) {
	dsn := os.Getenv("ROTA_TEST_DSN")
	if dsn == "" {
		t.Skip("ROTA_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHealthHandler(&database.DB{Pool: pool}, nil, logger.New("error"))

	w := httptest.NewRecorder()
	h.Readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ready"`) {
		t.Fatalf("readyz with DB = %d %s", w.Code, w.Body.String())
	}

	pool.Close() // DB gone → not ready
	w = httptest.NewRecorder()
	h.Readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz without DB = %d, want 503", w.Code)
	}
}
