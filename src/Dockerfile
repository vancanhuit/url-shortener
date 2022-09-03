FROM python:3.10-slim-bullseye

WORKDIR /src

ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONPATH /src

COPY ./requirements.txt .

RUN pip install -U pip && \
    pip install -r requirements.txt

COPY . .
