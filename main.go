package main

import (
	"flag"
	"fmt"

	"github.com/acorn-io/acorn-istio-plugin/pkg/controller"
	"github.com/acorn-io/acorn-istio-plugin/pkg/scheme"
	"github.com/acorn-io/acorn-istio-plugin/pkg/version"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/acorn-io/baaah/pkg/restconfig"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

var (
	versionFlag                = flag.Bool("version", false, "print version and exit")
	debugImageFlag             = flag.String("debug-image", "ghcr.io/acorn-io/acorn-istio-plugin:main", "Container image used to kill Istio sidecars (needs to have curl installed)")
	allowTrafficFromNamespaces = flag.String("allow-traffic-from-namespaces", "", `Extra namespaces that should be allowed to send traffic to all Acorn apps (comma-separated).
								Pods in these namespaces must be part of the Istio service mesh in order to send traffic.`)
)

func main() {
	flag.Parse()

	logrus.SetLevel(logrus.ErrorLevel)

	fmt.Printf("Version: %s\n", version.Get())
	if *versionFlag {
		return
	}

	config, err := restconfig.Default()
	if err != nil {
		logrus.Fatal(err)
	}
	config.APIPath = "api"
	config.GroupVersion = &corev1.SchemeGroupVersion
	config.NegotiatedSerializer = scheme.Codecs

	k8s := kubernetes.NewForConfigOrDie(config)

	ctx := signals.SetupSignalHandler()
	if err := controller.Start(ctx, controller.Options{
		K8s:                        k8s,
		DebugImage:                 *debugImageFlag,
		AllowTrafficFromNamespaces: *allowTrafficFromNamespaces,
	}); err != nil {
		logrus.Fatal(err)
	}
	<-ctx.Done()
	logrus.Fatal(ctx.Err())
}
