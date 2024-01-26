/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

const (
	// Every feature gate should add method here following this template:
	//
	// // owner: @username
	// // alpha: v1.4
	// MyFeature featuregate.Feature = "MyFeature"
	//
	// Feature gates should be listed in alphabetical, case-sensitive
	// (upper before any lower case character) order. This reduces the risk
	// of code conflicts because changes are more likely to be scattered
	// across the file.

	// owner: @jefftree @alexzielenski
	// alpha: v1.26
	//
	// Enables an single HTTP endpoint /discovery/<version> which supports native HTTP
	// caching with ETags containing all APIResources known to the apiserver.
	AggregatedDiscoveryEndpoint featuregate.Feature = "AggregatedDiscoveryEndpoint"

	// owner: @smarterclayton
	// alpha: v1.8
	// beta: v1.9
	//
	// Allow API clients to retrieve resource lists in chunks rather than
	// all at once.
	APIListChunking featuregate.Feature = "APIListChunking"

	// owner: @MikeSpreitzer @yue9944882
	// alpha: v1.18
	// beta: v1.20
	//
	// Enables managing request concurrency with prioritization and fairness at each server.
	// The FeatureGate was introduced in release 1.15 but the feature
	// was not really implemented before 1.18.
	APIPriorityAndFairness featuregate.Feature = "APIPriorityAndFairness"

	// owner: @ilackams
	// alpha: v1.7
	// beta: v1.16
	//
	// Enables compression of REST responses (GET and LIST only)
	APIResponseCompression featuregate.Feature = "APIResponseCompression"

	// owner: @roycaihw
	// alpha: v1.20
	//
	// Assigns each kube-apiserver an ID in a cluster.
	APIServerIdentity featuregate.Feature = "APIServerIdentity"

	// owner: @dashpole
	// alpha: v1.22
	//
	// Add support for distributed tracing in the API Server
	APIServerTracing featuregate.Feature = "APIServerTracing"

	// owner: @tallclair
	// alpha: v1.7
	// beta: v1.8
	// GA: v1.12
	//
	// AdvancedAuditing enables a much more general API auditing pipeline, which includes support for
	// pluggable output backends and an audit policy specifying how different requests should be
	// audited.
	AdvancedAuditing featuregate.Feature = "AdvancedAuditing"

	// owner: @cici37 @jpbetz
	// kep: http://kep.k8s.io/3488
	// alpha: v1.26
	//
	// Enables expression validation in Admission Control
	ValidatingAdmissionPolicy featuregate.Feature = "ValidatingAdmissionPolicy"

	// owner: @cici37
	// kep: https://kep.k8s.io/2876
	// alpha: v1.23
	// beta: v1.25
	//
	// Enables expression validation for Custom Resource
	CustomResourceValidationExpressions featuregate.Feature = "CustomResourceValidationExpressions"

	// owner: @apelisse
	// alpha: v1.12
	// beta: v1.13
	// stable: v1.18
	//
	// Allow requests to be processed but not stored, so that
	// validation, merging, mutation can be tested without
	// committing.
	DryRun featuregate.Feature = "DryRun"

	// owner: @wojtek-t
	// alpha: v1.20
	// beta: v1.21
	// GA: v1.24
	//
	// Allows for updating watchcache resource version with progress notify events.
	EfficientWatchResumption featuregate.Feature = "EfficientWatchResumption"

	// owner: @aramase
	// kep: https://kep.k8s.io/3299
	// alpha: v1.25
	//
	// Enables KMS v2 API for encryption at rest.
	KMSv2 featuregate.Feature = "KMSv2"

	// owner: @jiahuif
	// kep: https://kep.k8s.io/2887
	// alpha: v1.23
	// beta: v1.24
	//
	// Enables populating "enum" field of OpenAPI schemas
	// in the spec returned from kube-apiserver.
	OpenAPIEnums featuregate.Feature = "OpenAPIEnums"

	// owner: @jefftree
	// kep: https://kep.k8s.io/2896
	// alpha: v1.23
	// beta: v1.24
	//
	// Enables kubernetes to publish OpenAPI v3
	OpenAPIV3 featuregate.Feature = "OpenAPIV3"

	// owner: @caesarxuchao
	// alpha: v1.15
	// beta: v1.16
	//
	// Allow apiservers to show a count of remaining items in the response
	// to a chunking list request.
	RemainingItemCount featuregate.Feature = "RemainingItemCount"

	// owner: @wojtek-t
	// alpha: v1.16
	// beta: v1.20
	// GA: v1.24
	//
	// Deprecates and removes SelfLink from ObjectMeta and ListMeta.
	RemoveSelfLink featuregate.Feature = "RemoveSelfLink"

	// owner: @apelisse, @lavalamp
	// alpha: v1.14
	// beta: v1.16
	// stable: v1.22
	//
	// Server-side apply. Merging happens on the server.
	ServerSideApply featuregate.Feature = "ServerSideApply"

	// owner: @kevindelgado
	// kep: https://kep.k8s.io/2885
	// alpha: v1.23
	// beta: v1.24
	//
	// Enables server-side field validation.
	ServerSideFieldValidation featuregate.Feature = "ServerSideFieldValidation"

	// owner: @caesarxuchao @roycaihw
	// alpha: v1.20
	//
	// Enable the storage version API.
	StorageVersionAPI featuregate.Feature = "StorageVersionAPI"

	// owner: @caesarxuchao
	// alpha: v1.14
	// beta: v1.15
	//
	// Allow apiservers to expose the storage version hash in the discovery
	// document.
	StorageVersionHash featuregate.Feature = "StorageVersionHash"

	// owner: @wojtek-t
	// alpha: v1.15
	// beta: v1.16
	// GA: v1.17
	//
	// Enables support for watch bookmark events.
	WatchBookmark featuregate.Feature = "WatchBookmark"
)

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultKubernetesFeatureGates))
}

