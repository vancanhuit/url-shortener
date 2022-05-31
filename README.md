# URL Shortener with FastAPI

[https://github.com/tiangolo/fastapi](https://github.com/tiangolo/fastapi)


```sh
$ git clone https://github.com/anuraagnalluri/url-shortener-python 
$ cd url-shortener
$ docker-compose up -d --build
$ docker-compose logs
```

Using [curl](https://curl.haxx.se)

To shorten a link:
```sh
$ curl -X POST -d '{"url": "https://google.com"}' -H "Content-Type: application/json" http://localhost:8000/api/shorten | jq .
{
  "short_link": "Nw4IY0Y"
}
```

To access a shortened link, point your browser to http://localhost:8000/Nw4IY0Y
