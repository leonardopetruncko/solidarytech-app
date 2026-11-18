package main

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Chaos injeta uma falha "silenciosa" no POST /donations para os testes de
// resiliência (Game Day): o /health continua 200, então o Kubernetes não percebe.
// Quem percebe é a observabilidade (SLO burn rate -> alerta -> self-healing).
//
//	mode=error   -> uma fração das doações responde 500 (queima o SLO de disponibilidade)
//	mode=latency -> as doações demoram 1,2 s (queima o SLO de latência)
//
// A falha vive só na memória do pod: um rollout restart (self-healer) a remove.
type Chaos struct {
	mu      sync.RWMutex
	mode    string
	percent int
}

func (c *Chaos) set(mode string, percent int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode, c.percent = mode, percent
}

func (c *Chaos) get() (string, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode, c.percent
}

// Apply devolve true quando a requisição deve falhar com 500.
func (c *Chaos) Apply(ctx context.Context) bool {
	mode, percent := c.get()
	switch mode {
	case "latency":
		select {
		case <-time.After(1200 * time.Millisecond):
		case <-ctx.Done():
		}
		return false
	case "error":
		return rand.IntN(100) < percent //nolint:gosec // não é uso criptográfico
	}
	return false
}

func (a *App) adminChaos(w http.ResponseWriter, r *http.Request) {
	// Sem ADMIN_TOKEN configurado o endpoint nem existe.
	if a.adminToken == "" {
		http.NotFound(w, r)
		return
	}
	token := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(token), []byte("Bearer "+a.adminToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "não autorizado"})
		return
	}

	switch r.Method {
	case http.MethodPost:
		mode := r.URL.Query().Get("mode")
		if mode == "" {
			mode = "error"
		}
		if mode != "error" && mode != "latency" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode deve ser error ou latency"})
			return
		}
		percent, err := strconv.Atoi(r.URL.Query().Get("percent"))
		if err != nil || percent < 1 || percent > 100 {
			percent = 30
		}
		a.chaos.set(mode, percent)
		slog.WarnContext(r.Context(), "CHAOS ATIVADO no donation-service", "mode", mode, "percent", percent)
		writeJSON(w, http.StatusOK, map[string]any{"chaos": "on", "mode": mode, "percent": percent})
	case http.MethodDelete:
		a.chaos.set("", 0)
		slog.WarnContext(r.Context(), "chaos desligado no donation-service")
		writeJSON(w, http.StatusOK, map[string]string{"chaos": "off"})
	case http.MethodGet:
		mode, percent := a.chaos.get()
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "percent": percent})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
	}
}
