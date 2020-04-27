module github.com/fluxcd/starling

go 1.13

require (
	github.com/fluxcd/source-controller v0.0.1-alpha.3
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/cli-runtime v0.17.3
	k8s.io/client-go v0.17.3
	// k8s.io/kubectl v0.17.3
	k8s.io/kubectl v0.17.2
	sigs.k8s.io/cli-utils v0.7.0
	sigs.k8s.io/controller-runtime v0.5.0
)
