"""ngo-service: cadastro e gestão das ONGs parceiras da SolidaryTech."""

import logging
import os
import threading

import psycopg2
from dotenv import load_dotenv
from flask import Flask, jsonify, request
from psycopg2.extras import RealDictCursor
from psycopg2.pool import ThreadedConnectionPool

from telemetry import setup_logging, setup_telemetry

load_dotenv()
setup_logging("ngo-service")
log = logging.getLogger("ngo-service")

app = Flask(__name__)
# OpenTelemetry antes do pool, para instrumentar as conexões do psycopg2.
setup_telemetry(flask_app=app, psycopg2=True)

DATABASE_URL = os.getenv("DATABASE_URL")
CAMPOS_OBRIGATORIOS = ("name", "email", "cause", "city")

_pool = None
_pool_lock = threading.Lock()


def get_pool():
    """Pool criado sob demanda: se o banco cair, o pod sobe e o /ready mostra o problema."""
    global _pool
    with _pool_lock:
        if _pool is None:
            if not DATABASE_URL:
                raise RuntimeError("DATABASE_URL não definida")
            _pool = ThreadedConnectionPool(1, 10, dsn=DATABASE_URL, connect_timeout=5)
            log.info("Pool de conexões com o PostgreSQL inicializado.")
        return _pool


@app.route("/health")
def health():
    return jsonify({"status": "ok", "service": "ngo-service"})


@app.route("/ready")
def ready():
    try:
        conn = get_pool().getconn()
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
        finally:
            get_pool().putconn(conn)
    except Exception as e:  # noqa: BLE001 - qualquer falha = não pronto
        log.warning("Readiness falhou: %s", e)
        return jsonify({"status": "unavailable"}), 503
    return jsonify({"status": "ready"})


@app.route("/ngos", methods=["POST"])
def create_ngo():
    data = request.get_json(silent=True)
    if not data or not all(str(data.get(k, "")).strip() for k in CAMPOS_OBRIGATORIOS):
        return jsonify({"error": "Campos obrigatórios ausentes"}), 400

    conn = get_pool().getconn()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                "INSERT INTO ngos (name, email, cause, city) VALUES (%s, %s, %s, %s) RETURNING *",
                (data["name"], data["email"], data["cause"], data["city"]),
            )
            new_ngo = cur.fetchone()
            conn.commit()
            log.info("ONG cadastrada: id=%s", new_ngo["id"])
            return jsonify(new_ngo), 201
    except psycopg2.IntegrityError:
        conn.rollback()
        return jsonify({"error": "E-mail já cadastrado"}), 409
    except Exception:
        conn.rollback()
        log.exception("Erro ao criar ONG")
        return jsonify({"error": "Erro interno"}), 500
    finally:
        get_pool().putconn(conn)


@app.route("/ngos", methods=["GET"])
def get_ngos():
    conn = get_pool().getconn()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute("SELECT * FROM ngos ORDER BY id DESC LIMIT 100")
            return jsonify(cur.fetchall()), 200
    except Exception:
        log.exception("Erro ao buscar ONGs")
        return jsonify({"error": "Erro interno"}), 500
    finally:
        get_pool().putconn(conn)


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.getenv("PORT", "8081")))  # nosec B104
