#!/bin/sh
# one HTTP exchange: read the request head, then answer with the JWKS
while IFS= read -r line; do [ "$(printf %s "$line" | tr -d '\r')" = "" ] && break; done
L=$(wc -c < /srv/jwks.json)
printf "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n" "$L"
cat /srv/jwks.json
