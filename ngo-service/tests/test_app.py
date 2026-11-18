import os

import psycopg2
import pytest

os.environ.setdefault("DATABASE_URL", "postgres://teste:teste@localhost:5432/ngo_db")

import app as ngo  # noqa: E402


class FakeCursor:
    def __init__(self, db):
        self.db = db
        self.result = None

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def execute(self, sql, params=None):
        if self.db.fail:
            raise self.db.fail
        if sql.startswith("INSERT"):
            name, email, cause, city = params
            if any(n["email"] == email for n in self.db.rows):
                raise psycopg2.IntegrityError("duplicado")
            row = {"id": len(self.db.rows) + 1, "name": name, "email": email, "cause": cause, "city": city}
            self.db.rows.append(row)
            self.result = row
        elif sql.startswith("SELECT 1"):
            self.result = 1
        else:
            self.result = list(reversed(self.db.rows))

    def fetchone(self):
        return self.result

    def fetchall(self):
        return self.result


class FakeConn:
    def __init__(self, db):
        self.db = db

    def cursor(self, cursor_factory=None):
        return FakeCursor(self.db)

    def commit(self):
        pass

    def rollback(self):
        pass


class FakePool:
    def __init__(self):
        self.rows = []
        self.fail = None

    def getconn(self):
        return FakeConn(self)

    def putconn(self, conn):
        pass


@pytest.fixture
def client(monkeypatch):
    pool = FakePool()
    monkeypatch.setattr(ngo, "get_pool", lambda: pool)
    ngo.app.config["TESTING"] = True
    with ngo.app.test_client() as c:
        c.pool = pool
        yield c


NGO = {"name": "Anjos de Patas", "email": "contato@anjos.org", "cause": "Proteção Animal", "city": "Osasco"}


def test_health(client):
    assert client.get("/health").status_code == 200


def test_ready(client):
    assert client.get("/ready").status_code == 200


def test_ready_sem_banco(client):
    client.pool.fail = psycopg2.OperationalError("down")
    assert client.get("/ready").status_code == 503


def test_cria_e_lista_ong(client):
    resp = client.post("/ngos", json=NGO)
    assert resp.status_code == 201
    assert resp.get_json()["id"] == 1
    lista = client.get("/ngos").get_json()
    assert lista[0]["email"] == NGO["email"]


def test_campos_obrigatorios(client):
    assert client.post("/ngos", json={"name": "Sem email"}).status_code == 400
    assert client.post("/ngos", json={**NGO, "city": "  "}).status_code == 400
    assert client.post("/ngos", data="não é json").status_code == 400


def test_email_duplicado(client):
    client.post("/ngos", json=NGO)
    assert client.post("/ngos", json=NGO).status_code == 409


def test_erro_de_banco_vira_500(client):
    client.pool.fail = psycopg2.OperationalError("down")
    assert client.post("/ngos", json=NGO).status_code == 500
