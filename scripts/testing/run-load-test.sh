#!/usr/bin/env bash

#  Copyright 2025 The kpt Authors
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

# Source common configuration
source "$(dirname "$0")/common.sh"

script_name=$(basename "$0")

git_repo_server=""
git_repo_count=0
package_count=0
package_revision_count=0
parallelism=1
result_file=load_test_results.txt
repo_result_file=load_test_repo_results.csv
log_file=load_test.log
dirty_mode=false

usage()
{
    echo ""
    echo "$script_name - runs a load test on porch"
    echo ""
    echo "       usage:  $script_name [-options]"
    echo ""
    echo "       options"
    echo "         -h                        - this help message"
    echo "         -s hostname               - the host name of the git server for porch git repos"
    echo "         -r repo-count             - the number of repos to create during the test, a positive integer"
    echo "         -p package-count          - the number of packages to create in each repo during the test, a positive integer"
    echo "         -e package-revision-count - the number of packagerevisions to create on each package during the test, a positive integer"
    echo "         -f result-file            - the file where the raw results will be stored, defaults to $result_file"
    echo "         -o repo-result-file       - the file where the results by reop will be stored, defaults to $repo_result_file"
    echo "         -l log-file               - the file where the test log will be stored, defaults to $log_file"
    echo "         -y                        - dirty mode, do not clean up after tests"
    echo "         -t parallelism            - number of parallel package creation threads per repo, defaults to 1"
    echo ""
    exit 255;
}

while getopts "hs:r:p:e:f:o:l:yt:" opt
do
    case $opt in
    h) usage ;;
    s) git_repo_server=$OPTARG ;;
    r) git_repo_count=$OPTARG ;;
    p) package_count=$OPTARG ;;
    e) package_revision_count=$OPTARG ;;
    f) result_file=$OPTARG ;;
    o) repo_result_file=$OPTARG ;;
    l) log_file=$OPTARG ;;
    y) dirty_mode=true ;;
    t) parallelism=$OPTARG ;;
    \?) usage ;;
    esac
done

if [[ "$parallelism" -lt 1 ]]
then
    echo "parallelism must be a positive integer on the -t flag"
    exit 1
fi

if [[ -z "$git_repo_server" ]]
then
    echo "git server for porch git repos not specified with the -s flag"
    exit 1
fi

if [[ "$git_repo_count" -lt 1 ]]
then
    echo "git repo count must be specified as a positive integer on the -r flag"
    exit 1
fi

if [[ "$package_count" -lt 1 ]]
then
    echo "package count must be specified as a positive integer on the -p flag"
    exit 1
fi

if [[ "$package_revision_count" -lt 1 ]]
then
    echo "package revision count must be specified as a positive integer on the -e flag"
    exit 1
fi

if [ -z "$result_file" ]
then
    echo "result file not specified on -f flag"
    exit 1
fi

if [ -z "$repo_result_file" ]
then
    echo "repo result file not specified on -o flag"
    exit 1
fi

if [ -z "$log_file" ]
then
    echo "log file not specified on -l flag"
    exit 1
fi

echo "  up to $parallelism packages will be created in parallel per repo" 
echo "running load test towards git server http://porch:secret@$git_repo_server:3000/porch/" 
echo "  $git_repo_count repos will be created"
echo "  $package_count packages in each repo"
echo "  $package_revision_count package revisions in each package" 
echo "  results will be stored in \"$result_file\""
echo "  repo results will be stored in \"$repo_result_file\""
echo "  the log will be stored in \"$log_file\""

