package controller

import (
	"testing"

	"github.com/acorn-io/acorn-istio-plugin/pkg/scheme"
	"github.com/acorn-io/baaah/pkg/router/tester"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestHandler_AddLabels(t *testing.T) {
	harness, input, err := tester.FromDir(scheme.Scheme, "testdata/labels")
	if err != nil {
		t.Fatal(err)
	}

	req := tester.NewRequest(t, harness.Scheme, input, harness.Existing...)

	if err := AddLabels(req, nil); err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "enabled", input.GetLabels()[injectionLabel])
}

func TestHandler_KillIstioSidecar(t *testing.T) {
	harness, input, err := tester.FromDir(scheme.Scheme, "testdata/killsidecar")
	if err != nil {
		t.Fatal(err)
	}

	req := tester.NewRequest(t, harness.Scheme, input, harness.Existing...)

	h := Handler{
		client:     fake.NewSimpleClientset(input),
		debugImage: "foo",
	}

	if err = h.KillIstioSidecar(req, nil); err != nil {
		t.Fatal(err)
	}

	expected := corev1.EphemeralContainer{
		TargetContainerName: proxySidecarContainerName,
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            "shutdown-sidecar",
			Image:           "foo",
			ImagePullPolicy: corev1.PullAlways,
			Command: []string{
				"curl", "-X", "POST", "http://localhost:15000/quitquitquit",
			},
		},
	}

	assert.Equal(t, expected, input.(*corev1.Pod).Spec.EphemeralContainers[0])
}

func TestHandler_PoliciesForApp(t *testing.T) {
	h := Handler{}
	tester.DefaultTest(t, scheme.Scheme, "testdata/app", h.PoliciesForApp)
}

func TestHandler_PoliciesForIngress(t *testing.T) {
	tester.DefaultTest(t, scheme.Scheme, "testdata/ingress", PoliciesForIngress)
}

func TestHandler_PoliciesForIngressExternalName(t *testing.T) {
	tester.DefaultTest(t, scheme.Scheme, "testdata/externalname", PoliciesForIngress)
}

func TestHandler_PoliciesForService(t *testing.T) {
	tester.DefaultTest(t, scheme.Scheme, "testdata/service", PoliciesForService)
}

func TestHandler_VirtualServiceForLink(t *testing.T) {
	tester.DefaultTest(t, scheme.Scheme, "testdata/link", VirtualServiceForLink)
}
