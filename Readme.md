# machine-api-provider-cloudscale

Provider for cloudscale.ch for the OpenShift machine-api.

## Development

## Updating OCP dependencies

```bash
RELEASE=release-4.XX
go get -u "github.com/openshift/api@${RELEASE}"
go get -u "github.com/openshift/library-go@${RELEASE}"
go get -u "github.com/openshift/machine-api-operator@${RELEASE}"
go mod tidy

# Update the CRDs required for testing on a local non-OCP cluster
make sync-crds
```

### Testing on a local non-OCP cluster

```bash
# Apply required upstream CRDs
kubectl apply -k config/crds

make run

# Apply a generic machine object that will not join a cluster
kubectl apply -f config/samples/machine-cloudscale-generic.yml
```

### Testing on a Project Syn managed OCP cluster

```bash
# Deploy nodelink-controller if not already deployed
kubectl apply -f config/deploy/nodelink-controller.yml

# Generate the userData secret from the main.tf.json in the cluster catalog
./pkg/machine/userdata/userdata-secret-from-maintfjson.sh manifests/openshift4-terraform/main.tf.json | k apply -f-

make run

# Apply a known working machine object
# This will join the cluster and become a worker node
# You want to update:
# - metadata.labels["machine.openshift.io/cluster-api-cluster"]
# - spec.providerSpec.value.zone
# - spec.providerSpec.value.baseDomain
# - spec.providerSpec.value.image
# - spec.providerSpec.value.interfaces[0].networkUUID
kubectl apply -f config/samples/machine-cloudscale-known-working.yml
```