update_package_resources() {
    cat << EOF > "$1/Kptfile"
apiVersion: kpt.dev/v1
kind: Kptfile

metadata:
  name: "$1"
  annotations:
    config.kubernetes.io/local-config: "true"

info:
  description: network function "$1" blueprint

pipeline:
  mutators:
    - image: ${PORCH_GHCR_PREFIX_URL}/apply-replacements:v0.1.5
      configPath: apply-replacements-annotation1.yaml
    - image: ${PORCH_GHCR_PREFIX_URL}/apply-replacements:v0.1.5
      configPath: apply-replacements-annotation2.yaml
EOF

    cat << EOF > "$1/deployment.yaml"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: "$1"
  annotations:
    annotation1: no-value-1
    annotation2: no-value-2
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: "$1"
  template:
    metadata:
      labels:
        app.kubernetes.io/name: "$1"
    spec:
      containers:
      - name: nginx
        image: nginx:latest
EOF

    cat << EOF > "$1/cm_deployment.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-deployment
  annotations:
    config.kubernetes.io/local-config: "true"
data:
  annotation1: local-annotation-1
  annotation2: local-annotation-2
EOF

    cat << EOF > "$1/apply-replacements-annotation1.yaml"
apiVersion: fn.kpt.dev/v1alpha1
kind: ApplyReplacements
metadata:
  name: replace-annotation1
  annotations:
    config.kubernetes.io/local-config: "true"
replacements:
- source:
    kind: ConfigMap
    name: cm-deployment
    fieldPath: data.annotation1
  targets:
  - select:
      kind: Deployment
    fieldPaths:
    - metadata.annotations.annotation1
EOF

    cat << EOF > "$1/apply-replacements-annotation2.yaml"
apiVersion: fn.kpt.dev/v1alpha1
kind: ApplyReplacements
metadata:
  name: replace-annotation1
  annotations:
    config.kubernetes.io/local-config: "true"
replacements:
- source:
    kind: ConfigMap
    name: cm-deployment
    fieldPath: data.annotation2
  targets:
  - select:
      kind: Deployment
    fieldPaths:
    - metadata.annotations.annotation2
EOF
}

create_package_revision() {
    LOAD_TEST_TMP_DIR=$(mktemp -d)
    pushd "$LOAD_TEST_TMP_DIR" || return

    porchctl -n porch-scale rpkg pull "$1" "$1"
    update_package_resources "$1"
    porchctl -n porch-scale rpkg push "$1" "$1"
    porchctl -n porch-scale rpkg propose "$1"
    porchctl -n porch-scale rpkg approve "$1"

    popd || return

    if ! $dirty_mode
    then
        rm -fr "$LOAD_TEST_TMP_DIR"
    fi
}
wait_for_jobs() {
    local max_jobs=$1
    while (( $(jobs -rp | wc -l) >= max_jobs )); do
        sleep 0.1
    done
}

create_package () {
    start_prv1=$EPOCHREALTIME
    pkg_v1=$(porchctl -n porch-scale rpkg init "network-function-$2" --repository="$1" --workspace=v1 | cut -f1 -d' ' )  
    echo "$1:$2:1 $pkg_v1 initialized" >> "$result_file"

    create_package_revision "$pkg_v1"
    echo "$(date "+%Y:%m:%d %T") $1:$2:1 $pkg_v1 approved, took $(echo $EPOCHREALTIME-$start_prv1 | bc -l) seconds" >> "$result_file"

    for k in $(seq 2 "$package_revision_count")
    do
        start_prvx=$EPOCHREALTIME

        pkg_vx=$(porchctl rpkg copy -n porch-scale "$pkg_v1"  "--workspace=v$k" | cut -f1 -d' ' )
        echo "$1:$2:$k $pkg_vx copied" >> "$result_file"
        create_package_revision "$pkg_vx"
        echo "$(date "+%Y:%m:%d %T") $1:$2:$k $pkg_vx approved, took $(echo $EPOCHREALTIME-$start_prvx | bc -l) seconds"  >> "$result_file"
    done
}

create_repo () {
    git_repo_name="porch-scale-test-$1"

    echo "creating repo $git_repo_name . . ."
    {
        curl -k -H "content-type: application/json" "http://porch:secret@$git_repo_server:3000/api/v1/user/repos" --data '{"name":"'"$git_repo_name"'"}' >> "$log_file" 2>&1

        REPO_TMP_DIR=$(mktemp -d)
        pushd "$REPO_TMP_DIR" || return
        git clone "http://porch:secret@$git_repo_server:3000/porch/$git_repo_name.git"

        pushd "$git_repo_name" || return
        git switch -c  main
        touch README.md
        git add README.md
        git config user.name porch
        git commit -m "first commit"
        git push -u origin main
        popd || return

        popd || return

        if ! $dirty_mode
        then
            rm -fr "$REPO_TMP_DIR"
        fi
    } >> "$log_file" 2>&1

    kubectl apply -f - <<EOF >> "$log_file" 2>&1
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository

metadata:
   name: "$git_repo_name"
   namespace: porch-scale

spec:
    description: "$git_repo_name"
    content: Package
    deployment: false
    type: git
    git:
        repo: "http://$git_repo_server:3000/porch/$git_repo_name.git"
        directory: /
        branch: main
        createBranch: true
        secretRef:
            name: gitea
EOF

    echo "created repo $git_repo_name"

    # Run create_package in parallel based on $parallelism if it is greater than 1
    running_jobs=0
    for j in $(seq 1 "$package_count")
    do 
        create_package "$git_repo_name" "$j" >> "$log_file" 2>&1 &

        ((running_jobs+=1))
        if (( running_jobs >= parallelism )); then
            wait -n  # wait for any background job to finish before starting a new one
            ((running_jobs-=1))
        fi
    done

    wait  # wait for all background jobs to complete
}

