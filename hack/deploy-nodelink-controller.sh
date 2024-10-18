#!/bin/bash
# This script deploys the nodelink controller as dev-nodelink-controller with the same image as the upstream machine-api-operator.
# This allows testing this provider on a cluster that does not yet have a full machine-api-controllers deployment.
# If the machine-api-controllers deployment is already present, this script will skip the deployment.
set -euo pipefail


UPSTREAM_NODELINK_DEPLOYMENT="machine-api-controllers"
UPSTREAM_MACHINE_API_OPERATOR_DEPLOYMENT="machine-api-operator"

if kubectl get deployment "${UPSTREAM_NODELINK_DEPLOYMENT}" &> /dev/null; then
  echo "Real upstream nodelink deployment already exists, skipping"
  exit 0
fi

tmpdir=$(mktemp -d)

image=$(kubectl get deployment "${UPSTREAM_MACHINE_API_OPERATOR_DEPLOYMENT}" -oyaml | yq '.spec.template.spec.containers | filter(.name == "machine-api-operator") | .[0].image')

imageParts=(${image//@/ })

echo "Deploying nodelink as 'dev-nodelink-controller' with image '${imageParts[0]}@${imageParts[1]}'"

cp hack/nodelink-controller.yaml "${tmpdir}/nodelink-deployment.yaml"

cat > "${tmpdir}/Kustomization.yaml" << YAML
resources:
- nodelink-deployment.yaml

images:
- name: ${imageParts[0]}
  digest: ${imageParts[1]}
YAML

kustomize build "${tmpdir}" | kubectl apply -f -

rm -rf "${tmpdir}"
