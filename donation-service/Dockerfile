# --- Build: compila um binário estático ---
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Dependências primeiro: camada em cache enquanto o go.mod não muda.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/donation-service .

# --- Final: distroless (sem shell, sem gerenciador de pacotes, usuário não-root) ---
# Imagem final com ~15 MB e superfície de ataque mínima para o Trivy.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/donation-service /app/donation-service

# UID numérico: o runAsNonRoot do Kubernetes só consegue validar usuário numérico.
USER 65532:65532
EXPOSE 8082
ENTRYPOINT ["/app/donation-service"]
