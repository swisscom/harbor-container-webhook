/*
Copyright 2020 Indeed.
*/

package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"indeed.com/devops-incubation/harbor-container-webhook/internal/config"
	"indeed.com/devops-incubation/harbor-container-webhook/internal/dynamic"
	"indeed.com/devops-incubation/harbor-container-webhook/internal/static"
	"indeed.com/devops-incubation/harbor-container-webhook/internal/webhook"

	admissionv1 "k8s.io/api/admission/v1"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = admissionv1.AddToScheme(scheme)
	_ = admissionv1beta1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	var configPath string
	flag.StringVar(&configPath, "config", "", "path to the config for the harbor-container-webhook")
	flag.Parse()

	conf, err := config.LoadConfiguration(configPath)
	if err != nil {
		setupLog.Error(err, "unable to read config from "+configPath)
		os.Exit(1)
	}
	fmt.Printf("config: %#v\n", conf)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     conf.MetricsAddr,
		HealthProbeBindAddress: conf.HealthAddr,
		Port:                   conf.Port,
		LeaderElection:         false,
		CertDir:                conf.CertDir,
	})
	if err != nil {
		setupLog.Error(err, "unable to start harbor-container-webhook")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	var transformer webhook.ContainerTransformer
	if conf.Dynamic != nil {
		transformer = dynamic.NewTransformer(*conf.Dynamic)
	} else if conf.Static != nil {
		transformer = static.NewTransformer(*conf.Static)
	} else {
		setupLog.Error(errors.New("no static or dynamic config blocks supplied"), "unable to start harbor-container-webhook")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("health-ping", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable add a liveness check to harbor-container-webhook")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("ready-ping", func(_ *http.Request) error {
		return transformer.Ready()
	}); err != nil {
		setupLog.Error(err, "Unable add a readiness check to harbor-container-webhook")
		os.Exit(1)
	}

	decoder, _ := admission.NewDecoder(scheme)
	mutate := webhook.PodContainerProxier{
		Client:      mgr.GetClient(),
		Decoder:     decoder,
		Transformer: transformer,
	}

	mgr.GetWebhookServer().Register("/webhook-v1-pod", &ctrlwebhook.Admission{Handler: &mutate})

	setupLog.Info("starting harbor-container-webhook")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running harbor-container-webhook")
		os.Exit(1)
	}
}
