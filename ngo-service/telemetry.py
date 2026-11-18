"""OpenTelemetry (traces + métricas) e logs estruturados em JSON.

Os dados saem via OTLP (gRPC) para o OTel Collector do cluster, que roteia cada sinal
para o backend certo (Datadog, Prometheus e Loki). Configuração pelas variáveis OTEL_*
padrão. Sem OTEL_EXPORTER_OTLP_ENDPOINT (dev local, testes e CI) a telemetria fica
desligada e o código roda igual.
"""

import json
import logging
import os

log = logging.getLogger(__name__)


class JsonFormatter(logging.Formatter):
    """Uma linha JSON por log, com trace_id/span_id para ligar o log (Loki) ao trace (Datadog)."""

    def __init__(self, service):
        super().__init__()
        self.service = service

    def format(self, record):
        data = {
            "time": self.formatTime(record, "%Y-%m-%dT%H:%M:%S%z"),
            "level": record.levelname,
            "service": self.service,
            "logger": record.name,
            "msg": record.getMessage(),
        }
        trace_id = getattr(record, "otelTraceID", "0")
        if trace_id and trace_id != "0":
            data["trace_id"] = trace_id
            data["span_id"] = getattr(record, "otelSpanID", "0")
        if record.exc_info:
            data["exc"] = self.formatException(record.exc_info)
        return json.dumps(data, ensure_ascii=False)


def setup_logging(service):
    handler = logging.StreamHandler()
    handler.setFormatter(JsonFormatter(service))
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(logging.INFO)


def setup_telemetry(flask_app=None, psycopg2=False, botocore=False):
    """Liga o SDK do OpenTelemetry e as instrumentações automáticas pedidas.

    Precisa rodar ANTES de criar o pool do PostgreSQL ou os clientes do boto3,
    porque as instrumentações substituem as funções de conexão dessas libs.
    """
    if not os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT"):
        log.info("OTEL_EXPORTER_OTLP_ENDPOINT não definida: telemetria desativada.")
        return

    from opentelemetry import metrics, trace
    from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
    from opentelemetry.instrumentation.logging import LoggingInstrumentor
    from opentelemetry.sdk.metrics import MeterProvider
    from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    # O Resource.create() lê OTEL_SERVICE_NAME e OTEL_RESOURCE_ATTRIBUTES.
    resource = Resource.create()

    tracer_provider = TracerProvider(resource=resource)
    tracer_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(tracer_provider)

    reader = PeriodicExportingMetricReader(OTLPMetricExporter(), export_interval_millis=15000)
    metrics.set_meter_provider(MeterProvider(resource=resource, metric_readers=[reader]))

    if flask_app is not None:
        from opentelemetry.instrumentation.flask import FlaskInstrumentor

        # Probes fora dos traces e das métricas (não inflam nem distorcem o SLO).
        FlaskInstrumentor().instrument_app(flask_app, excluded_urls="health,ready")

    if psycopg2:
        from opentelemetry.instrumentation.psycopg2 import Psycopg2Instrumentor

        # skip_dep_check: a imagem usa o pacote psycopg2-binary, não o psycopg2.
        Psycopg2Instrumentor().instrument(skip_dep_check=True)

    if botocore:
        from opentelemetry.instrumentation.botocore import BotocoreInstrumentor

        BotocoreInstrumentor().instrument()

    # Coloca otelTraceID/otelSpanID em cada registro de log (o JsonFormatter os publica).
    LoggingInstrumentor().instrument(inject_trace_context=True, enable_log_auto_instrumentation=False)

    log.info("OpenTelemetry inicializado (traces + métricas via OTLP).")
