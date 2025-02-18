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
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/api/machineconfiguration/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	managedupgradev1beta1 "github.com/appuio/openshift-upgrade-controller/api/v1beta1"
	"github.com/appuio/openshift-upgrade-controller/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	utilruntime.Must(machineconfigurationv1.AddToScheme(scheme))

	utilruntime.Must(managedupgradev1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	defaultNamespace := "default"
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		defaultNamespace = ns
	}

	var managedUpstreamClusterVersionName string
	var managedClusterVersionName, managedClusterVersionNamespace string
	flag.StringVar(&managedUpstreamClusterVersionName, "managed-upstream-cluster-version-name", "version", "The name of the upstream ClusterVersion object to manage.")
	flag.StringVar(&managedClusterVersionName, "managed-cluster-version-name", "version", "The name of the ClusterVersion object to manage.")
	flag.StringVar(&managedClusterVersionNamespace, "managed-cluster-version-namespace", defaultNamespace, "The namespace of the ClusterVersion object to manage.")

	var nodeDrainReconcileInterval time.Duration
	flag.DurationVar(&nodeDrainReconcileInterval, "node-drain-reconcile-interval", 3*time.Minute, "The interval at which to reconcile the node force drainer during active node drains. This is a safety mechanism to guard against edge cases, such as daemonsets orphaning pods, or programming errors in the node force drainer. Set to zero to disable this safety mechanism.")

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "9fced507.appuio.io",
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
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	metrics.Registry.MustRegister(&controllers.UpgradeInformationCollector{
		Client: mgr.GetClient(),

		ManagedUpstreamClusterVersionName: managedUpstreamClusterVersionName,
	})
	metrics.Registry.MustRegister(&controllers.ClusterVersionCollector{
		Client: mgr.GetClient(),

		ManagedClusterVersionName:      managedClusterVersionName,
		ManagedClusterVersionNamespace: managedClusterVersionNamespace,
	})

	if err = (&controllers.NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}
	if err = (&controllers.ClusterVersionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Clock: realClock{},

		ManagedUpstreamClusterVersionName: managedUpstreamClusterVersionName,
		ManagedClusterVersionName:         managedClusterVersionName,
		ManagedClusterVersionNamespace:    managedClusterVersionNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterVersion")
		os.Exit(1)
	}
	if err = (&controllers.UpgradeJobReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Clock: realClock{},

		ManagedUpstreamClusterVersionName: managedUpstreamClusterVersionName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UpgradeJob")
		os.Exit(1)
	}
	if err = (&controllers.UpgradeConfigReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("upgrade-config-controller"),

		Clock: realClock{},

		ManagedUpstreamClusterVersionName: managedUpstreamClusterVersionName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UpgradeConfig")
		os.Exit(1)
	}
	if err = (&controllers.UpgradeSuspensionWindowReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UpgradeSuspensionWindow")
		os.Exit(1)
	}
	if err = (&controllers.NodeForceDrainReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("node-force-drain-controller"),

		Clock: realClock{},

		MaxReconcileIntervalDuringActiveDrain: nodeDrainReconcileInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NodeForceDrainReconciler")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}
