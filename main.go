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
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machineconfigurationv1 "github.com/openshift/api/machineconfiguration/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

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
	utilruntime.Must(machinev1beta1.AddToScheme(scheme))

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

	var secureMetrics bool
	var metricsCertPath, metricsCertName, metricsCertKey string
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	defaultNamespace := "default"
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		defaultNamespace = ns
	}

	var managedUpstreamClusterVersionName string
	var managedClusterVersionName, managedClusterVersionNamespace string
	flag.StringVar(&managedUpstreamClusterVersionName, "managed-upstream-cluster-version-name", "version", "The name of the upstream ClusterVersion object to manage.")
	flag.StringVar(&managedClusterVersionName, "managed-cluster-version-name", "version", "The name of the ClusterVersion object to manage.")
	flag.StringVar(&managedClusterVersionNamespace, "managed-cluster-version-namespace", defaultNamespace, "The namespace of the ClusterVersion object to manage.")

	var namespace string
	flag.StringVar(&namespace, "namespace", defaultNamespace, "The namespace where the controller should reconcile objects. This is used to limit the scope of the controller to a specific namespace. If explicitly set to \"\", the controller will reconcile objects in all namespaces.")

	var nodeDrainReconcileInterval time.Duration
	flag.DurationVar(&nodeDrainReconcileInterval, "node-drain-reconcile-interval", 3*time.Minute, "The interval at which to reconcile the node force drainer during active node drains. This is a safety mechanism to guard against edge cases, such as daemonsets orphaning pods, or programming errors in the node force drainer. Set to zero to disable this safety mechanism.")

	var machineNamespace string
	flag.StringVar(&machineNamespace, "machine-namespace", "openshift-machine-api", "The namespace where the machines are located.")

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Create watcher for metrics
	var metricsCertWatcher *certwatcher.CertWatcher

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       []func(*tls.Config){},
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	cacheOpts := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&machinev1beta1.Machine{}: {
				Namespaces: map[string]cache.Config{
					machineNamespace: {},
				},
			},
			&managedupgradev1beta1.ClusterVersion{}: {
				Namespaces: map[string]cache.Config{
					managedClusterVersionNamespace: {},
				},
			},
		},
	}
	if namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{
			defaultNamespace: {},
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
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
		Cache: cacheOpts,
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
	metrics.Registry.MustRegister(&controllers.NodeCollector{
		Client: mgr.GetClient(),
	})
	metrics.Registry.MustRegister(&controllers.MachineCollector{
		Client:           mgr.GetClient(),
		MachineNamespace: machineNamespace,
	})

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

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

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
