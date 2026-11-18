package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu       sync.Mutex
	saved    []Donation
	failNext bool
	pingErr  error
}

func (f *fakeStore) Insert(_ context.Context, d *Donation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		return errors.New("db down")
	}
	d.ID = len(f.saved) + 1
	d.CreatedAt = time.Now()
	f.saved = append(f.saved, *d)
	return nil
}

func (f *fakeStore) List(context.Context, int) ([]Donation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saved, nil
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

type fakePublisher struct {
	mu    sync.Mutex
	count int
}

func (p *fakePublisher) Publish(context.Context, Donation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count++
	return nil
}

func do(t *testing.T, app *App, method, path, body, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	app := NewApp(&fakeStore{}, noopPublisher{}, "")
	if rec := do(t, app, http.MethodGet, "/health", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("esperava 200, veio %d", rec.Code)
	}
}

func TestReadyFalhaSemBanco(t *testing.T) {
	app := NewApp(&fakeStore{pingErr: errors.New("timeout")}, noopPublisher{}, "")
	if rec := do(t, app, http.MethodGet, "/ready", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("esperava 503, veio %d", rec.Code)
	}
}

func TestCriaDoacao(t *testing.T) {
	store, pub := &fakeStore{}, &fakePublisher{}
	app := NewApp(store, pub, "")
	rec := do(t, app, http.MethodPost, "/donations", `{"ngo_id":1,"amount":50.5,"donor_name":"Maria"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperava 201, veio %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"APPROVED"`) {
		t.Fatalf("doação deveria vir APPROVED: %s", rec.Body.String())
	}
	if len(store.saved) != 1 {
		t.Fatalf("esperava 1 doação salva, tem %d", len(store.saved))
	}
}

func TestValidacao(t *testing.T) {
	app := NewApp(&fakeStore{}, noopPublisher{}, "")
	casos := map[string]string{
		"json inválido":    `{"ngo_id":`,
		"ngo_id zero":      `{"ngo_id":0,"amount":10,"donor_name":"A"}`,
		"amount negativo":  `{"ngo_id":1,"amount":-5,"donor_name":"A"}`,
		"sem nome":         `{"ngo_id":1,"amount":5,"donor_name":"  "}`,
		"amount gigante":   `{"ngo_id":1,"amount":5000000,"donor_name":"A"}`,
		"nome muito longo": `{"ngo_id":1,"amount":5,"donor_name":"` + strings.Repeat("x", 101) + `"}`,
	}
	for nome, body := range casos {
		t.Run(nome, func(t *testing.T) {
			if rec := do(t, app, http.MethodPost, "/donations", body, ""); rec.Code != http.StatusBadRequest {
				t.Fatalf("esperava 400, veio %d", rec.Code)
			}
		})
	}
}

func TestErroNoBancoVira500(t *testing.T) {
	app := NewApp(&fakeStore{failNext: true}, noopPublisher{}, "")
	rec := do(t, app, http.MethodPost, "/donations", `{"ngo_id":1,"amount":5,"donor_name":"A"}`, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("esperava 500, veio %d", rec.Code)
	}
}

func TestMetodoNaoPermitido(t *testing.T) {
	app := NewApp(&fakeStore{}, noopPublisher{}, "")
	if rec := do(t, app, http.MethodDelete, "/donations", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("esperava 405, veio %d", rec.Code)
	}
}

func TestChaos(t *testing.T) {
	app := NewApp(&fakeStore{}, noopPublisher{}, "segredo")

	if rec := do(t, app, http.MethodPost, "/admin/chaos?mode=error&percent=100", "", "Bearer errado"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token errado deveria dar 401, veio %d", rec.Code)
	}
	if rec := do(t, app, http.MethodPost, "/admin/chaos?mode=error&percent=100", "", "Bearer segredo"); rec.Code != http.StatusOK {
		t.Fatalf("ligar chaos deveria dar 200, veio %d", rec.Code)
	}
	rec := do(t, app, http.MethodPost, "/donations", `{"ngo_id":1,"amount":5,"donor_name":"A"}`, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("com chaos 100%% esperava 500, veio %d", rec.Code)
	}
	// /health continua 200: a falha é "silenciosa" para o Kubernetes.
	if rec := do(t, app, http.MethodGet, "/health", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("health deveria seguir 200, veio %d", rec.Code)
	}
	do(t, app, http.MethodDelete, "/admin/chaos", "", "Bearer segredo")
	rec = do(t, app, http.MethodPost, "/donations", `{"ngo_id":1,"amount":5,"donor_name":"A"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("sem chaos esperava 201, veio %d", rec.Code)
	}
}

func TestChaosDesligadoSemToken(t *testing.T) {
	app := NewApp(&fakeStore{}, noopPublisher{}, "")
	if rec := do(t, app, http.MethodPost, "/admin/chaos", "", "Bearer x"); rec.Code != http.StatusNotFound {
		t.Fatalf("sem ADMIN_TOKEN o endpoint não deveria existir, veio %d", rec.Code)
	}
}
