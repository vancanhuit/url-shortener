FROM python:3.10-slim-bullseye

WORKDIR /src

ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONPATH /src

COPY ./requirements.txt .

RUN apt-get update && \
    apt-get --yes install gcc python3-dev libpq-dev && \
    apt-get autoclean && apt-get autoremove && \
    apt-get clean

RUN pip install --no-cache-dir -U pip && \
    pip install --no-cache-dir -r requirements.txt

COPY . .
