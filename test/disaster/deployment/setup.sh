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

# Stricter error handling:
#   - Exit on error
#   - Require predefined variables
#   - Check errors in piped commands
set -euo pipefail

PORCH_DIR="$(git rev-parse --show-toplevel)"
self_dir="$(dirname "$(readlink -f "$0")")"

source "$self_dir/../util/functions.sh"

CATALOG_REPO='https://github.com/nephio-project/catalog.git'
KPT_REPO='https://github.com/kptdev/kpt.git'
cd "$PORCH_DIR"

export cleandown=${CLEANDOWN:-true}
export skip_package_variant_sets=${SKIP_PACKAGE_VARIANT_SETS:-true}
export skip_package_variants=${SKIP_PACKAGE_VARIANTS:-true}
export SKIP_IMG_BUILD="false"

data_kind_cluster="porch-disaster-test-data"
dbcache_kind_cluster="porch-disaster-test-dbcache"

function install() {

    if [[ "${CLEANDOWN:-true}" == "true" ]]; then
        h1 "Clean down before install"
        kind delete cluster --name "$dbcache_kind_cluster" || true
        kind delete cluster --name "$data_kind_cluster" || true
    fi

    {
        if [[ $SKIP_IMG_BUILD == "false" ]]; then
            prefixLogs --prefix "building images" --colour 7

            make build-images IMAGE_REPO='porch-kind' IMAGE_TAG='test'
        fi
    } &

    {
        prefixLogs --prefix "creating data cluster" --colour 6

        h1 "Create data cluster"
        kind create cluster --name "$data_kind_cluster" --kubeconfig "$data_cluster_kubeconfig_file" || kind export kubeconfig --name "$data_kind_cluster" --kubeconfig "$data_cluster_kubeconfig_file"

        h1 "Install MetalLB on data cluster"
        kubectl_data apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.9/config/manifests/metallb-native.yaml
        sleep 1
        echo "Waiting for controller to become ready..."
        kubectl_data wait --namespace metallb-system deploy controller \
                        --for=condition=available \
                        --timeout=90s
        # Generate MetalLB config dynamically from the kind Docker network subnet
        source "${PORCH_DIR}/scripts/get-kind-metallb-subnet.sh"
        KIND_DOCKER_NETWORK="kind" generate_metallb_config | kubectl_data apply -f -
    } &

    {
        prefixLogs --prefix "creating DB cache cluster" --colour 3

        h1 "Create DB cache cluster"
        kind create cluster --name "$dbcache_kind_cluster" --kubeconfig "$dbcache_kubeconfig_file" || kind export kubeconfig --name "$dbcache_kind_cluster" --kubeconfig "$dbcache_kubeconfig_file"
    } &
    wait

    deploy_config_dir="${PORCH_DIR}/.build/disaster-test"
    rm -rf "$deploy_config_dir" || true
    mkdir -p "$deploy_config_dir"

    {
        prefixLogs --prefix "installing Gitea" --colour 6

        h1 "Install Gitea on data cluster"
        KUBECONFIG="$data_cluster_kubeconfig_file" "${PORCH_DIR}"/scripts/install-dev-gitea-setup.sh "disaster"
    } &

    PORCH_SERVER_MEMORY="${PORCH_SERVER_MEMORY:-4Gi}"
    {
        prefixLogs --prefix "installing Porch with DB cache" --colour 3

        h1 "Install Postgres on data cluster"
        h2 "Preparing deployment config for Porch with DB cache"
        make load-images-to-kind deployment-config PORCH_CACHE_TYPE='DB' SKIP_IMG_BUILD='true' IMAGE_REPO='porch-kind' IMAGE_TAG='test' KIND_CONTEXT_NAME="$dbcache_kind_cluster" DEPLOYPORCHCONFIGDIR="$deploy_config_dir/dbcache" KUBECONFIG="$dbcache_kubeconfig_file"

        h2 "Extracting Postgres deployment config for standalone DB"
        postgres_dir="$deploy_config_dir/external-postgres"
        mkdir "$postgres_dir"
        kpt pkg init "$postgres_dir"

        mv "$deploy_config_dir"/dbcache/*porch-postgres*.yaml "$postgres_dir"
        cp "$deploy_config_dir"/dbcache/*namespace*.yaml "$postgres_dir"
        kpt fn eval "$postgres_dir" \
        --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
        --match-kind Namespace --match-name porch-fn-system \
        -- source='ctx.resource_list["items"] = []'

        h2 "Deploying Postgres"
        make KUBECONFIG="$data_cluster_kubeconfig_file" DEPLOYPORCHCONFIGDIR="$postgres_dir" deploy-current-config
        db_host_ip=$(\
            kubectl_data --namespace porch-system get service porch-postgresql-lb -o custom-columns='IP:.status.loadBalancer.ingress[0].ip' --no-headers \
        ) || { echo "error getting DB host IP from data cluster"; exit 1; }

        h1 "Install Porch with DB cache (with no in-cluster DB)"
        h2 "Setting DB cache to use Postgres on data cluster (at IP $db_host_ip)"
        cp "$self_dir/postgres-config.yaml" "$deploy_config_dir/dbcache"
        sed -i -e 's/\(  DB_HOST: \).*/\1"'"$db_host_ip"'"/' "$deploy_config_dir/dbcache/postgres-config.yaml" && echo "Set"

        h2 "Increasing porch-server memory to $PORCH_SERVER_MEMORY"
        kpt fn eval "$deploy_config_dir/dbcache/" \
        --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
        --match-kind Deployment --match-name porch-server --match-namespace porch-system \
        -- source='for resource in ctx.resource_list["items"]:
            containers = resource.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
            for container in containers:
                if container["name"] == "porch-server":
                    container["resources"]["limits"]["memory"] = "'"$PORCH_SERVER_MEMORY"'"' \
        && echo "Done"

        h2 "Deploying..."
        make deploy-current-config IMAGE_TAG='test' KIND_CONTEXT_NAME="$dbcache_kind_cluster" DEPLOYPORCHCONFIGDIR="$deploy_config_dir/dbcache" KUBECONFIG="$dbcache_kubeconfig_file"
    } &
    wait
}

