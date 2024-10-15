#!/bin/bash
set -euo pipefail

# map names of CRD files between the vendored openshift/api repository and the ./install directory
CRDS_MAPPING=( "0000_10_machine-api_01_machines-Default.crd.yaml:machine.openshift.io.crd.yaml"
               "0000_10_machine-api_01_machinesets-Default.crd.yaml:machineset.openshift.io.crd.yaml"
               "0000_10_machine-api_01_machinehealthchecks.crd.yaml:machinehealthcheck.openshift.io.crd.yaml" )

for crd in "${CRDS_MAPPING[@]}" ; do
    SRC="${crd%%:*}"
    DES="${crd##*:}"
    cp "${VENDOR_DIR:-vendor}/github.com/openshift/api/machine/v1beta1/zz_generated.crd-manifests/$SRC" "config/crds/$DES"
done
