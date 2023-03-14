# acorn-istio-plugin

> This plugin is a WORK IN PROGRESS.

acorn-istio-plugin is an Acorn plugin to enable mTLS in Acorn using Istio.

This plugin is responsible for two things:

1. Adding service mesh annotations to Acorn project namespaces, which will then be propagated to app namespaces.
2. Kill Istio sidecars on Acorn jobs, once the other containers in the job have completed.

## Build

```shell
make build
```

## Development

### Prerequisites

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

### Running the plugin

Run the plugin with Acorn:

```shell
acorn run --name acorn-istio-plugin -i .
```
