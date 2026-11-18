# --- Build: instala as dependências num diretório separado ---
FROM python:3.12-slim AS builder
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# --- Final: só o runtime + dependências, usuário não-root ---
FROM python:3.12-slim
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PORT=8081

# Patches de segurança do SO + remove o pip (não é usado em runtime e traz CVEs ao scan).
RUN apt-get update && apt-get upgrade -y --no-install-recommends && rm -rf /var/lib/apt/lists/* \
 && pip uninstall -y pip setuptools wheel 2>/dev/null; \
    groupadd -r app && useradd -r -g app -u 10001 app

WORKDIR /app
COPY --from=builder /install /usr/local
COPY app.py telemetry.py gunicorn.conf.py ./

USER 10001
EXPOSE 8081
CMD ["gunicorn", "-c", "gunicorn.conf.py", "app:app"]
