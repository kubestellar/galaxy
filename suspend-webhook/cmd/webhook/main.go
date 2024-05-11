package main

import (
	"flag"
	"log"
	"os"

	"context"
	"encoding/json"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	Version  string
)

const (
	controlLabelKey           = "kubestellar.io/cluster"
	container_count_threshold = 2
)

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

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	config := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(config, ctrl.Options{

		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "c6f71c85.kflex.kubestellar.org",
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

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Set up the webhook server.
	hookServer := mgr.GetWebhookServer()

	// Register the admission webhook to the webhook server.
	hookServer.Register("/mutate-workflows", &webhook.Admission{Handler: NewWorkflowWebhookHandler(mgr)})

	// Start the manager, which will start the server as well.
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Fatalf("Error starting manager: %v", err)
	}
}

// WorkflowWebhookHandler handles the incoming webhook calls.
type WorkflowWebhookHandler struct {
	Client  client.Client
	Decoder *admission.Decoder
}

func NewWorkflowWebhookHandler(mgr manager.Manager) *WorkflowWebhookHandler {
	return &WorkflowWebhookHandler{
		Client:  mgr.GetClient(),
		Decoder: admission.NewDecoder(mgr.GetScheme()),
	}
}

// Handle processes the AdmissionReview request and applies mutations.
func (h *WorkflowWebhookHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	workflow := &v1alpha1.Workflow{}

	// Decode the Workflow object from the request
	err := h.Decoder.Decode(req, workflow)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// this is used for the standalone argo workflows demo
	// Modify the .spec.enable field based on the presence of the control label key
	// also, make sure logs are always archived in S3
	// if workflow.Labels != nil {
	// 	if _, ok := workflow.Labels[controlLabelKey]; ok {
	// 		workflow.Spec.Suspend = pointer.Bool(true)
	// 		workflow.Spec.ArchiveLogs = pointer.Bool(true)
	// 	}
	// }

	// KFP demo

	// ensure workflow archives logs
	workflow.Spec.ArchiveLogs = pointer.Bool(true)

	// if label not present, it has not yet been scheduled, then set to suspend = true
	if _, ok := workflow.Labels[controlLabelKey]; !ok {
		workflow.Spec.Suspend = pointer.Bool(true)
	}

	// Create the patch operations
	marshaledWorkflow, err := json.Marshal(workflow)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Return the patch response
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledWorkflow)
}

// func shouldBeRemoteByAnnotation(workflow *v1alpha1.Workflow) bool {
// 	if ann, ok := workflow.Annotations["pipelines.kubeflow.org/run_name"]; ok {
// 		if strings.Contains(ann, "remote") {
// 			return true
// 		}
// 	}
// 	return false
// }

func countContainers(workflow *v1alpha1.Workflow) int {
	count := 0
	for _, template := range workflow.Spec.Templates {
		if template.Container != nil {
			count++
		}
	}
	return count
}
