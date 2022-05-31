from datetime import datetime, timezone

from pydantic import HttpUrl
from fastapi import FastAPI, Depends, Body, HTTPException
from fastapi.responses import RedirectResponse

app = FastAPI()

# Implement shorten and redirect API methods here

@app.post('/api/shorten')
def get_short_link():
    # Write code here


@app.get('/{short_link}')
def redirect(short_link: str):
    # Write code here
