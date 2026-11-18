"""volunteer-service: inscrição de voluntários nas campanhas das ONGs (DynamoDB)."""

import logging
import os
import time
import uuid

import boto3
from boto3.dynamodb.conditions import Key
from botocore.exceptions import BotoCoreError, ClientError
from dotenv import load_dotenv
from flask import Flask, jsonify, request

from telemetry import setup_logging, setup_telemetry

load_dotenv()
setup_logging("volunteer-service")
log = logging.getLogger("volunteer-service")

app = Flask(__name__)
# OpenTelemetry antes do boto3, para instrumentar as chamadas ao DynamoDB.
setup_telemetry(flask_app=app, botocore=True)

AWS_REGION = os.getenv("AWS_REGION", "us-east-1")
DYNAMODB_TABLE = os.getenv("AWS_DYNAMODB_TABLE", "SolidaryTechVolunteers")
# Índice secundário (GSI) por ONG, criado no Terraform: a consulta lê só os itens
# da ONG pedida, em vez de um Scan que lê (e cobra) a tabela inteira.
NGO_INDEX = os.getenv("AWS_DYNAMODB_NGO_INDEX", "ngo_id-index")
CAMPOS_OBRIGATORIOS = ("name", "email", "ngo_id")

_table = None


def get_table():
    """Cliente criado sob demanda (credenciais: LabRole do nó via IMDS no EKS)."""
    global _table
    if _table is None:
        _table = boto3.resource("dynamodb", region_name=AWS_REGION).Table(DYNAMODB_TABLE)
        log.info("Usando a tabela DynamoDB %s (%s)", DYNAMODB_TABLE, AWS_REGION)
    return _table


@app.route("/health")
def health():
    return jsonify({"status": "ok", "service": "volunteer-service"})


@app.route("/ready")
def ready():
    try:
        get_table().load()  # DescribeTable: confirma credencial, rede e tabela
    except (BotoCoreError, ClientError) as e:
        log.warning("Readiness falhou: %s", e)
        return jsonify({"status": "unavailable"}), 503
    return jsonify({"status": "ready"})


@app.route("/volunteers", methods=["POST"])
def register_volunteer():
    data = request.get_json(silent=True)
    if not data or not all(str(data.get(k, "")).strip() for k in CAMPOS_OBRIGATORIOS):
        return jsonify({"error": "Campos obrigatórios ausentes"}), 400
    try:
        ngo_id = int(data["ngo_id"])
    except (TypeError, ValueError):
        return jsonify({"error": "ngo_id deve ser numérico"}), 400

    item = {
        "volunteer_id": str(uuid.uuid4()),
        "name": data["name"],
        "email": data["email"],
        "ngo_id": ngo_id,
        "registered_at": str(int(time.time())),
    }
    try:
        get_table().put_item(Item=item)
    except (BotoCoreError, ClientError):
        log.exception("Erro ao salvar voluntário no DynamoDB")
        return jsonify({"error": "Erro interno ao processar dados"}), 500
    log.info("Voluntário inscrito: ngo_id=%s", ngo_id)
    return jsonify(item), 201


@app.route("/volunteers/<int:ngo_id>", methods=["GET"])
def get_volunteers_by_ngo(ngo_id):
    try:
        response = get_table().query(IndexName=NGO_INDEX, KeyConditionExpression=Key("ngo_id").eq(ngo_id))
    except (BotoCoreError, ClientError):
        log.exception("Erro ao buscar voluntários no DynamoDB")
        return jsonify({"error": "Erro interno"}), 500
    return jsonify(response.get("Items", [])), 200


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.getenv("PORT", "8083")))  # nosec B104