function resetTestWorkload() {
    if [[ "${CLEANDOWN:-true}" == "true" ]]; then

        h1 "Cleaning down..."

        (
            prefixLogs --prefix "cleaning down DB cache cluster" --colour 3

            if [[ $skip_package_variant_sets != "true" ]]; then
                h2 "Deleting package variant sets"
                kubectl_dbcache delete packagevariantsets --all
            fi
            if [[ $skip_package_variants != "true" ]]; then
                h2 "Deleting package variants"
                kubectl_dbcache delete packagevariants --all
            fi

            h2 "Deleting Porch repositories"
            kubectl_dbcache delete repositories --all

            h2 "Deleting Git repositories"
            kubectl_data --namespace gitea delete pod --selector app=gitea --force --grace-period 0

            h2 "Wiping and resetting Postgres"
            { kubectl_data -n porch-system delete pvc data-porch-postgresql-0 --force --grace-period 0 & }
            kubectl_data -n porch-system patch pvc data-porch-postgresql-0 -p '{"metadata":{"finalizers":null}}' --type merge
            kubectl_data -n porch-system delete pod porch-postgresql-0 --force --grace-period 0

            h2 "Waiting for Gitea and Postgres to become ready again"
            kubectl_data --namespace gitea wait --for condition=available --timeout 5m deployment/gitea
            kubectl_data --namespace porch-system wait --for condition=Ready --timeout 5m pod porch-postgresql-0
        )
    fi

    h1 "Load Git with test packages from Nephio catalog and Kpt examples"
    [[ -d "$self_dir/catalog" ]] || {
        git clone "$CATALOG_REPO" "$self_dir/catalog"
    } &
    [[ -d "$self_dir/kpt" ]] || {
        git clone "$KPT_REPO" "$self_dir/kpt"
        cd "$self_dir/kpt" && {
            find "$self_dir/kpt" -mindepth 1 -maxdepth 1 -type d ! \( -name ".git" -o -name "e2e" -o -name "internal" -o -name "package-examples" -o -name "rollouts" \) -print0 | xargs -0 git rm -r
            git repack -a -d -f --depth=250 --window=250
            git commit  -m "trim down repo file size for Porch disaster tests"
        }
    } &
    wait

    gitea_ip="$(\
        kubectl_data --namespace gitea get service gitea-lb --output jsonpath='{.status.loadBalancer.ingress[0].ip}' \
    )" || { echo "Error getting Gitea IP from data cluster"; exit 1; }

    {
        for repo in "catalog" "kpt"; do
            {
                prefixLogs --prefix 'pushing "'"$repo"' copies' --colour 6

                cd "$self_dir/$repo"
                git config http.postBuffer 524288000
                git config protocol.version 2
                for copy in {1..6}; do
                    h2 "Pushing http://porch:secret@$gitea_ip:3000/porch/$repo-load-$copy"
                    git remote set-url origin "http://porch:secret@$gitea_ip:3000/porch/$repo-load-$copy"
                    git push origin main
                done
            } &
        done
        wait
    } &

    {
        prefixLogs --prefix 'pushing edge1 repo copies' --colour 6

        mkdir "$self_dir"/edge1 && cd "$self_dir"/edge1 && {
            touch README.md
            git init; git checkout -b main; git config user.name porch
            git add README.md; git commit -m "first commit";
            
            git remote add origin http://porch:secret@"$gitea_ip":3000/porch/edge1.git; git push -u origin main

            if [[ $skip_package_variant_sets != "true" ]]; then
                for copy in {1..10}; do
                    h2 "Pushing http://porch:secret@$gitea_ip:3000/porch/variantset-edge-$copy"
                    git remote set-url origin "http://porch:secret@$gitea_ip:3000/porch/variantset-edge-$copy"
                    git push origin main
                done
            fi

            cd "$self_dir"
        } && rm -rf edge1/
    } &
    wait

    sed -i -e 's/\[GITEA_PLACEHOLDER\]/porch:secret@'"$gitea_ip"':3000/' "$self_dir"/load-repositories/*.yaml

    prefixLogs --prefix 'loading Porch-with-DB-cache' --colour 3

    kubectl_dbcache apply -f "$self_dir/load-repositories/edge1-repository.yaml"

    if [[ $skip_package_variant_sets != "true" ]]; then
        h2 "Creating variant-set repositories from $self_dir/load-repositories/variantset-repositories.yaml"
        kubectl_dbcache apply -f "$self_dir/load-repositories/variantset-repositories.yaml"
        h2 "Waiting for all repositories to have condition Ready==True"
        kubectl_dbcache wait repositories --all-namespaces --all --for 'condition=Ready=True' --timeout 10m

        h2 "Creating package variant set from $self_dir/load-variantset.yaml"
        kubectl_dbcache apply -f "$self_dir/load-variantset.yaml"
        h2 "Waiting for package variant set to have condition Ready==True"
        kubectl_dbcache wait packagevariantsets --all --all-namespaces --for 'condition=Ready=True' --timeout 5m
    fi

    if [[ $skip_package_variants != "true" ]]; then
        h2 "Creating package variants from $self_dir/load-variants.yaml"
        kubectl_dbcache apply -f "$self_dir/load-variants.yaml"
    fi

    h1 "Create Porch repositories for workload"
    for file in "$self_dir"/load-repositories/*batch*.yaml; do
        h2 "Creating repositories in batch $file"
        kubectl_dbcache apply -f "$file"
        h2 "Waiting for all repositories to have condition Ready==True"
        kubectl_dbcache wait repositories --all-namespaces --all --for 'condition=Ready=True' --timeout 20m
    done

    if [[ $skip_package_variant_sets != "true" || $skip_package_variants != "true" ]]; then

        h2 "Waiting for all package variants to have condition Ready==True"
        kubectl_dbcache wait packagevariants --all --all-namespaces --for 'condition=Ready=True' --timeout 10m
    fi


    h1 "Create some new package revisions in a selection of lifecycle states"

    package_revisions="$(\
        kubectl_dbcache get packagerevisions -o name --no-headers | awk -F '/' '{print $2}' \
    )" || { echo "Error getting package revisions to clone"; exit 1; }

    to_clone="$(grep -E '(catalog|kpt).*-[12]' <<< "$package_revisions" | grep -E '^.*-1\.' | sed -n '1,20p')"
    to_edit="$(grep -E '(catalog|kpt).*-[12]' <<< "$package_revisions" | grep -E '^.*-2\.' | sed -n '1,20p')"


    h2 "Cloning $(wc -l <<< "$to_clone") package revisions"
    cloned_count=0
    for rev in $to_clone; do
        echo
        cloned_count=$(( cloned_count + 1 ))
        echo "Cloning $rev"
        porchctl_dbcache rpkg clone "$rev" "clone-$cloned_count" --repository edge1
    done

    h2 "Editing $(wc -l <<< "$to_edit") package revisions"
    edited_count=0
    for rev in $to_edit; do
        echo
        edited_count=$(( edited_count + 1 ))
        porchctl_dbcache rpkg clone "$rev" "clone-to-edit-$edited_count" --repository edge1
    done
    to_edit="$(\
        kubectl_dbcache get packagerevisions -o name --no-headers | awk -F '/' '{print $2}' | grep -E 'clone-to-edit' \
    )" || { echo "Error getting package revisions to edit"; exit 1; }
    edited_count=0
    for rev in $to_edit; do
        echo
        porchctl_dbcache rpkg propose "$rev"
        porchctl_dbcache rpkg approve "$rev"
        edited_count=$(( edited_count + 1 ))
        echo "Editing $rev"
        porchctl_dbcache rpkg edit "$rev" --workspace "edit-$edited_count"
    done

    package_revisions="$(\
        kubectl_dbcache get packagerevisions -o name --no-headers | awk -F '/' '{print $2}' \
    )" || { echo "Error getting package revisions to perform lifecycle operations"; exit 1; }
    cloned="$(grep -E "clone-" <<< "$package_revisions")" || { echo "Error picking out cloned package revisions"; exit 1; }
    edited="$(grep -E "\.edit-" <<< "$package_revisions")" || { echo "Error picking out edited package revisions"; exit 1; }


    if [[ $skip_package_variant_sets != "true" ]]; then
        variantset_created="$(grep -E 'variantset' <<< "$package_revisions")" || { echo "Error picking out package revisions created from variant-set"; exit 1; }
    else
        variantset_created=""
    fi
    if [[ $skip_package_variants != "true" ]]; then
        variant_created="$(grep -E '\-variant\.' <<< "$package_revisions")" || { echo "Error picking out package revisions created from variants"; exit 1; }
    else
        variant_created=""
    fi


    to_propose="$(sed -n '6,20p' <<< "$cloned")"$'\n'"$(sed -n '6,20p' <<< "$edited")"$'\n'"$(sed -n '6,20p' <<< "$variantset_created")"$'\n'"$(sed -n '6,20p' <<< "$variant_created")"
    h2 "Proposing $(wc -l <<< "$(echo -n "$to_propose")") package revisions"

    for rev in $to_propose; do
        echo
        echo "Proposing $rev"
        porchctl_dbcache rpkg propose "$rev"
    done


    to_approve="$(sed -n '11,20p' <<< "$cloned")"$'\n'"$(sed -n '11,20p' <<< "$edited")"$'\n'"$(sed -n '11,20p' <<< "$variantset_created")"$'\n'"$(sed -n '11,20p' <<< "$variant_created")"
    h2 "Approving $(wc -l <<< "$(echo -n "$to_approve")") package revisions"

    for rev in $to_approve; do
        echo
        echo "Approving $rev"
        porchctl_dbcache rpkg approve "$rev"
    done


    to_proposedelete="$(sed -n '16,20p' <<< "$cloned")"$'\n'"$(sed -n '16,20p' <<< "$edited")"$'\n'"$(sed -n '16,20p' <<< "$variantset_created")"$'\n'"$(sed -n '16,20p' <<< "$variant_created")"
    h2 "Proposing $(wc -l <<< "$(echo -n "$to_proposedelete")") package revisions for deletion"

    for rev in $to_proposedelete; do
        echo
        echo "Proposing $rev for deletion"
        porchctl_dbcache rpkg propose-delete "$rev"
    done
}

while [[ -n "${1:-DONE}" ]] ; do
    XCASE=$( tr "[:upper:]" "[:lower:]" <<< "${1-DONE}" )
    case $XCASE in
        reset)
            export RESET="true"
            ;;
        --no-cleandown)
            export CLEANDOWN="false"
            ;;
        *)
            break
            ;;
    esac
    shift
done

if ! which porchctl && ! ls "${PORCH_DIR}/.build/porchctl" && ! ls "${PORCH_DIR}/.build/disaster-test/porchctl" ; then
    {
        prefixLogs --prefix "building porchctl" --colour 7
        make porchctl PORCHCTL="${PORCH_DIR}/.build/disaster-test/porchctl"
    } &
    export PATH="${PORCH_DIR}/.build/disaster-test:$PATH"
fi

if [[ "${RESET:-false}" != "true" ]]; then
    install
    export CLEANDOWN="false"
fi

resetTestWorkload