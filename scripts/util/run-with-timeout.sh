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

# Portable timeout wrapper that works on both Linux (GNU coreutils) and macOS.
# Usage: run-with-timeout.sh <seconds> <command> [args...]

set -euo pipefail

if [ $# -lt 2 ]; then
  echo "Usage: $0 <timeout_seconds> <command> [args...]" >&2
  exit 1
fi

TIMEOUT_SECS="$1"
shift

if command -v timeout &>/dev/null; then
  # GNU coreutils timeout (Linux, or macOS with coreutils installed)
  timeout --kill-after=10 "${TIMEOUT_SECS}" "$@"
else
  # POSIX fallback using process group for reliable subprocess cleanup.
  # Run the command in its own process group so we can kill the entire tree on timeout.
  set -m
  "$@" &
  pid=$!

  (sleep "${TIMEOUT_SECS}" && kill -- -"$pid" 2>/dev/null && echo "ERROR: command timed out after ${TIMEOUT_SECS}s" >&2) &
  watchdog=$!

  # Forward SIGINT/SIGTERM to the process group and clean up the watchdog.
  trap 'kill -- -"$pid" 2>/dev/null; kill "$watchdog" 2>/dev/null; exit 143' INT TERM

  if wait "$pid"; then
    kill "$watchdog" 2>/dev/null || true
    wait "$watchdog" 2>/dev/null || true
    exit 0
  else
    status=$?
    kill -- -"$pid" 2>/dev/null || true
    kill "$watchdog" 2>/dev/null || true
    wait "$watchdog" 2>/dev/null || true
    exit $status
  fi
fi
