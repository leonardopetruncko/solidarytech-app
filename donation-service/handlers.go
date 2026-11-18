package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Donation struct {
	ID        int       `json:"id"`
	NgoID     int       `json:"ngo_id"`
	Amount    float64   `json:"amount"`
	DonorName string    `json:"donor_name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Store e Publisher são interfaces para os testes rodarem sem banco e sem AWS.
type Store interface {
	Insert(ctx context.Context, d *Donation) error
	List(ctx context.Context, limit int) ([]Donation, error)
	Ping(ctx context.Context) error
}

type Publisher interface {
	Publish(ctx context.Context, d Donation) error
}

type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, Donation) error { return nil }

type App struct {
	store      Store
	publisher  Publisher
	adminToken string
	chaos      *Chaos

	// Métricas de negócio (vão para o Prometheus e o Datadog via OTel Collector).
	donationsCounter metric.Int64Counter
	amountHistogram  metric.Float64Histogram
}

func NewApp(store Store, publisher Publisher, adminToken string) *App {
	meter := otel.Meter("donation-service")
	counter, _ := meter.Int64Counter("solidarytech.donations",
		metric.WithDescription("Doações recebidas, por resultado"))
	histogram, _ := meter.Float64Histogram("solidarytech.donation.amount",
		metric.WithDescription("Valor das doações aprovadas"), metric.WithUnit("BRL"))

	return &App{
		store:            store,
		publisher:        publisher,
		adminToken:       adminToken,
		chaos:            &Chaos{},
		donationsCounter: counter,
		amountHistogram:  histogram,
	}
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("/ready", a.ready)
	mux.HandleFunc("/donations", a.donations)
	mux.HandleFunc("/admin/chaos", a.adminChaos)
	return mux
}

// Liveness: o processo está de pé. NÃO depende do banco (senão uma queda do RDS
// faria o Kubernetes reiniciar todos os pods em cascata).
func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "donation-service"})
}

// Readiness: só recebe tráfego se o banco responde.
func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "erro": "banco indisponível"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *App) donations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.createDonation(w, r)
	case http.MethodGet:
		a.listDonations(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
	}
}

func validate(d Donation) error {
	switch {
	case d.NgoID <= 0:
		return errors.New("ngo_id deve ser maior que zero")
	case d.Amount <= 0:
		return errors.New("amount deve ser maior que zero")
	case d.Amount > 1_000_000:
		return errors.New("amount acima do limite por doação")
	case strings.TrimSpace(d.DonorName) == "":
		return errors.New("donor_name é obrigatório")
	case len(d.DonorName) > 100:
		return errors.New("donor_name com mais de 100 caracteres")
	}
	return nil
}

func (a *App) createDonation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Falha injetada (simulação de incidente): ver chaos.go.
	if a.chaos.Apply(ctx) {
		a.count(ctx, "error")
		slog.ErrorContext(ctx, "falha ao processar doação: gateway de pagamento indisponível")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var d Donation
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		a.count(ctx, "invalid")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Payload inválido"})
		return
	}
	if err := validate(d); err != nil {
		a.count(ctx, "invalid")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	d.Status = "APPROVED" // simulação do gateway de pagamento
	if err := a.store.Insert(ctx, &d); err != nil {
		a.count(ctx, "error")
		slog.ErrorContext(ctx, "erro ao salvar doação", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	// O evento sai em segundo plano, mas continua no mesmo trace (WithoutCancel
	// mantém o span pai e não morre quando a resposta HTTP termina).
	go func(ctx context.Context, d Donation) {
		if err := a.publisher.Publish(ctx, d); err != nil {
			slog.ErrorContext(ctx, "falha ao publicar evento de doação no SQS", "erro", err, "donation_id", d.ID)
		}
	}(context.WithoutCancel(ctx), d)

	a.count(ctx, "approved")
	a.amountHistogram.Record(ctx, d.Amount)
	slog.InfoContext(ctx, "doação aprovada", "donation_id", d.ID, "ngo_id", d.NgoID, "amount", d.Amount)
	writeJSON(w, http.StatusCreated, d)
}

func (a *App) listDonations(w http.ResponseWriter, r *http.Request) {
	// LIMIT: uma listagem sem limite cresce com a base e degrada a latência do Hot Path.
	list, err := a.store.List(r.Context(), 100)
	if err != nil {
		slog.ErrorContext(r.Context(), "erro ao listar doações", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *App) count(ctx context.Context, result string) {
	a.donationsCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
