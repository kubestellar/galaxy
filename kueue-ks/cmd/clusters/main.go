/*
Copyright 2024 The KubeStellar Authors.

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
	//"context"
	"context"
	"crypto/tls"
	"flag"
	"fmt"

	//"time"

	//"sync"

	//"fmt"
	"os"
	"path/filepath"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	//	"k8s.io/client-go/dynamic"
	//	"k8s.io/client-go/dynamic"
	//	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	//	"k8s.io/client-go/restmapper"

	//	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"kubestellar/galaxy/kueue-ks/controllers"

	metricsv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
	//	scheduler "kubestellar/galaxy/mc-scheduling/pkg/scheduler"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	//"sigs.k8s.io/controller-runtime/pkg/healthz"
	//	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	//	kueueClient "sigs.k8s.io/kueue/client-go/clientset/versioned"
	//
	// ocmclusterv1 "open-cluster-management.io/api/cluster/v1"
	//
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kueue.AddToScheme(scheme))
	utilruntime.Must(ksv1alpha1.AddToScheme(scheme))
	utilruntime.Must(metricsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var secureMetrics bool
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var enableHTTP2 bool
	var clusterQueue string
	var defaultResourceFlavorName string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8084", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&clusterQueue, "clusterQueue-name", "cluster-queue-ks", "cluster queue name")
	flag.StringVar(&defaultResourceFlavorName, "defaultResourceFlavorName", "default-flavor", "default flavor name")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}
	ctx := context.Background()
	enableLeaderElection = false
	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	/*
		configLoadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		configOverrides := &clientcmd.ConfigOverrides{CurrentContext: "its1"}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(configLoadingRules, configOverrides)
		restConfig, err := kubeConfig.ClientConfig()
		if err != nil {
			setupLog.Error(err, "Failed to switch context")
			os.Exit(1)
		}

		// Update clientset with the new context's configuration
		_, err = kubernetes.NewForConfig(restConfig)
		if err != nil {
			setupLog.Error(err, "Failed to create clientset")
			os.Exit(1)
		}
	*/

	kflexConfig, err := getContextConfig(kubeconfig, "kubeflex")
	if err != nil {
		setupLog.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}

	// ckonfig, err := clientcmd.LoadFromFile(kubeconfig)

	itsConfig, err := getContextConfig(kubeconfig, "its1")
	if err != nil {
		setupLog.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}

	//fmt.Printf("context switch itsConfig %v\n", itsConfig)

	its1Mgr, err := manager.New(itsConfig,
		ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress:   metricsAddr,
				SecureServing: secureMetrics,
				TLSOpts:       tlsOpts,
			},
			WebhookServer:          webhookServer,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         enableLeaderElection,
			LeaderElectionID:       "423ebda8.kubestellar.io",
		},
	)
	if err != nil {
		fmt.Printf("---------------- Error \n")
		setupLog.Error(err, "unable to start manager")

		os.Exit(1)
	}

	probeAddr = ":8085"
	metricsAddr = ":8086"

	kflexMgr, err := manager.New(kflexConfig,
		ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress:   metricsAddr,
				SecureServing: secureMetrics,
				TLSOpts:       tlsOpts,
			},
			WebhookServer:          webhookServer,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         false,
			LeaderElectionID:       "423ebda9.kubestellar.io",
		},
	)

	/*
		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress:   metricsAddr,
				SecureServing: secureMetrics,
				TLSOpts:       tlsOpts,
			},
			WebhookServer:          webhookServer,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         enableLeaderElection,
			LeaderElectionID:       "423ebda8.kubestellar.io",
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
	*/
	if err != nil {
		fmt.Printf("---------------- Error \n")
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	/*
		kClient, err := kueueClient.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}

		// in reality, you would only construct these once
		clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}

		groupResources, err := restmapper.GetAPIGroupResources(clientset.Discovery())
		if err != nil {
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}

		rm := restmapper.NewDiscoveryRESTMapper(groupResources)
		if err = (&controllers.WorkloadReconciler{
			Client:        mgr.GetClient(),
			KueueClient:   kClient,
			DynamicClient: dynamic.NewForConfigOrDie(ctrl.GetConfigOrDie()),
			RestMapper:    rm,
			Scheduler:     scheduler.NewDefaultScheduler(),
			Recorder:      mgr.GetEventRecorderFor("job-recorder"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Workload")
			os.Exit(1)
		}

			if err = (&controllers.ClusterMetricsReconciler{
				Client:                    mgr.GetClient(),
				Scheme:                    mgr.GetScheme(),
				WorkerClusters:            make(map[string]metricsv1alpha1.ClusterMetrics),
				ClusterQueue:              clusterQueue,
				DefaultResourceFlavorName: defaultResourceFlavorName,
			}).SetupWithManager(mgr); err != nil {
				setupLog.Error(err, "unable to create controller", "controller", "ClusterMetrics")
				os.Exit(1)
			}
			if err = (&controllers.AdmissionCheckReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			}).SetupWithManager(mgr); err != nil {
				setupLog.Error(err, "unable to create controller", "controller", "AdmissionCheck")
				os.Exit(1)
			}
	*/

	/* --
	setupReconciller(its1Mgr, kflexMgr)
	*/
	/*
		ctrl.NewControllerManagedBy(its1Mgr).
			// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
			//For(&ocmclusterv1.ManagedCluster{}).
			Complete(&controllers.ManagedClusterReconciler{
				//Client: mgr.GetClient(),
				Scheme:         its1Mgr.GetScheme(),
				Its1Client:     its1Mgr.GetClient(),
				KubeflexClient: its1Mgr.GetClient(),
			})
	*/

	//cl, err := client.New(kflexConfig, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Printf("---------------- Error \n")
		setupLog.Error(err, "unable to locate Config")
		os.Exit(1)
	}
	if err = (&controllers.ManagedClusterReconciler{
		Client:         its1Mgr.GetClient(),
		Scheme:         its1Mgr.GetScheme(),
		Its1Client:     its1Mgr.GetClient(),
		KubeflexClient: kflexMgr.GetClient(),
	}).SetupWithManager(its1Mgr); err != nil {
		fmt.Printf("---------------- Error \n")
		setupLog.Error(err, "unable to create controller", "controller", "ManagedCluster")
		os.Exit(1)
	}

	if err = (&controllers.ClusterMetricsReconciler{
		Client:                    kflexMgr.GetClient(),
		Scheme:                    scheme,
		WorkerClusters:            make(map[string]metricsv1alpha1.ClusterMetrics),
		ClusterQueue:              clusterQueue,
		DefaultResourceFlavorName: defaultResourceFlavorName,
	}).SetupWithManager(kflexMgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterMetrics")
		os.Exit(1)
	}
	/*
		if err = (&controllers.AdmissionCheckReconciler{
			Client: kflexMgr.GetClient(),
			Scheme: kflexMgr.GetScheme(),
		}).SetupWithManager(kflexMgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "AdmissionCheck")
			os.Exit(1)
		}

		kClient, err := kueueClient.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}
		clientset, err := kubernetes.NewForConfig(kflexConfig)
		// in reality, you would only construct these once
		//clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}
		dynClient, err := dynamic.NewForConfig(kflexConfig)
		if err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "unable to create dynamic client")
			os.Exit(1)
		}
		groupResources, err := restmapper.GetAPIGroupResources(clientset.Discovery())
		if err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "unable to locate Config")
			os.Exit(1)
		}

		rm := restmapper.NewDiscoveryRESTMapper(groupResources)

		if err = (&controllers.WorkloadReconciler{
			Client:        kflexMgr.GetClient(),
			KueueClient:   kClient,
			DynamicClient: dynClient, //ForConfigOrDie(ctrl.GetConfigOrDie()),
			//DynamicClient: dynamic.NewForConfigOrDie(ctrl.GetConfigOrDie()),
			RestMapper: rm,
			Scheduler:  scheduler.NewDefaultScheduler(),
			Recorder:   kflexMgr.GetEventRecorderFor("job-recorder"),
		}).SetupWithManager(kflexMgr); err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "unable to create controller", "controller", "Workload")
			os.Exit(1)
		}
	*/
	//+kubebuilder:scaffold:builder
	/*
		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up health check")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up ready check")
			os.Exit(1)
		}
	*/
	//controllers.ManageClusters()

	//var wg sync.WaitGroup
	//	wg.Add(2)
	//doneCh := make(chan struct{})

	go func() {
		//	defer wg.Done()
		//defer close(doneCh)
		//setupLog.Info("starting its1 manager")
		if err := its1Mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "problem running its1 manager")
			os.Exit(1)
		}
	}()

	//time.Sleep(5 * time.Second)
	go func() {
		//defer wg.Done()
		//defer close(doneCh)
		//setupLog.Info("starting kubeflex manager")
		if err := kflexMgr.Start(ctx); err != nil {
			//	if err := kflexMgr.Start(ctrl.SetupSignalHandler()); err != nil {
			fmt.Printf("---------------- Error \n")
			setupLog.Error(err, "problem running kubeflex manager")
			os.Exit(1)
		}
	}()

	select {}

	//select {}
	//wg.Wait()
	/*
		select {
		case <-doneCh:
			setupLog.Error(err, "both managers exited")
		case <-ctx.Done():
			setupLog.Error(err, "context cancelled")
		}
	*/
}

// SetupWithManager sets up the controller with the Manager.
func setupReconciller(its1Mgr ctrl.Manager, kflexMgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(kflexMgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		//For(&ocmclusterv1.ManagedCluster{}).
		Complete(&controllers.ManagedClusterReconciler{
			//Client: mgr.GetClient(),
			Scheme:         its1Mgr.GetScheme(),
			Its1Client:     its1Mgr.GetClient(),
			KubeflexClient: kflexMgr.GetClient(),
		})
}
func getContextConfig(kubeconfigPath, context string) (*rest.Config, error) {
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	config.CurrentContext = context
	clientConfig := clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{})

	return clientConfig.ClientConfig()
}
