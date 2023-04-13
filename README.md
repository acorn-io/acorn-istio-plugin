# acorn-istio-plugin

acorn-istio-plugin is an Acorn plugin to enable mTLS in Acorn using Istio.

This plugin is responsible for the following:

1. Adding service mesh annotations to Acorn project namespaces, which will then be propagated to app namespaces.
2. Killing Istio sidecars on Acorn jobs, once the other containers in the job have completed.
3. Setting up a STRICT PeerAuthentication for every Acorn app.
4. Setting up a PERMISSIVE PeerAuthentication for every published port in every Acorn app.
5. Setting up AuthorizationPolicies to allow only the needed traffic.
   - The AuthorizationPolicies allow traffic from any IP address to published ports. Acorn's built-in NetworkPolicies are more restrictive than this, and allow only traffic coming from outside the cluster to the published ports, if it is configured properly. See the [docs](https://docs.acorn.io/next/installation/options#kubernetes-networkpolicies) for more information.

## Build

```shell
make build
```

## Args

- `--allow-traffic-from-namespaces`: list of namespaces to allow to connect to all Acorn apps as a single string, comma separated
  - example: `--allow-traffic-from-namespaces "monitoring,kube-system"`

## Prerequisites

Your local Kubernetes cluster needs to have Acorn installed with the following options at a minimum:

```shell
acorn install --set-pod-security-enforce-profile=false --propagate-project-label="istio-injection"
```

Your cluster also needs to have Istio installed. Ingress and egress gateways are not needed, but Istio base and Istiod are required. The easiest way to do this is with Helm:

```shell
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm repo update istio
helm install istio istio/base -n istio-system --create-namespace
helm install istiod istio/istiod -n istio-system
```

## Running the plugin

Run the plugin with Acorn:

```shell
# dev mode:
acorn run --name acorn-istio-plugin --profile dev -i .

# latest main build:
acorn run --name acorn-istio-plugin ghcr.io/acorn-io/acorn-istio-plugin:main

# production:
acorn run --name acorn-istio-plugin ghcr.io/acorn-io/acorn-istio-plugin:prod
```
