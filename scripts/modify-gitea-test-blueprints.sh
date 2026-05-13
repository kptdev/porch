#!/usr/bin/env bash
# Copyright 2025 The kpt Authors
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

# modify-gitea-test-blueprints.sh - Manage kpt packages in test-blueprints.bundle

# Stricter error handling
set -e # Exit on error
set -u # Must predefine variables
set -o pipefail # Check errors in piped commands

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
BUNDLE_PATH="$SCRIPT_DIR/../test/pkgs/test-pkgs/test-blueprints.bundle"

# Initialize bundle workspace
init_bundle() {
    TEMP_DIR=$(mktemp -d)
    trap "rm -rf $TEMP_DIR" EXIT
    git clone "$BUNDLE_PATH" "$TEMP_DIR/repo"
    cd "$TEMP_DIR/repo"
    git config user.name "e2e Test User"
    git config user.email "e2etest@example.com"
}

# Check if package or tag exists
check_exists() {
    local package_name="$1"
    local tag="$2"
    
    if [ -d "$package_name" ]; then
        echo "Error: Package '$package_name' already exists"
        exit 1
    fi
    
    if [ -n "$tag" ] && git tag -l | grep -q "^$tag$"; then
        echo "Error: Tag '$tag' already exists"
        exit 1
    fi
}

# Pull kpt package from repo or local dir
pull_kpt_package() {
    local source="$1"
    local kpt_ref="$2"
    local kpt_dir="$3"
    local package_name="$4"
    
    cd "$TEMP_DIR/repo"
    
    if [ -d "$source" ]; then
        # Local directory
        echo "Copying kpt package from local directory $source"
        if [ -n "$kpt_dir" ]; then
            cp -r "$source/$kpt_dir" "$package_name"
        else
            cp -r "$source" "$package_name"
        fi
    else
        # Git repository - use kpt pkg get
        # Check if kpt is installed
        if ! command -v kpt >/dev/null 2>&1; then
            echo "Error: kpt is not installed or not in PATH"
            echo "Please install kpt: https://kpt.dev/installation/"
            exit 1
        fi
        
        echo "Pulling kpt package from $source@$kpt_ref"
        
        # Build kpt pkg get URL
        local kpt_url="$source"
        if [ -n "$kpt_dir" ]; then
            kpt_url="$source/$kpt_dir"
        fi
        if [ "$kpt_ref" != "main" ]; then
            kpt_url="$kpt_url@$kpt_ref"
        fi
        
        kpt pkg get "$kpt_url" "$package_name"
    fi
    
    # Update Kptfile name
    if [ -f "$package_name/Kptfile" ]; then
        sed -i "s/name: .*/name: $package_name/" "$package_name/Kptfile"
    fi
}

# Commit and tag package
commit_package() {
    local package_name="$1"
    local source="$2"
    local kpt_ref="$3"
    local tag="$4"
    
    git add .
    if [ -d "$source" ]; then
        git commit -m "Add pkg $package_name from local"
    else
        git commit -m "Add pkg $package_name from $source@$kpt_ref"
    fi
    
    if [ -n "$tag" ]; then
        git tag "$tag"
    fi
}



# Read bundle contents
read_bundle() {
    local temp_dir=$(mktemp -d)
    trap "rm -rf $temp_dir" EXIT
    
    git clone "$BUNDLE_PATH" "$temp_dir/repo"
    cd "$temp_dir/repo"
    
    echo ""
    echo "Bundle contents:"
    git --no-pager log --oneline --all
    echo ""
    echo "Packages:"
    ls -la
    echo ""
    echo "Tags:"
    git --no-pager tag -l
}

# Revert last commit
revert_bundle() {
    local temp_dir=$(mktemp -d)
    trap "rm -rf $temp_dir" EXIT
    
    git clone "$BUNDLE_PATH" "$temp_dir/repo"
    cd "$temp_dir/repo"
    
    echo "Last commit:"
    git --no-pager log -1 --oneline
    
    # Get tags and files before reverting
    local tags_to_delete=$(git tag --points-at HEAD)
    local files_removed=$(git --no-pager show --name-only --format="" HEAD)
    
    git reset --hard HEAD~1
    
    # Delete tags that were pointing to the reverted commit
    if [ -n "$tags_to_delete" ]; then
        echo "$tags_to_delete" | xargs -r git tag -d
    fi
    
    git bundle create "$BUNDLE_PATH" --all
    
    echo "Reverted last commit from bundle"
    echo ""
    echo "Removed files:"
    echo "$files_removed"
    if [ -n "$tags_to_delete" ]; then
        echo ""
        echo "Removed tags:"
        echo "$tags_to_delete"
    fi
}

