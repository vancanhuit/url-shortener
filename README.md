# URL Shortener with FastAPI

[![CI](https://github.com/vancanhuit/url-shortener/actions/workflows/ci.yaml/badge.svg?branch=master)](https://github.com/vancanhuit/url-shortener/actions/workflows/ci.yaml)

[https://github.com/tiangolo/fastapi](https://github.com/tiangolo/fastapi)

```sh
$ docker version
 Version:           20.10.12
 API version:       1.41
 Go version:        go1.17.3
 Git commit:        20.10.12-0ubuntu4
 Built:             Mon Mar  7 17:10:06 2022
 OS/Arch:           linux/amd64
 Context:           default
 Experimental:      true

Server:
 Engine:
  Version:          20.10.12
  API version:      1.41 (minimum version 1.12)
  Go version:       go1.17.3
  Git commit:       20.10.12-0ubuntu4
  Built:            Mon Mar  7 15:57:50 2022
  OS/Arch:          linux/amd64
  Experimental:     false
 containerd:
  Version:          1.5.9-0ubuntu3
  GitCommit:
 runc:
  Version:          1.1.0-0ubuntu1
  GitCommit:
 docker-init:
  Version:          0.19.0
  GitCommit:

$ docker-compose version
docker-compose version 1.29.2, build 5becea4c
docker-py version: 5.0.0
CPython version: 3.7.10
OpenSSL version: OpenSSL 1.1.0l  10 Sep 2019
```


```sh
$ git clone https://github.com/vancanhuit/url-shortener.git
$ cd url-shortener
$ docker-compose up -d --build
$ docker-compose exec api alembic upgrade head # Run for the first time to initialize database schemas
$ docker-compose logs -f
$ docker-compose exec db psql --username=dev --dbname=dev # Check database schemas
$ docker-compose exec api pytest # Run test
```

Using [curl](https://curl.haxx.se) and [jq](https://stedolan.github.io/jq/):
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

Point your browser to http://localhost:8000/Nw4IY0Y or http://localhost:8000/pk9nq5_ (the links will be varied because they depend on timestamp).
