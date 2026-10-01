# Vendored `k8s.io/apiserver` patches

This directory holds local patches that are applied on top of the upstream
`k8s.io/apiserver` source vendored into `third_party/k8s.io/apiserver-v<version>`.

They are applied automatically by
[`scripts/util/update-kube-apiserver-vendoring.sh`](../update-kube-apiserver-vendoring.sh)
after a fresh upstream checkout, so the local modifications no longer have to be
remembered and re-applied by hand.

## Patches

### `apiserver-request-timeout-upper-bound.patch`

Raises `requestTimeoutUpperBound` in `pkg/endpoints/handlers/rest.go` from the
upstream `34s` to `290s`.

The aggregated Porch API server proxies create/update/patch/delete requests to
git and the function runner, which can take significantly longer than the
upstream default. The upstream cap would abort those long-running operations, so
the bound is raised for the vendored fork only.

## Temporary legacy debt

This fork of `k8s.io/apiserver` (and therefore every patch here) is temporary.
Porch is migrating from the custom aggregated-apiserver architecture to a
CRD/controller architecture, after which the vendored tree and these patches
should be deleted entirely. Do not build new functionality on top of these
patches — keep them minimal so the eventual removal stays easy.

## When a patch fails to apply

`update-kube-apiserver-vendoring.sh` runs `git apply --check` before applying.
If an upstream upgrade changes the surrounding code enough that a patch no
longer applies, the script fails with a clear error naming the offending patch.
Refresh the patch against the new upstream source (or drop it if the migration
has made it obsolete), then re-run the script.
