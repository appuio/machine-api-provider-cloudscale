package v1beta1

import "k8s.io/apimachinery/pkg/runtime/schema"

// +k8s:deepcopy-gen=package
// +k8s:defaulter-gen=TypeMeta
// +k8s:openapi-gen=true

var (
	GroupName    = "machine.appuio.io"
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1beta1"}
)
