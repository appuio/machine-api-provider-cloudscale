/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/cloudscale-ch/cloudscale-go-sdk/v5"
	configv1 "github.com/openshift/api/config/v1"
	apifeatures "github.com/openshift/api/features"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/library-go/pkg/features"
	capimachine "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/util/feature"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/featuregate"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/appuio/machine-api-provider-cloudscale/controllers"
	"github.com/appuio/machine-api-provider-cloudscale/pkg/machine"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(machinev1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var target string
	flag.StringVar(&target, "target", "manager", "The target mode of this binary. Valid values are 'manager', 'machine-api-controllers-manager', and 'termination-handler'.")

	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	var watchNamespace string
	flag.StringVar(&watchNamespace, "namespace", "", "Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	// TODO(bastjan): Check what those flags do. They are required since release-4.18
	featureGate := feature.DefaultMutableFeatureGate
	gateOpts, err := features.NewFeatureGateOptions(featureGate, apifeatures.SelfManaged, apifeatures.FeatureGateMachineAPIMigration)
	if err != nil {
		setupLog.Error(err, "Error setting up feature gates")
	}
	gateOpts.AddFlagsToGoFlagSet(nil)

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	switch target {
	case "manager":
		runManager(metricsAddr, probeAddr, watchNamespace, enableLeaderElection, featureGate)
	case "termination-handler":
		runTerminationHandler()
	case "machine-api-controllers-manager":
		runMachineAPIControllersManager(metricsAddr, probeAddr, watchNamespace, enableLeaderElection)
	default:
		setupLog.Error(nil, "invalid target", "target", target)
		os.Exit(1)
	}
}

func runManager(metricsAddr, probeAddr, watchNamespace string, enableLeaderElection bool, featureGate featuregate.MutableVersionedFeatureGate) {
	opts := ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "3a21cedd.appuio.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,

		Cache: cache.Options{
			// Override the default 10 hour sync period so that we pick up external changes
			// to the VMs within a reasonable time frame.
			SyncPeriod: ptr.To(10 * time.Minute),
		},
	}

	if watchNamespace != "" {
		opts.Cache.DefaultNamespaces = map[string]cache.Config{
			watchNamespace: {},
		}
		setupLog.Info("Watching machine-api objects only given namespace for reconciliation.", "namespace", watchNamespace)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	versionString := "unknown"
	if v, ok := debug.ReadBuildInfo(); ok {
		versionString = fmt.Sprintf("%s (%s)", v.Main.Version, v.GoVersion)
	}
	userAgent := "machine-api-provider-cloudscale.appuio.io/" + versionString

	newClient := func(token string) *cloudscale.Client {
		cs := cloudscale.NewClient(http.DefaultClient)
		cs.UserAgent = userAgent
		cs.AuthToken = token
		return cs
	}

	machineActuator := machine.NewActuator(machine.ActuatorParams{
		K8sClient: mgr.GetClient(),

		DefaultCloudscaleAPIToken: os.Getenv("CLOUDSCALE_API_TOKEN"),

		ServerClientFactory: func(token string) cloudscale.ServerService {
			return newClient(token).Servers
		},
		ServerGroupClientFactory: func(token string) cloudscale.ServerGroupService {
			return newClient(token).ServerGroups
		},
	})

	if err := capimachine.AddWithActuator(mgr, machineActuator, featureGate); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Machine")
		os.Exit(1)
	}

	if err := (&controllers.MachineSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachineSet")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func runTerminationHandler() {
	panic("not implemented")
}

func runMachineAPIControllersManager(metricsAddr, probeAddr, watchNamespace string, enableLeaderElection bool) {
	if watchNamespace == "" {
		setupLog.Error(nil, "namespace must be set for the machine-api-controllers manager")
		os.Exit(1)
	}

	opts := ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "458f6dca.appuio.io",
		LeaderElectionReleaseOnCancel: true,

		// Limit the manager to only watch the namespace the controller is running in.
		NewCache: func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
			opts.DefaultNamespaces = map[string]cache.Config{
				watchNamespace: {},
			}
			return cache.New(config, opts)
		},
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if err := (&controllers.MachineAPIControllersReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Namespace: watchNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UpstreamDeployment")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
