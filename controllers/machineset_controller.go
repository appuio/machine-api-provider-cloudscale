package controllers

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	csv1beta1 "github.com/appuio/machine-api-provider-cloudscale/api/cloudscale/provider/v1beta1"
)

// MachineSetReconciler reconciles a MachineSet object
type MachineSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// This exposes compute information based on the providerSpec input.
	// This is needed by the autoscaler to foresee upcoming capacity when scaling from zero.
	// https://github.com/openshift/enhancements/pull/186
	cpuKey    = "machine.openshift.io/vCPU"
	memoryKey = "machine.openshift.io/memoryMb"
	gpuKey    = "machine.openshift.io/GPU"
	labelsKey = "capacity.cluster-autoscaler.kubernetes.io/labels"

	gpuKeyValue = "0"
	arch        = "kubernetes.io/arch=amd64"
)

// Reconcile reacts to MachineSet changes and updates the annotations used by the OpenShift autoscaler.
// GPU is always set to 0, as cloudscale does not provide GPU instances.
// The architecture label is always set to amd64.
func (r *MachineSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var machineSet machinev1beta1.MachineSet
	if err := r.Get(ctx, req.NamespacedName, &machineSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !machineSet.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	origSet := machineSet.DeepCopy()

	if machineSet.Annotations == nil {
		machineSet.Annotations = make(map[string]string)
	}

	spec, err := csv1beta1.ProviderSpecFromRawExtension(machineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get provider spec from machine template: %w", err)
	}
	if spec == nil {
		return ctrl.Result{}, nil
	}

	flavor, err := parseCloudscaleFlavor(spec.Flavor)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse flavor %q: %w", spec.Flavor, err)
	}

	machineSet.Annotations[cpuKey] = strconv.Itoa(flavor.CPU)
	machineSet.Annotations[memoryKey] = strconv.Itoa(flavor.MemGB * 1024)
	machineSet.Annotations[gpuKey] = gpuKeyValue

	// We guarantee that any existing labels provided via the capacity annotations are preserved.
	// See https://github.com/kubernetes/autoscaler/pull/5382 and https://github.com/kubernetes/autoscaler/pull/5697
	machineSet.Annotations[labelsKey] = mergeCommaSeparatedKeyValuePairs(
		arch,
		machineSet.Annotations[labelsKey])

	if equality.Semantic.DeepEqual(origSet.Annotations, machineSet.Annotations) {
		return ctrl.Result{}, nil
	}

	if err := r.Patch(ctx, &machineSet, client.MergeFrom(origSet)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch MachineSet %q: %w", machineSet.Name, err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&machinev1beta1.MachineSet{}).
		Complete(r)
}

type cloudscaleFlavor struct {
	Type  string
	CPU   int
	MemGB int
}

var cloudscaleFlavorRegexp = regexp.MustCompile(`^(\w+)-(\d+)-(\d+)$`)

// Parse parses a cloudscale flavor string.
func parseCloudscaleFlavor(flavor string) (cloudscaleFlavor, error) {
	parts := cloudscaleFlavorRegexp.FindStringSubmatch(flavor)

	if len(parts) != 4 {
		return cloudscaleFlavor{}, fmt.Errorf("flavor %q does not match expected format", flavor)
	}
	mem, err := strconv.Atoi(parts[2])
	if err != nil {
		return cloudscaleFlavor{}, fmt.Errorf("failed to parse memory from flavor %q: %w", flavor, err)
	}
	cpu, err := strconv.Atoi(parts[3])
	if err != nil {
		return cloudscaleFlavor{}, fmt.Errorf("failed to parse CPU from flavor %q: %w", flavor, err)
	}

	return cloudscaleFlavor{
		Type:  parts[1],
		CPU:   cpu,
		MemGB: mem,
	}, nil
}

// mergeCommaSeparatedKeyValuePairs merges multiple comma separated lists of key=value pairs into a single, comma-separated, list
// of key=value pairs. If a key is present in multiple lists, the value from the last list is used.
func mergeCommaSeparatedKeyValuePairs(lists ...string) string {
	merged := make(map[string]string)
	for _, list := range lists {
		for _, kv := range strings.Split(list, ",") {
			kv := strings.Split(kv, "=")
			if len(kv) != 2 {
				// ignore invalid key=value pairs
				continue
			}
			merged[kv[0]] = kv[1]
		}
	}
	// convert the map back to a comma separated list
	var result []string
	for k, v := range merged {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	slices.Sort(result)
	return strings.Join(result, ",")
}
