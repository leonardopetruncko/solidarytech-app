# SolidaryTech - Microsserviços

Monorepo com os 3 microsserviços da SolidaryTech (base: [dougls/hackathon-DCLT](https://github.com/dougls/hackathon-DCLT)), evoluídos para produção.

| Serviço | Stack | Porta | Dados |
|---|---|---|---|
| `ngo-service` | Python 3.12 / Flask / Gunicorn | 8081 | PostgreSQL (`ngo_db`) |
| `donation-service` (**Hot Path**) | Go 1.26 | 8082 | PostgreSQL (`donation_db`) + SQS |
| `volunteer-service` | Python 3.12 / Flask / Gunicorn | 8083 | DynamoDB |

## O que mudou em relação ao código original

- **Observabilidade:** OpenTelemetry (traces + métricas via OTLP), logs JSON com `trace_id`/`span_id` e métricas de negócio (doações e valor doado).
- **Saúde:** `/health` (liveness, só o processo) e `/ready` (readiness, checa banco/DynamoDB); graceful shutdown.
- **donation-service:**
  - Migração do `aws-sdk-go` v1 (fim de suporte) para o **v2** e do pgx v4 para o v5.
  - Validação de payload e `LIMIT` na listagem.
  - Endpoint `/admin/chaos`, protegido por token, para o Game Day.
- **volunteer-service:** `Query` num índice por ONG (GSI) em vez de `Scan` na tabela inteira (custo e latência).
- **Segurança:** imagens multi-stage, distroless (Go) e slim (Python), usuário não-root, pip removido da imagem final, pacotes do SO atualizados.
- **Testes unitários** nos 3 serviços.

## CI/CD DevSecOps (`.github/workflows/`)

Um pipeline por serviço, disparado só quando a pasta do serviço muda. Reaproveita os [reusable workflows](https://github.com/leonardopetruncko/reusable-workflows) das fases anteriores:

`lint + testes` → `SAST (gosec/bandit) + SCA (Trivy)` → `build + scan da imagem (Trivy) + push no ECR` → `atualiza a tag no repo GitOps` → o Argo CD faz o deploy.