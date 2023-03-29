package controller

import (
	"context"

	"github.com/acorn-io/acorn-istio-plugin/pkg/scheme"
	"github.com/acorn-io/baaah"
	"k8s.io/client-go/kubernetes"
)

type Options struct {
	K8s                        kubernetes.Interface
	DebugImage                 string
	IngressControllerNamespace string
	AllowTrafficFromNamespaces string
	Local                      bool
}

func Start(ctx context.Context, opt Options) error {
	router, err := baaah.DefaultRouter("istio-controller", scheme.Scheme)
	if err != nil {
		return err
	}

	if err := RegisterRoutes(router, opt.K8s, opt.DebugImage, opt.IngressControllerNamespace, opt.AllowTrafficFromNamespaces, opt.Local); err != nil {
		return err
	}

	return router.Start(ctx)
}
