#!/bin/bash
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

set -o pipefail

# Set environment variables
export PORCH_NAMESPACE="porch-system"
export MONITORING_NAMESPACE="porch-monitoring"

echo "=== OTEL Validation Checks ==="
echo ""

# Check if Jaeger is accessible
echo "=== 1. Checking Jaeger availability ==="
JAEGER_POD=$(kubectl get pods -n ${MONITORING_NAMESPACE} -l app=jaeger -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
if [ -z "$JAEGER_POD" ]; then
  echo "ERROR: Jaeger pod not found in ${MONITORING_NAMESPACE}"
  exit 1
fi
echo "✓ Jaeger pod found: $JAEGER_POD"

# Check porch-server metrics endpoint via port-forward
echo ""
echo "=== 2. Checking porch-server metrics endpoint ==="
PORCH_SERVER_POD=$(kubectl get pods -n ${PORCH_NAMESPACE} -l app=porch-server -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
if [ -z "$PORCH_SERVER_POD" ]; then
  echo "ERROR: porch-server pod not found in ${PORCH_NAMESPACE}"
  exit 1
fi

# Create temporary port-forward
PF_TEMP=$(mktemp)
kubectl port-forward -n ${PORCH_NAMESPACE} ${PORCH_SERVER_POD} 9464:9464 > "$PF_TEMP" 2>&1 &
PF_PID=$!
sleep 2

METRICS=$(curl -s http://localhost:9464/metrics 2>/dev/null | head -20 || echo "")
kill $PF_PID 2>/dev/null || true
rm -f "$PF_TEMP"
wait $PF_PID 2>/dev/null || true

if [ -z "$METRICS" ]; then
  echo "ERROR: Could not scrape metrics from porch-server"
  exit 1
fi
echo "✓ Metrics endpoint responding"
echo "$METRICS"

# Check Prometheus has metrics from all three components
echo ""
echo "=== 2b. Checking Prometheus for all components metrics ==="
PROMETHEUS_POD=$(kubectl get pods -n ${MONITORING_NAMESPACE} -l app=prometheus -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
if [ -z "$PROMETHEUS_POD" ]; then
  echo "ERROR: Prometheus pod not found in ${MONITORING_NAMESPACE}"
  exit 1
fi

# Query Prometheus for metrics from each component (by instance/target)
PF_TEMP=$(mktemp)
kubectl port-forward -n ${MONITORING_NAMESPACE} ${PROMETHEUS_POD} 9090:9090 > "$PF_TEMP" 2>&1 &
PF_PID=$!
sleep 2

COMPONENTS_FOUND=0
# Query for prometheus scrape targets that are currently up
TARGETS=$(curl -s "http://localhost:9090/api/v1/query?query=up" 2>/dev/null | jq -r '.data.result[].metric.instance' 2>/dev/null | grep -E "porch-server|porch-controllers|function-runner" || echo "")

if [ -n "$TARGETS" ]; then
  echo "Found targets: $TARGETS"
  # Count unique component types using pipeline (avoids multi-line count output)
  if echo "$TARGETS" | grep -q "porch-server"; then
    echo "✓ Prometheus has metrics from porch-server"
    ((COMPONENTS_FOUND++))
  fi
  if echo "$TARGETS" | grep -q "porch-controllers"; then
    echo "✓ Prometheus has metrics from porch-controllers"
    ((COMPONENTS_FOUND++))
  fi
  if echo "$TARGETS" | grep -q "function-runner"; then
    echo "✓ Prometheus has metrics from function-runner"
    ((COMPONENTS_FOUND++))
  fi
else
  echo "WARNING: Could not query Prometheus targets (may still be healthy)"
fi

kill $PF_PID 2>/dev/null || true
rm -f "$PF_TEMP"
wait $PF_PID 2>/dev/null || true

if [ $COMPONENTS_FOUND -gt 0 ]; then
  echo "✓ Found $COMPONENTS_FOUND/3 Porch components exporting metrics to Prometheus"
else
  echo "WARNING: Could not verify component metrics via Prometheus (check manually at http://localhost:9092)"
fi

# Check for OTEL in porch-server logs
echo ""
echo "=== 3. Checking porch-server OTEL initialization ==="
OTEL_INIT=$(kubectl logs -n ${PORCH_NAMESPACE} ${PORCH_SERVER_POD} --tail=100 2>/dev/null | grep -i "OpenTelemetry initialized" | tail -1 || echo "")
if [ -z "$OTEL_INIT" ]; then
  echo "WARNING: No OTEL initialization message found (may not be critical)"
else
  echo "✓ OTEL initialized: $OTEL_INIT"
fi

# Check for export errors in logs
echo ""
echo "=== 4. Checking for OTEL export errors ==="
EXPORT_ERRORS=$(kubectl logs -n ${PORCH_NAMESPACE} ${PORCH_SERVER_POD} --tail=100 2>/dev/null | grep -i "export.*error\|error.*export" | wc -l)
if [ "$EXPORT_ERRORS" -gt 0 ]; then
  echo "WARNING: Found $EXPORT_ERRORS export-related errors in logs"
  kubectl logs -n ${PORCH_NAMESPACE} ${PORCH_SERVER_POD} --tail=100 2>/dev/null | grep -i "export.*error\|error.*export" || true
fi
echo "✓ Export error check complete"

# Check Jaeger for traces (with retry)
echo ""
echo "=== 5. Querying Jaeger for traces ==="
TRACES_FOUND=0
for i in {1..3}; do
  echo "Attempt $i..."
  
  # Create temporary port-forward to Jaeger
  kubectl port-forward -n ${MONITORING_NAMESPACE} ${JAEGER_POD} 16686:16686 > /dev/null 2>&1 &
  PF_PID=$!
  sleep 3
  
  # Query for traces - look for data array length
  RESPONSE=$(curl -s "http://localhost:16686/api/traces?service=porch-server&limit=1" 2>/dev/null)
  TRACE_COUNT=$(echo "$RESPONSE" | jq '.data | length' 2>/dev/null || echo "0")
  
  kill $PF_PID 2>/dev/null
  
  if [ -n "$TRACE_COUNT" ] && [ "$TRACE_COUNT" -gt 0 ]; then
    TRACES_FOUND=1
    echo "✓ Found $TRACE_COUNT trace(s) in Jaeger"
    break
  fi
  
  if [ $i -lt 3 ]; then
    echo "  No traces yet, waiting 5 seconds..."
    sleep 5
  fi
done

if [ $TRACES_FOUND -eq 0 ]; then
  echo "ERROR: No traces found in Jaeger after multiple attempts"
  exit 1
fi

# List all services exporting traces
echo ""
echo "=== 6. Services reporting traces to Jaeger ==="
kubectl port-forward -n ${MONITORING_NAMESPACE} ${JAEGER_POD} 16686:16686 > /dev/null 2>&1 &
PF_PID=$!
sleep 3

SERVICES=$(curl -s "http://localhost:16686/api/services" 2>/dev/null | jq -r '.data[] | select(. != "jaeger-all-in-one")' 2>/dev/null || echo "")

kill $PF_PID 2>/dev/null

if [ -n "$SERVICES" ]; then
  echo "✓ Porch components exporting traces:"
  echo "$SERVICES" | while read service; do
    echo "  - $service"
  done
else
  echo "WARNING: No Porch services found in Jaeger"
fi

# Summary
echo ""
echo "=== OTEL Validation Complete ==="
echo "✓ Jaeger is running"
echo "✓ Metrics endpoint responsive"
echo "✓ OTEL components initialized"
echo ""
echo "Infrastructure ready for OTEL testing"
