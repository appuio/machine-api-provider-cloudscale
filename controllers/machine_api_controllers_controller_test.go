package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_MachineAPIControllersReconciler_Reconcile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const namespace = "openshift-machine-api"

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	images := map[string]string{
		"machineAPIOperator": "registry.io/machine-api-operator:v1.0.0",
		"kubeRBACProxy":      "registry.io/kube-rbac-proxy:v1.0.0",
	}
	imagesJSON, err := json.Marshal(images)
	require.NoError(t, err)

	ucm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imagesConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			imageKey: string(imagesJSON),
		},
	}

	c := &fakeSSA{
		fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(ucm).
			Build(),
	}

	r := &MachineAPIControllersReconciler{
		Client: c,
		Scheme: scheme,

		Namespace: namespace,
	}

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ucm)})
	require.NoError(t, err)

	var deployment appsv1.Deployment
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "appuio-" + originalUpstreamDeploymentName}, &deployment))

	assert.Equal(t, "system-node-critical", deployment.Spec.Template.Spec.PriorityClassName)
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Image == images["machineAPIOperator"] || c.Image == images["kubeRBACProxy"] {
			continue
		}
		t.Errorf("expected image %q or %q, got %q", images["machineAPIOperator"], images["kubeRBACProxy"], c.Image)
	}
}

func Test_MachineAPIControllersReconciler_OriginalDeploymentExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const namespace = "openshift-machine-api"

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	images := map[string]string{
		"machineAPIOperator": "registry.io/machine-api-operator:v1.0.0",
		"kubeRBACProxy":      "registry.io/kube-rbac-proxy:v1.0.0",
	}
	imagesJSON, err := json.Marshal(images)
	require.NoError(t, err)

	ucm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imagesConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			imageKey: string(imagesJSON),
		},
	}

	origDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      originalUpstreamDeploymentName,
			Namespace: namespace,
		},
	}

	c := &fakeSSA{
		fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(ucm, origDeploy).
			Build(),
	}

	r := &MachineAPIControllersReconciler{
		Client: c,
		Scheme: scheme,

		Namespace: namespace,
	}

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ucm)})
	require.ErrorContains(t, err, "machine-api-controllers already exists")
}

// fakeSSA is a fake client that approximates SSA.
// It creates objects that don't exist yet and _updates_ them if they exist.
// This is completely kaputt since the object is overwritten with the new object.
// See https://github.com/kubernetes-sigs/controller-runtime/issues/2341
type fakeSSA struct {
	client.WithWatch
}

// Patch approximates SSA by creating objects that don't exist yet.
func (f *fakeSSA) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	// Apply patches are supposed to upsert, but fake client fails if the object doesn't exist,
	// if an apply patch occurs for an object that doesn't yet exist, create it.
	if patch.Type() != types.ApplyPatchType {
		return f.WithWatch.Patch(ctx, obj, patch, opts...)
	}
	check, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return errors.New("could not check for object in fake client")
	}
	if err := f.WithWatch.Get(ctx, client.ObjectKeyFromObject(obj), check); apierrors.IsNotFound(err) {
		if err := f.WithWatch.Create(ctx, check); err != nil {
			return fmt.Errorf("could not inject object creation for fake: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("could not check for object in fake client: %w", err)
	}
	return f.WithWatch.Update(ctx, obj)
}
