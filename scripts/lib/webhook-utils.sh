#!/usr/bin/env bash
# Copyright 2026 The kpt Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Common webhook utilities for certificate and configuration management.
# Provides functions for generating certs, injecting caBundle, and creating Secrets.

set -e

# generate_webhook_certs <output-dir> [service-name] [namespace]
# Generates self-signed TLS certificates for webhooks with proper DNS names.
generate_webhook_certs() {
  local CERT_DIR="${1:-.build/webhook-certs}"
  local SERVICE_NAME="${2:-porch-controllers}"
  local NAMESPACE="${3:-porch-system}"

  mkdir -p "$CERT_DIR"

  local CERT_FILE="$CERT_DIR/tls.crt"
  local KEY_FILE="$CERT_DIR/tls.key"

  # Skip if certificates already exist
  if [[ -f "$CERT_FILE" && -f "$KEY_FILE" ]]; then
    echo "✓ Webhook certificates already exist at $CERT_FILE"
    return 0
  fi

  echo "Generating webhook certificates for $SERVICE_NAME.$NAMESPACE.svc..."

  # Create certificate configuration
  local CSR_CONF="$CERT_DIR/csr.conf"
  cat > "$CSR_CONF" << EOF
[ req ]
default_bits = 4096
prompt = no
default_md = sha256
req_extensions = req_ext
distinguished_name = dn

[ dn ]
CN = ${SERVICE_NAME}.${NAMESPACE}.svc

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster
DNS.5 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local

[ v3_ext ]
authorityKeyIdentifier=keyid,issuer:always
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment,dataEncipherment
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=@alt_names
EOF

  # Generate CA
  local CA_KEY="$CERT_DIR/ca.key"
  local CA_CRT="$CERT_DIR/ca.crt"

  if [[ ! -f "$CA_CRT" || ! -f "$CA_KEY" ]]; then
    echo "  Generating CA certificate..."
    openssl genrsa -out "$CA_KEY" 4096 2>/dev/null
    openssl req -x509 -new -nodes -key "$CA_KEY" \
      -subj "/O=kpt.dev/CN=${SERVICE_NAME}-webhook-ca" \
      -days 365 -out "$CA_CRT" 2>/dev/null
  fi

  # Generate server certificate
  local SERVER_KEY="$CERT_DIR/server.key"
  local SERVER_CSR="$CERT_DIR/server.csr"

  if [[ ! -f "$CERT_FILE" ]]; then
    echo "  Generating server certificate..."
    openssl genrsa -out "$SERVER_KEY" 4096 2>/dev/null
    openssl req -new -key "$SERVER_KEY" -out "$SERVER_CSR" \
      -config "$CSR_CONF" 2>/dev/null

    openssl x509 -req -in "$SERVER_CSR" \
      -CA "$CA_CRT" -CAkey "$CA_KEY" -CAcreateserial \
      -out "$CERT_FILE" -days 365 \
      -extensions v3_ext -extfile "$CSR_CONF" 2>/dev/null
  fi

  # Use server certificate as the TLS key
  if [[ ! -f "$KEY_FILE" ]]; then
    echo "  Writing certificate files..."
    cp "$SERVER_KEY" "$KEY_FILE"
  fi

  # Verify generated files
  if [[ ! -f "$CERT_FILE" || ! -f "$KEY_FILE" ]]; then
    echo "✗ Failed to generate webhook certificates" >&2
    return 1
  fi

  echo "✓ Generated webhook certificates:"
  echo "  Certificate: $CERT_FILE"
  echo "  Key: $KEY_FILE"
}

# inject_webhook_cabundle <webhook-yaml> <certificate-file>
# Injects base64-encoded certificate into webhook configuration YAML.
inject_webhook_cabundle() {
  local WEBHOOK_YAML="$1"
  local CERT_FILE="$2"

  if [[ ! -f "$WEBHOOK_YAML" ]]; then
    echo "Error: Webhook YAML file not found: $WEBHOOK_YAML" >&2
    return 1
  fi

  if [[ ! -f "$CERT_FILE" ]]; then
    echo "Error: Certificate file not found: $CERT_FILE" >&2
    return 1
  fi

  # Read certificate and base64-encode it (single line, no wrapping).
  # Uses tr -d to strip newlines for portability across GNU and BSD base64.
  local CABUNDLE=$(base64 < "$CERT_FILE" | tr -d '\n')

  # Inject caBundle after clientConfig line using a temp file for portability
  # (avoids GNU vs BSD sed -i incompatibility).
  local TMPFILE="${WEBHOOK_YAML}.tmp"
  sed "s/^  clientConfig:$/  clientConfig:\n    caBundle: $CABUNDLE/" "$WEBHOOK_YAML" > "$TMPFILE" && mv "$TMPFILE" "$WEBHOOK_YAML"

  echo "✓ Injected caBundle into $WEBHOOK_YAML"
}

# create_webhook_certs_secret <cert-dir> [output-file]
# Creates Kubernetes Secret YAML containing webhook certificates.
# If output-file is specified, writes to file; otherwise prints to stdout.
create_webhook_certs_secret() {
  local CERT_DIR="$1"
  local OUTPUT_FILE="${2:-}"

  if [[ ! -f "$CERT_DIR/tls.crt" || ! -f "$CERT_DIR/tls.key" ]]; then
    echo "Error: Webhook certificates not found in $CERT_DIR" >&2
    return 1
  fi

  # Read and base64-encode certificates (portable across GNU and BSD).
  local CERT_DATA=$(base64 < "$CERT_DIR/tls.crt" | tr -d '\n')
  local KEY_DATA=$(base64 < "$CERT_DIR/tls.key" | tr -d '\n')

  # Generate Secret YAML
  local SECRET_YAML=$(cat << EOF
---
apiVersion: v1
kind: Secret
metadata:
  name: porch-controllers-webhook-tls
  namespace: porch-system
type: kubernetes.io/tls
data:
  tls.crt: $CERT_DATA
  tls.key: $KEY_DATA
EOF
)

  if [[ -n "$OUTPUT_FILE" ]]; then
    echo "$SECRET_YAML" > "$OUTPUT_FILE"
    echo "✓ Created Secret YAML: $OUTPUT_FILE"
  else
    echo "$SECRET_YAML"
  fi
}

# Main script execution when called directly (not sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  COMMAND="${1:-help}"
  case "$COMMAND" in
    generate-certs)
      generate_webhook_certs "$2" "$3" "$4"
      ;;
    inject-cabundle)
      inject_webhook_cabundle "$2" "$3"
      ;;
    create-secret)
      create_webhook_certs_secret "$2" "$3"
      ;;
    help|*)
      cat << EOF
Webhook utilities for certificate and configuration management.

Usage: $0 <command> [options]

Commands:
  generate-certs <dir> [service] [namespace]
    Generate self-signed webhook certificates
    Default service: porch-controllers, namespace: porch-system

  inject-cabundle <webhook-yaml> <cert-file>
    Inject certificate into webhook configuration YAML

  create-secret <cert-dir> [output-file]
    Create Secret YAML with webhook certificates
    If output-file omitted, prints to stdout

Examples:
  $0 generate-certs .build/webhook-certs
  $0 inject-cabundle deployment/webhook.yaml .build/webhook-certs/tls.crt
  $0 create-secret .build/webhook-certs > secret.yaml
EOF
      ;;
  esac
fi
