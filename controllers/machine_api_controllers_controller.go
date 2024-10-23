package controllers

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/google/go-jsonnet"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	imagesConfigMapName            = "machine-api-operator-images"
	originalUpstreamDeploymentName = "machine-api-controllers"
	imageKey                       = "images.json"

	caBundleConfigMapName = "appuio-machine-api-ca-bundle"
)

//go:embed machine_api_controllers_deployment.jsonnet
var deploymentTemplate string

// MachineAPIControllersReconciler creates a appuio-machine-api-controllers deployment based on the images.json ConfigMap
// if the upstream machine-api-controllers does not exist.
type MachineAPIControllersReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Namespace string
}

func (r *MachineAPIControllersReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != imagesConfigMapName {
		return ctrl.Result{}, nil
	}

	l := log.FromContext(ctx).WithName("UpstreamDeploymentReconciler.Reconcile")
	l.Info("Reconciling")

	var imageCM corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &imageCM); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ij, ok := imageCM.Data[imageKey]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("images.json key not found in ConfigMap %q", imagesConfigMapName)
	}
	images := make(map[string]string)
	if err := json.Unmarshal([]byte(ij), &images); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to unmarshal %q from %q: %w", imageKey, imagesConfigMapName, err)
	}

	// Check that the original upstream deployment does not exist
	// If it does, we should not create the new deployment
	var upstreamDeployment appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{
		Name:      originalUpstreamDeploymentName,
		Namespace: r.Namespace,
	}, &upstreamDeployment)
	if err == nil {
		return ctrl.Result{}, fmt.Errorf("original upstream deployment %s already exists", originalUpstreamDeploymentName)
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to check for original upstream deployment %s: %w", originalUpstreamDeploymentName, err)
	}

	caBundleConfigMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      caBundleConfigMapName,
			Namespace: r.Namespace,
			Labels: map[string]string{
				"config.openshift.io/inject-trusted-cabundle": "true",
			},
		},
	}
	if err := controllerutil.SetControllerReference(&imageCM, &caBundleConfigMap, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
	}
	if err := r.Client.Patch(ctx, &caBundleConfigMap, client.Apply, client.FieldOwner("upstream-deployment-controller")); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ConfigMap %q: %w", caBundleConfigMapName, err)
	}

	vm, err := jsonnetVMWithContext(images, caBundleConfigMap)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create jsonnet VM: %w", err)
	}

	ud, err := vm.EvaluateAnonymousSnippet("controllers_deployment.jsonnet", deploymentTemplate)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to evaluate jsonnet: %w", err)
	}

	// TODO(bastjan) this could be way more generic and support any kind of object.
	// We don't need any other object types right now, so we're keeping it simple.
	var toDeploy appsv1.Deployment
	if err := json.Unmarshal([]byte(ud), &toDeploy); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to unmarshal jsonnet output: %w", err)
	}
	if toDeploy.APIVersion != "apps/v1" || toDeploy.Kind != "Deployment" {
		return ctrl.Result{}, fmt.Errorf("expected Deployment, got %s/%s", toDeploy.APIVersion, toDeploy.Kind)
	}
	toDeploy.Namespace = r.Namespace
	if err := controllerutil.SetControllerReference(&imageCM, &toDeploy, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
	}
	if err := r.Client.Patch(ctx, &toDeploy, client.Apply, client.FieldOwner("upstream-deployment-controller")); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply Deployment %q: %w", toDeploy.GetName(), err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineAPIControllersReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func jsonnetVMWithContext(images map[string]string, cabundle corev1.ConfigMap) (*jsonnet.VM, error) {
	jcr, err := json.Marshal(map[string]any{
		"images":   images,
		"cabundle": cabundle,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to marshal jsonnet context: %w", err)
	}
	jvm := jsonnet.MakeVM()
	jvm.ExtCode("context", string(jcr))
	// Don't allow imports
	jvm.Importer(&jsonnet.MemoryImporter{})
	return jvm, nil
}
