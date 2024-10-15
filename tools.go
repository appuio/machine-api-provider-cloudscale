//go:build tools
// +build tools

// Package tools is a place to put any tooling dependencies as imports.
// Go modules will be forced to download and install them.
package tools

import (
	// We sync the required manifests to run the controller from here
	_ "github.com/openshift/api/machine/v1beta1/zz_generated.crd-manifests"

	// This is basically KubeBuilder
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
