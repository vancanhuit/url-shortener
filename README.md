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

Using [curl](https://curl.haxx.se):
```sh
$ curl -X POST -d '{"url": "https://google.com"}' -H "Content-Type: application/json" http://localhost:8000/api/shorten | jq .
{
  "short_link": "Nw4IY0Y"
}
```

Using [httpie](https://httpie.org):
```sh
$ http POST localhost:8000/api/shorten url=https://freecodecamp.org

HTTP/1.1 200 OK
content-length: 24
content-type: application/json
date: Wed, 15 Jul 2020 13:56:37 GMT
server: uvicorn

{
    "short_link": "pk9nq5_"
}
```

Open brower at http://localhost:8000/Nw4IY0Y or http://localhost:8000/pk9nq5_ (the links will be varied because they depend on timestamp).
