from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def test_main():
    response = client.post(
        "/api/shorten",
        headers={"Content-Type": "application/json"},
        json={"url": "https://www.reddit.com"},
    )

    assert response.status_code == 200
    assert "short_link" in response.json()
    short_link = response.json()["short_link"]
    assert len(short_link) == 7

    response = client.get("/{}".format(short_link), allow_redirects=False)
    assert response.status_code == 307
    assert response.headers["Location"] == "https://www.reddit.com"

    response = client.get("/non-existing-url", allow_redirects=False)
    assert response.status_code == 404
