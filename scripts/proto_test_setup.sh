#!/bin/bash
# Sets up a self-signed cert in /opt/zenithpanel/data/certs/ so TLS-bound
# inbounds (Hysteria2, TUIC, Trojan, VMess+WS+TLS) can be exercised on the
# loopback. Intended for connectivity smoke tests only — never use these
# certs for production traffic.
set -e

CERT_DIR=/opt/zenithpanel/data/certs
mkdir -p "$CERT_DIR"

if [ ! -f "$CERT_DIR/fullchain.pem" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 7 \
    -subj "/CN=test.local" \
    -addext "subjectAltName=DNS:test.local,DNS:hysteria2.fanni-panda.com,IP:127.0.0.1" \
    -keyout "$CERT_DIR/privkey.pem" \
    -out  "$CERT_DIR/fullchain.pem"
  chmod 0600 "$CERT_DIR/privkey.pem"
fi

ls -la "$CERT_DIR"
echo "Cert fingerprint:"
openssl x509 -in "$CERT_DIR/fullchain.pem" -noout -fingerprint -sha256
