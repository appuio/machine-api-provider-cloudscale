#!/bin/bash
# This script is used to generate the userdata file from the main.tf.json output by component openshift4-terraform
# First optional positional argument is the path to the main.tf.json file

set -euo pipefail

maintf="${1:-main.tf.json}"
sdir="$(dirname "$(readlink -f "$0")")"

ignitionCA="$(jq -er '.module.cluster.ignition_ca' "$maintf")"
ignitionHost="api-int.$(jq -er '.module.cluster.cluster_name' "$maintf").$(jq -er '.module.cluster.base_domain' "$maintf")"

jsonnet "${sdir}/secret.jsonnet" --ext-str "ignitionHost=${ignitionHost}" --ext-str "ignitionCA=${ignitionCA}"
