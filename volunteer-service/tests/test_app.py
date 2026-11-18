from decimal import Decimal

import pytest
from botocore.exceptions import ClientError

import app as vol


class FakeTable:
    def __init__(self):
        self.items = []
        self.fail = False

    def _check(self, op):
        if self.fail:
            raise ClientError({"Error": {"Code": "ResourceNotFoundException", "Message": "x"}}, op)

    def load(self):
        self._check("DescribeTable")

    def put_item(self, Item):
        self._check("PutItem")
        self.items.append(Item)

    def query(self, IndexName, KeyConditionExpression):
        self._check("Query")
        assert IndexName == "ngo_id-index"
        ngo_id = KeyConditionExpression.get_expression()["values"][1]
        return {"Items": [i for i in self.items if i["ngo_id"] == ngo_id]}


@pytest.fixture
def client(monkeypatch):
    table = FakeTable()
    monkeypatch.setattr(vol, "get_table", lambda: table)
    vol.app.config["TESTING"] = True
    with vol.app.test_client() as c:
        c.table = table
        yield c


def test_health(client):
    assert client.get("/health").status_code == 200


def test_ready_e_falha(client):
    assert client.get("/ready").status_code == 200
    client.table.fail = True
    assert client.get("/ready").status_code == 503


def test_inscreve_e_consulta_por_ong(client):
    resp = client.post("/volunteers", json={"name": "Ana", "email": "ana@x.org", "ngo_id": "2"})
    assert resp.status_code == 201
    assert resp.get_json()["ngo_id"] == 2
    client.post("/volunteers", json={"name": "Bia", "email": "bia@x.org", "ngo_id": 3})
    lista = client.get("/volunteers/2").get_json()
    assert [v["name"] for v in lista] == ["Ana"]


def test_validacao(client):
    assert client.post("/volunteers", json={"name": "Ana"}).status_code == 400
    assert client.post("/volunteers", json={"name": "Ana", "email": "a@x", "ngo_id": "abc"}).status_code == 400


def test_erro_dynamodb_vira_500(client):
    client.table.fail = True
    assert client.post("/volunteers", json={"name": "Ana", "email": "a@x", "ngo_id": 1}).status_code == 500
    assert client.get("/volunteers/1").status_code == 500


def test_decimal_serializa():
    # O DynamoDB devolve números como Decimal; o Flask precisa conseguir serializar.
    with vol.app.app_context():
        assert vol.jsonify({"ngo_id": Decimal("2")}).status_code == 200