// defaultKubernetesFeatureGates consists of all known Kubernetes-specific feature keys.
// To add a new feature, define a key for it above and add it here. The features will be
// available throughout Kubernetes binaries.
var defaultKubernetesFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	AggregatedDiscoveryEndpoint: {Default: false, PreRelease: featuregate.Alpha},

	APIListChunking: {Default: true, PreRelease: featuregate.Beta},

	APIPriorityAndFairness: {Default: true, PreRelease: featuregate.Beta},

	APIResponseCompression: {Default: true, PreRelease: featuregate.Beta},

	APIServerIdentity: {Default: true, PreRelease: featuregate.Beta},

	APIServerTracing: {Default: false, PreRelease: featuregate.Alpha},

	AdvancedAuditing: {Default: true, PreRelease: featuregate.GA},

	ValidatingAdmissionPolicy: {Default: false, PreRelease: featuregate.Alpha},

	CustomResourceValidationExpressions: {Default: true, PreRelease: featuregate.Beta},

	DryRun: {Default: true, PreRelease: featuregate.GA, LockToDefault: true}, // remove in 1.28

	EfficientWatchResumption: {Default: true, PreRelease: featuregate.GA, LockToDefault: true},

	KMSv2: {Default: false, PreRelease: featuregate.Alpha},

	OpenAPIEnums: {Default: true, PreRelease: featuregate.Beta},

	OpenAPIV3: {Default: true, PreRelease: featuregate.Beta},

	RemainingItemCount: {Default: true, PreRelease: featuregate.Beta},

	RemoveSelfLink: {Default: true, PreRelease: featuregate.GA, LockToDefault: true},

	ServerSideApply: {Default: true, PreRelease: featuregate.GA, LockToDefault: true}, // remove in 1.29

	ServerSideFieldValidation: {Default: true, PreRelease: featuregate.Beta},

	StorageVersionAPI: {Default: false, PreRelease: featuregate.Alpha},

	StorageVersionHash: {Default: true, PreRelease: featuregate.Beta},

	WatchBookmark: {Default: true, PreRelease: featuregate.GA, LockToDefault: true},
}
