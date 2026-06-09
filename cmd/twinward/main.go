package main

import (
	"flag"
	"log/slog"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	twinwardv1alpha1 "github.com/weisshorn-cyd/twinward/api/v1alpha1"
	"github.com/weisshorn-cyd/twinward/internal/config"
	"github.com/weisshorn-cyd/twinward/internal/controller"
	"github.com/weisshorn-cyd/twinward/internal/logging"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(twinwardv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var leaderElection bool
	var logLevel slog.Level

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.TextVar(&logLevel, "log-level", slog.LevelInfo, "Log level (debug, info, warn, error).")
	flag.Parse()

	log := logging.Configure(logLevel).With("logger", "setup")

	contentHashSalt, err := controller.NewContentHashSalt()
	if err != nil {
		log.Error("unable to initialize content hash salt", "error", err)
		os.Exit(1)
	}

	namespacePolicy, err := config.NewNamespacePolicy(os.Getenv(config.AllowedNamespacesEnv))
	if err != nil {
		log.Error("invalid namespace allowlist", "error", err, "env", config.AllowedNamespacesEnv)
		os.Exit(1)
	}
	log.Info("configured namespace allowlist", "env", config.AllowedNamespacesEnv, "patterns", namespacePolicy.Patterns())

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&corev1.Secret{}},
			},
		},
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "twinward.io",
	})
	if err != nil {
		log.Error("unable to start manager", "error", err)
		os.Exit(1)
	}

	if err := (&controller.SecretCopyReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		NamespacePolicy: namespacePolicy,
		Recorder:        mgr.GetEventRecorderFor("twinward"),
		ContentHashSalt: contentHashSalt,
	}).SetupWithManager(mgr); err != nil {
		log.Error("unable to create controller", "error", err, "controller", "SecretCopy")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error("unable to set up health check", "error", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error("unable to set up ready check", "error", err)
		os.Exit(1)
	}

	log.Info("starting manager", "kind", twinwardv1alpha1.GroupVersion.WithKind("SecretCopy").String())
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error("problem running manager", "error", err)
		os.Exit(1)
	}
}
