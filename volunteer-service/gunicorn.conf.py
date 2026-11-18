"""Configuração do Gunicorn (dimensionada para o request de 100m CPU / 128Mi do pod)."""

import os

bind = f"0.0.0.0:{os.getenv('PORT', '8083')}"  # nosec B104
workers = int(os.getenv("GUNICORN_WORKERS", "2"))
threads = int(os.getenv("GUNICORN_THREADS", "4"))
worker_class = "gthread"
timeout = 30
graceful_timeout = 20  # termina as requisições em andamento no rollout
keepalive = 5
accesslog = None  # o acesso já vira span/métrica no OpenTelemetry
