import hashlib
import base64


def create_short_link(original_url: str, timestamp: float):
    to_encode = f'{original_url}{timestamp}'

    b64_encoded_str = base64.urlsafe_b64encode(
        hashlib.sha256(to_encode.encode()).digest()).decode()
    return b64_encoded_str[:7]
