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

self_dir="$(dirname "$(readlink -f "$0")")"

data_cluster_kubeconfig_file="$self_dir/kubeconfigs/data_cluster.conf"
dbcache_kubeconfig_file="$self_dir/kubeconfigs/porch_dbcache.conf"

export logs_dir='/tmp/disaster-test-logs_'$$
mkdir -p "$logs_dir"
trap 'cleanUpLogs' EXIT
function cleanUpLogs() {
    h1 "Clean up log pipes..."
    rm -frv "$logs_dir"
    pkill -ef 'sed.*\|>'
}

function h1() {
  MESSAGE=" $* "
  TERMINAL_WIDTH=$(tput -T xterm cols)
  STAR_COUNT=$((((TERMINAL_WIDTH - ${#MESSAGE}) / 2) - 2))
  STAR_SEGMENT="$(head -c "$STAR_COUNT" < /dev/zero | tr "\0" "-")"
  MESSAGE_LINE="$(tput setaf 4)$STAR_SEGMENT<$(tput sgr0)$MESSAGE$(tput setaf 4)>$STAR_SEGMENT$(tput sgr0)"
  echo
  echo -e "$MESSAGE_LINE"
  echo
}

function h2() {
  MESSAGE=" $* "
  MESSAGE_LINE="$(tput setaf 4)===\u200B==>>$(tput sgr0) $MESSAGE"
  echo
  echo -e "$MESSAGE_LINE"
}

function prefixLogs() {
    while [[ -n "${1-DONE}" ]] ; do
        XCASE=$( tr "[:upper:]" "[:lower:]" <<< "${1-DONE}" )
        case $XCASE in
            --prefix)
                shift
                prefix_string="$1"
                ;;
            --colour|--color)
                shift
                colour="$1"
                ;;
            *)
                break
                ;;
        esac
        shift
    done

    tmp_logfile="$logs_dir"'/'"${prefix_string// /_}"'_'$$'.tmp'
    mknod "$tmp_logfile" p
    sed -e 's/^\(.\)/'"$(tput setaf "$colour")$prefix_string"'|>'"$(tput sgr0)"' \1/' --unbuffered <"$tmp_logfile" &
    exec &> "$tmp_logfile"
}

function kubectl_data() {
    kubectl --kubeconfig "$data_cluster_kubeconfig_file" "$@"
}
function kubectl_dbcache() {
    kubectl --kubeconfig "$dbcache_kubeconfig_file" "$@"
}
function porchctl_dbcache() {
    porchctl --kubeconfig "$dbcache_kubeconfig_file" -n default "$@"
}