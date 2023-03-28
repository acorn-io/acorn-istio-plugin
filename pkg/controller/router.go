package controller

import (
	"github.com/acorn-io/baaah/pkg/router"
	corev1 "k8s.io/api/core/v1"
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
)

func RegisterRoutes(router *router.Router, client kubernetes.Interface, debugImage string) error {
	h := Handler{
		client:     client,
		debugImage: debugImage,
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

	router.Type(&corev1.Namespace{}).Selector(projectSelector).HandlerFunc(AddLabels)
	router.Type(&corev1.Namespace{}).Selector(appNamespaceSelector).HandlerFunc(PeerAuthenticationForApp)
	router.Type(&corev1.Pod{}).Selector(managedSelector).Selector(jobSelector).HandlerFunc(h.KillIstioSidecar)
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
