# URL Shortener with FastAPI

[https://github.com/tiangolo/fastapi](https://github.com/tiangolo/fastapi)


```sh
$ git clone https://github.com/vancanhuit/url-shortener.git
$ cd url-shortener
$ docker-compose up -d --build
$ docker-compose exec api alembic upgrade head # Run for the first time to initialize database schemas
$ docker-compose logs
$ docker-compose exec db psql --username=dev --dbname=dev # Check database schemas
```

```sh
$ curl -X POST -d '{"url": "https://google.com"}' -H "Content-Type: application/json" http://localhost:8000/api/shorten | jq .
{
  "short_link": "Nw4IY0Y"
}
```

Open brower at http://localhost:8000/Nw4IY0Y (the link will be varied because it depends on timestamp).
