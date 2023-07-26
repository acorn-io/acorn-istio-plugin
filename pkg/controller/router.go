package controller

import (
	"github.com/acorn-io/baaah/pkg/router"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
)

var (
	projectSelector = labels.SelectorFromSet(map[string]string{
		"acorn.io/project": "true",
	})

	acornManagedSelector = labels.SelectorFromSet(map[string]string{
		"acorn.io/managed": "true",
	})

	appNameLabel      = "acorn.io/app-name"
	appNamespaceLabel = "acorn.io/app-namespace"
	jobLabel          = "acorn.io/job-name"
	linkLabel         = "acorn.io/link-name"
)

func RegisterRoutes(router *router.Router, client kubernetes.Interface, debugImage, allowTrafficFromNamespaces string) error {
	h := Handler{
		client:                     client,
		debugImage:                 debugImage,
		allowTrafficFromNamespaces: allowTrafficFromNamespaces,
	}

	managedSelector, err := getAcornManagedSelector()
	if err != nil {
		return err
	}

	jobSelector, err := getJobPodSelector()
	if err != nil {
		return err
	}

	appNamespaceSelector, err := getAppNamespaceSelector()
	if err != nil {
		return err
	}

	linkSelector, err := getLinkSelector()
	if err != nil {
		return err
	}

	router.Type(&corev1.Namespace{}).Selector(projectSelector).HandlerFunc(AddLabels)
	router.Type(&corev1.Namespace{}).Selector(appNamespaceSelector).HandlerFunc(h.PoliciesForApp)
	router.Type(&netv1.Ingress{}).Selector(managedSelector).HandlerFunc(PoliciesForIngress)
	router.Type(&securityv1beta1.PeerAuthentication{}).Selector(managedSelector).HandlerFunc(GCOrphans)
	router.Type(&securityv1beta1.AuthorizationPolicy{}).Selector(managedSelector).HandlerFunc(GCOrphans)
	router.Type(&corev1.Service{}).Selector(managedSelector).HandlerFunc(PoliciesForService)
	router.Type(&corev1.Pod{}).Selector(managedSelector).Selector(jobSelector).HandlerFunc(h.KillIstioSidecar)
	router.Type(&corev1.Service{}).Selector(linkSelector).HandlerFunc(VirtualServiceForLink)

	// Delete existing AuthorizationPolicies
	router.Type(&securityv1beta1.AuthorizationPolicy{}).HandlerFunc(DoNothing)
	return nil
}

func getAcornManagedSelector() (labels.Selector, error) {
	r1, err := labels.NewRequirement(appNameLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	r2, err := labels.NewRequirement(appNamespaceLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	acornManagedSelector.Add(*r1, *r2)
	return acornManagedSelector, nil
}

func getJobPodSelector() (labels.Selector, error) {
	r1, err := labels.NewRequirement(jobLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*r1), nil
}

func getAppNamespaceSelector() (labels.Selector, error) {
	req, err := labels.NewRequirement(appNamespaceLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*req), nil
}

func getLinkSelector() (labels.Selector, error) {
	req, err := labels.NewRequirement(linkLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*req), nil
}
