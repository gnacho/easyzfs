package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head><meta charset=\"utf-8\"></head><body><div id=root></div></body></html>")},
		"sw.js":      &fstest.MapFile{Data: []byte("self.addEventListener('fetch', ()=>{})")},
	}
}

// TestGCIndexOverride (#103): el tracker GoatCounter solo se inyecta en la
// demo (DEMO=1) con GC_ORIGIN definido; una instalación normal sirve el index
// intacto.
func TestGCIndexOverride(t *testing.T) {
	origin := "https://stats.example"

	t.Run("sin GC_ORIGIN no inyecta", func(t *testing.T) {
		t.Setenv("GC_ORIGIN", "")
		if got := gcIndexOverride(true, testFS()); got != nil {
			t.Errorf("inyectó sin GC_ORIGIN: %q", got)
		}
	})

	t.Run("sin demo no inyecta aunque haya origen", func(t *testing.T) {
		t.Setenv("GC_ORIGIN", origin)
		if got := gcIndexOverride(false, testFS()); got != nil {
			t.Errorf("inyectó sin DEMO=1: %q", got)
		}
	})

	t.Run("demo+origen inyecta antes de </head>", func(t *testing.T) {
		t.Setenv("GC_ORIGIN", origin+"/") // barra final: debe recortarse
		got := gcIndexOverride(true, testFS())
		if got == nil {
			t.Fatal("no inyectó con demo+origen")
		}
		s := string(got)
		if !strings.Contains(s, `data-goatcounter="`+origin+`/count"`) {
			t.Errorf("falta el endpoint de count: %s", s)
		}
		if !strings.Contains(s, `src="`+origin+`/count.js"`) {
			t.Errorf("falta el script de count.js: %s", s)
		}
		if !strings.Contains(s, `return '/demo'+p`) {
			t.Errorf("falta el prefijo /demo en las rutas: %s", s)
		}
		if strings.Contains(s, origin+`//`) {
			t.Errorf("la barra final del origen no se recortó: %s", s)
		}
		if pos := strings.Index(s, origin); pos == -1 || pos > strings.LastIndex(s, "</head>") {
			t.Errorf("el snippet no quedó antes de </head>: %s", s)
		}
	})

	t.Run("html sin </head> no inyecta", func(t *testing.T) {
		t.Setenv("GC_ORIGIN", origin)
		fsys := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><body></body></html>")}}
		if got := gcIndexOverride(true, fsys); got != nil {
			t.Errorf("inyectó en un HTML sin </head>: %q", got)
		}
	})
}

// TestSpaHandlerIndexOverride (#103): con override, "/" y el fallback SPA
// sirven el index inyectado; el resto de ficheros (sw.js) salen del FS.
func TestSpaHandlerIndexOverride(t *testing.T) {
	override := []byte("<!doctype html><html>inyectado</html>")
	h := spaHandler(http.FS(testFS()), override)

	t.Run("raiz sirve el override", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if got := rec.Body.String(); got != string(override) {
			t.Errorf("body=/ %q", got)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("Content-Type=%q", ct)
		}
	})

	t.Run("fallback SPA sirve el override", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/pools", nil))
		if got := rec.Body.String(); got != string(override) {
			t.Errorf("body=/pools %q", got)
		}
	})

	t.Run("sw.js no se toca", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/sw.js", nil))
		if got := rec.Body.String(); !strings.HasPrefix(got, "self.addEventListener") {
			t.Errorf("sw.js alterado: %q", got)
		}
	})

	t.Run("sin override sirve el index del FS", func(t *testing.T) {
		h2 := spaHandler(http.FS(testFS()), nil)
		rec := httptest.NewRecorder()
		h2.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if !strings.Contains(rec.Body.String(), "<div id=root>") {
			t.Errorf("index del FS alterado: %q", rec.Body.String())
		}
	})
}
