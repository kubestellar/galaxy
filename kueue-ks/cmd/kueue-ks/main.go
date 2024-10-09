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
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	metricsv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
	"kubestellar/galaxy/kueue-ks/internal/controller"
	scheduler "kubestellar/galaxy/mc-scheduling/pkg/scheduler"
	"os"
	"time"

	kfv1aplha1 "github.com/kubestellar/kubeflex/api/v1alpha1"
	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	kslclient "github.com/kubestellar/kubestellar/pkg/client"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/clock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueueClient "sigs.k8s.io/kueue/client-go/clientset/versioned"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

var (
	kubeconfig string
	k8sctx     = context.Background()
)

const (
	ControlPlaneTypeLabel    = "kflex.kubestellar.io/cptype"
	ControlPlaneTypeITS      = "its"
	ErrNoControlPlane        = "no control plane found. At least one control plane labeled with %s=%s must be present"
	ErrControlPlaneNotFound  = "control plane with type %s and name %s was not found"
	ErrMultipleControlPlanes = "more than one control plane of type %s was found and no name was specified"
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
	var deleteOnCompletion bool
	var defaultResourceFlavorName string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8085", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8086", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&clusterQueue, "clusterQueue-name", "cluster-queue-ks", "cluster queue name")
	flag.BoolVar(&deleteOnCompletion, "deleteOnCompletion", false, "If set, workload will be auto cleaned up from the WEC")
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

	enableLeaderElection = false
	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	kflexConfig, err := rest.InClusterConfig()
	if err != nil {
		setupLog.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}

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
			LeaderElection:         enableLeaderElection,
			LeaderElectionID:       "423ebda8.kubestellar.io",
		},
	)

	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.ClusterMetricsReconciler{
		Client:                    kflexMgr.GetClient(),
		Scheme:                    kflexMgr.GetScheme(),
		WorkerClusters:            make(map[string]metricsv1alpha1.ClusterMetrics),
		ClusterQueue:              clusterQueue,
		DefaultResourceFlavorName: defaultResourceFlavorName,
	}).SetupWithManager(kflexMgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterMetrics")
		os.Exit(1)
	}
	if err = (&controller.AdmissionCheckReconciler{
		Client: kflexMgr.GetClient(),
		Scheme: kflexMgr.GetScheme(),
	}).SetupWithManager(kflexMgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AdmissionCheck")
		os.Exit(1)
	}

	kClient, err := kueueClient.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to locate Config")
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(kflexConfig)
	if err != nil {
		setupLog.Error(err, "unable to locate Config")
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(kflexConfig)
	if err != nil {
		setupLog.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}
	groupResources, err := restmapper.GetAPIGroupResources(clientset.Discovery())
	if err != nil {
		setupLog.Error(err, "unable to locate Config")
		os.Exit(1)
	}

	rm := restmapper.NewDiscoveryRESTMapper(groupResources)
	wr := &controller.WorkloadReconciler{
		Client:                 kflexMgr.GetClient(),
		KueueClient:            kClient,
		DynamicClient:          dynClient,
		RestMapper:             rm,
		Scheduler:              scheduler.NewDefaultScheduler(),
		Recorder:               kflexMgr.GetEventRecorderFor("job-recorder"),
		CleanupWecOnCompletion: deleteOnCompletion,
	}
	if err = wr.SetupWithManager(kflexMgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Workload")
		os.Exit(1)
	}
	/*
		if err = (&controller.WorkloadReconciler{
			Client:        kflexMgr.GetClient(),
			KueueClient:   kClient,
			DynamicClient: dynClient,
			RestMapper:    rm,
			Scheduler:     scheduler.NewDefaultScheduler(),
			Recorder:      kflexMgr.GetEventRecorderFor("job-recorder"),
		}).SetupWithManager(kflexMgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Workload")
			os.Exit(1)
		}
	*/
	if err = (&controller.CombinedStatusReconciler{
		Client:             kflexMgr.GetClient(),
		Scheme:             kflexMgr.GetScheme(),
		Clock:              clock.RealClock{},
		WorkloadReconciler: wr,
	}).SetupWithManager(kflexMgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CombinedStatus")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	go func() {
		if err := kflexMgr.Start(k8sctx); err != nil {
			setupLog.Error(err, "problem running kubeflex manager")
			os.Exit(1)
		}
	}()

	select {}
}

// SetupWithManager sets up the controller with the Manager.
func setupReconciller(its1Mgr ctrl.Manager, kflexMgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(kflexMgr).
		Complete(&controller.ManagedClusterReconciler{
			Scheme:         its1Mgr.GetScheme(),
			Its1Client:     its1Mgr.GetClient(),
			KubeflexClient: kflexMgr.GetClient(),
		})
}

func getRestConfig(cpName, labelValue string) (*rest.Config, string, error) {
	logger := log.FromContext(k8sctx)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	kubeClient := *kslclient.GetClient()
	labelSelector := labels.SelectorFromSet(labels.Set(map[string]string{
		ControlPlaneTypeLabel: labelValue,
	}))
	var targetCP *kfv1aplha1.ControlPlane

	// wait until there are control planes with supplied labels
	logger.Info("Waiting for cp", "type", labelValue, "name", cpName)
	err := wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		if cpName != "" {
			cp := &kfv1aplha1.ControlPlane{}
			err := kubeClient.Get(ctx, client.ObjectKey{Name: cpName}, cp, &client.GetOptions{})
			if err == nil {
				targetCP = cp
				return true, nil
			}
			logger.Error(err, "Unable to connect to its1")
			return false, nil
		}
		list := &kfv1aplha1.ControlPlaneList{}
		err := kubeClient.List(ctx, list, &client.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			logger.Info("Failed to list control planes, will retry", "err", err)
			return false, nil
		}
		if len(list.Items) == 0 {
			logger.Info("No control planes yet, will retry")
			return false, nil
		}
		if len(list.Items) == 1 {
			targetCP = &list.Items[0]
			return true, nil
		}
		// TODO - we do not allow this case for a WDS as there is a 1:1 relashionship controller:cp for WDS.
		// Need to revisit for ITS where we can have multiple shards.
		// Assume we are not waiting for control planes to go away.
		return true, fmt.Errorf(ErrMultipleControlPlanes, labelValue)
	})
	if err != nil {
		return nil, "", err
	}
	if targetCP == nil {
		if cpName != "" {
			return nil, "", fmt.Errorf(ErrControlPlaneNotFound, labelValue, cpName)
		}
		return nil, "", fmt.Errorf(ErrNoControlPlane, ControlPlaneTypeLabel, labelValue)
	}

	clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return nil, "", fmt.Errorf("error creating new clientset: %w", err)
	}

	if targetCP.Status.SecretRef == nil {
		return nil, "", fmt.Errorf("access secret reference doesn't exist for %s", targetCP.Name)
	}
	namespace := targetCP.Status.SecretRef.Namespace
	name := targetCP.Status.SecretRef.Name
	key := targetCP.Status.SecretRef.InClusterKey

	// determine if the configuration is in-cluster or off-cluster and use related key
	_, err = rest.InClusterConfig()
	if err != nil { // off-cluster
		key = targetCP.Status.SecretRef.Key
	}

	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("error getting secrets: %w", err)
	}

	restConf, err := restConfigFromBytes(secret.Data[key])
	if err != nil {
		return nil, "", fmt.Errorf("error getting rest config from bytes: %w", err)
	}

	return restConf, targetCP.Name, nil
}

func restConfigFromBytes(kubeconfig []byte) (*rest.Config, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
	if err != nil {
		return nil, err
	}

	return clientConfig.ClientConfig()
}