create_namespace_secret() {
    kubectl apply -f - <<EOF >> "$log_file" 2>&1
apiVersion: v1
kind: Namespace
metadata:
  name: porch-scale

---
apiVersion: v1
kind: Secret
metadata:
  name: gitea
  namespace: porch-scale
type: kubernetes.io/basic-auth
data:
  username: cG9yY2g=
  password: c2VjcmV0
EOF
}

delete_package_revisions () {
    for pkg in $(porchctl rpkg get -n porch-scale | grep -v 'WORKSPACENAME' | cut -f1 -d' ')
    do
        start_prvd=$EPOCHREALTIME
        porchctl -n porch-scale rpkg propose-delete "$pkg" >> "$log_file" 2>&1
        porchctl -n porch-scale rpkg delete "$pkg" >> "$log_file" 2>&1
        echo "$(date "+%Y:%m:%d %T") $pkg deleted, took $(echo $EPOCHREALTIME-$start_prvd | bc -l) seconds"
    done
}

delete_repo () {
    git_repo_name="porch-scale-test-$1"

    echo "deleting repo $git_repo_name . . ."
    curl -k -H "content-type: application/json" --request DELETE "http://porch:secret@$git_repo_server:3000/api/v1/repos/porch/$git_repo_name" >> "$log_file" 2>&1
    kubectl delete -n porch-scale repositories.config.porch.kpt.dev "$git_repo_name" >> "$log_file" 2>&1
    echo "deleted repo $git_repo_name"
}

split_repo_results() {
    SPLIT_REPO_TMP_DIR=$(mktemp -d)
    CURRENT_DIR=$(pwd)

    grep "approved, took" "$result_file" | awk '{printf("%s,%s,%s\n", $2, $3, $7)}' > "$SPLIT_REPO_TMP_DIR/approved.csv"
    grep "deleted, took" "$result_file" | awk '{printf("%s,%s,%s\n", $2, $3, $6)}' > "$SPLIT_REPO_TMP_DIR/deleted.csv"

    cd "$SPLIT_REPO_TMP_DIR" || exit

    for i in $(seq 1 "$git_repo_count")
    do
        repo_suffix=$(printf "%0${#git_repo_count}d" "$i")
        echo "REPO-$repo_suffix-TIME,REPO-$repo_suffix-TEST,REPO-$repo_suffix-DURATION" > "repo_$repo_suffix.csv"
        grep ",porch-scale-test-$i\:" approved.csv | sed "s/,porch-scale-test-$i:/,/" >> "repo_$repo_suffix.csv"
        grep ",porch-scale-test-$i-" deleted.csv | sed "s/,porch-scale-test-$i-/,del-/" >> "repo_$repo_suffix.csv"
    done

    paste -d',' ./repo_*.csv

    cd "$CURRENT_DIR" || exit
    if ! $dirty_mode
    then
        rm -fr "$SPLIT_REPO_TMP_DIR"
    fi
}

rm -fr "$result_file" "$repo_result_file" "$log_file"

create_namespace_secret

for i in $(seq 1 "$git_repo_count")
do 
    create_repo "$i" >> "$result_file"
done

if ! $dirty_mode
then
    delete_package_revisions >> "$result_file"

    for i in $(seq 1 "$git_repo_count")
    do 
        delete_repo "$i" >> "$result_file"
    done

    kubectl delete ns porch-scale >> "$log_file" 2>&1
fi

split_repo_results > "$repo_result_file"

echo "load test towards git server http://porch:secret@$git_repo_server:3000/porch/ completed" 