# Main function
add_kpt_package() {
    if [ -z "$PACKAGE_NAME" ] || [ -z "$SOURCE" ]; then
        echo "Error: --name and --source are required"
        return 1
    fi
    
    init_bundle
    check_exists "$PACKAGE_NAME" "$TAG"
    pull_kpt_package "$SOURCE" "$KPT_REF" "$KPT_DIR" "$PACKAGE_NAME"
    commit_package "$PACKAGE_NAME" "$SOURCE" "$KPT_REF" "$TAG"
    git bundle create "$BUNDLE_PATH" --all
    
    read_bundle
    if [ -d "$SOURCE" ]; then
        echo "Added pkg $PACKAGE_NAME to test-blueprints bundle from local directory $SOURCE"
    else
        echo "Added pkg $PACKAGE_NAME to test-blueprints bundle from $SOURCE"
    fi
}

# Reload gitea test-blueprints repo
reload_gitea() {
    if ! command -v kubectl >/dev/null 2>&1; then
        echo "Error: kubectl is not installed or not in PATH"
        return 1
    fi
    
    if ! kubectl get pods -l app=gitea -n gitea >/dev/null 2>&1; then
        echo "Error: Gitea is not running"
        return 1
    fi
    
    echo "Reloading gitea test-blueprints repo..."
    "$SCRIPT_DIR/install-dev-gitea-setup.sh" reload
}

# Parse flags
parse_flags() {
    PACKAGE_NAME=""
    SOURCE=""
    KPT_REF="main"
    KPT_DIR=""
    TAG=""
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            -n|--name)
                PACKAGE_NAME="$2"
                shift 2
                ;;
            -s|--source)
                SOURCE="$2"
                shift 2
                ;;
            -r|--ref)
                KPT_REF="$2"
                shift 2
                ;;
            -d|--dir)
                KPT_DIR="$2"
                shift 2
                ;;
            -t|--tag)
                TAG="$2"
                shift 2
                ;;
            *)
                echo "Unknown option $1"
                exit 1
                ;;
        esac
    done
}

# Run if called directly
if [ "${BASH_SOURCE[0]}" == "${0}" ]; then
    if [ "$1" == "read" ]; then
        read_bundle
    elif [ "$1" == "revert" ]; then
        revert_bundle
    elif [ "$1" == "reload" ]; then
        reload_gitea
    elif [ "$1" == "-h" ] || [ "$1" == "--help" ] || [ $# -eq 0 ]; then
        echo "Usage: $0 -n <name> -s <source> [-r <ref>] [-d <dir>] [-t <tag>]"
        echo "       $0 read"
        echo "       $0 revert"
        echo "       $0 reload"
        echo ""
        echo "Options:"
        echo "  -n, --name     Package name in bundle (required)"
        echo "  -s, --source   Git repository URL or local directory path (required)"
        echo "  -r, --ref      Git ref (branch/tag, default: main)"
        echo "  -d, --dir      Subdirectory in source (optional)"
        echo "  -t, --tag      Git tag to create (optional)"
        echo ""
        echo "Commands:"
        echo "  read           Show bundle contents"
        echo "  revert         Undo last commit"
        echo "  reload         Reload gitea test-blueprints repo"
        echo ""
        echo "Examples:"
        echo "  # Add bucket package from GCP blueprints"
        echo "  $0 -n bucket -s https://github.com/GoogleCloudPlatform/blueprints.git -r bucket-blueprint-v0.4.3 -d catalog/bucket -t bucket/bucket-blueprint-v0.4.3"
        echo ""
        echo "  # Add package from local directory"
        echo "  $0 -n bucket -s ~/Downloads/bucket-copy/ -t bucket/bucket-blueprint-v0.4.3"
        echo ""
        echo "  # Show bundle contents"
        echo "  $0 read"
        echo ""
        echo "  # Revert last change"
        echo "  $0 revert"
        echo ""
        echo "  # Reload gitea repo"
        echo "  $0 reload"
        exit 1
    else
        parse_flags "$@"
        add_kpt_package
    fi
fi