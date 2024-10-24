package controllers

import (
	"context"
	"fmt"
	"testing"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_MachineSetReconciler_Reconcile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, machinev1beta1.AddToScheme(scheme))

	ms := &machinev1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machineset1",
			Namespace: "default",
			Annotations: map[string]string{
				"random":  "annotation",
				labelsKey: "a=a,b=b",
			},
		},
		Spec: machinev1beta1.MachineSetSpec{},
	}

	setFlavorOnMachineSet(ms, "plus-4-2")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ms).
		Build()

	subject := &MachineSetReconciler{
		Client: c,
		Scheme: scheme,
	}

	_, err := subject.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ms)})
	require.NoError(t, err)
	updated := &machinev1beta1.MachineSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(ms), updated))
	assert.Equal(t, "2", updated.Annotations[cpuKey])
	assert.Equal(t, "4096", updated.Annotations[memoryKey])
	assert.Equal(t, "0", updated.Annotations[gpuKey])
	assert.Equal(t, "a=a,b=b,kubernetes.io/arch=amd64", updated.Annotations[labelsKey])
}

func setFlavorOnMachineSet(machine *machinev1beta1.MachineSet, flavor string) {
	machine.Spec.Template.Spec.ProviderSpec.Value = &runtime.RawExtension{
		Raw: []byte(fmt.Sprintf(`{"flavor": "%s"}`, flavor)),
	}
}
