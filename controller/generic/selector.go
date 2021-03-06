/*
Copyright 2019 The MayaData Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package generic

import (
	"fmt"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	"openebs.io/metac/apis/metacontroller/v1alpha1"
	"openebs.io/metac/controller/common"
	dynamicdiscovery "openebs.io/metac/dynamic/discovery"
)

// Selector is the placeholder to store various selection criteria
// It is a single structure that supports multiple selector strategies
//
// TODO (@amitkumardas) Check if this can be a common package
type Selector struct {
	nameSelectors       NameSelectorsByGK
	labelSelectors      LabelSelectorsByGK
	annotationSelectors AnnotationSelectorsByGK
}

// NameSelectorsByGK acts as the registrar of NameSelectors anchored by
// apiversion and kind
type NameSelectorsByGK map[string]v1alpha1.NameSelector

// Set registers the given NameSelector based on the given version
// and kind
func (m NameSelectorsByGK) Set(group, kind string, selector v1alpha1.NameSelector) {
	m[makeSelectorKeyFromGK(group, kind)] = selector
}

// Get returns the NameSelector from the registrar based on the
// given version and kind
func (m NameSelectorsByGK) Get(group, kind string) v1alpha1.NameSelector {
	return m[makeSelectorKeyFromGK(group, kind)]
}

// LabelSelectorsByGK acts as the registrar of LabelSelectors anchored by
// api group and kind
type LabelSelectorsByGK map[string]labels.Selector

// Set registers the given LabelSelector based on the given version
// and kind
func (m LabelSelectorsByGK) Set(group, kind string, selector labels.Selector) {
	m[makeSelectorKeyFromGK(group, kind)] = selector
}

// Get returns the LabelSelector from the registrar based on the
// given version and kind
func (m LabelSelectorsByGK) Get(group, kind string) labels.Selector {
	return m[makeSelectorKeyFromGK(group, kind)]
}

// AnnotationSelectorsByGK acts as the registrar of AnnotationSelectors
// anchored by api group and kind
type AnnotationSelectorsByGK map[string]labels.Selector

// Set registers the given AnnotationSelector based on the given version
// and kind
func (m AnnotationSelectorsByGK) Set(group, kind string, selector labels.Selector) {
	m[makeSelectorKeyFromGK(group, kind)] = selector
}

// Get returns the AnnotationSelector from the registrar based on the
// given version and resource
func (m AnnotationSelectorsByGK) Get(group, kind string) labels.Selector {
	return m[makeSelectorKeyFromGK(group, kind)]
}

// SelectorOption is a typed function used to build
// an instance of selector
//
// This pattern of building an instance is known as
// "functional options" pattern
type SelectorOption func(*Selector) error

// FromGCtlResourceSelectRequirements builds the Selector instance
// based on GenericControllerResource's select requirements
func FromGCtlResourceSelectRequirements(
	resourceMgr *dynamicdiscovery.APIResourceManager,
	gctlResource v1alpha1.GenericControllerResource,
) SelectorOption {
	return func(s *Selector) error {
		var err error

		if resourceMgr == nil {
			return errors.Errorf("Selector failed: Nil resource manager")
		}

		// fetch the resource from the discovered set
		gctlResObj := resourceMgr.GetByResource(gctlResource.APIVersion, gctlResource.Resource)
		if gctlResObj == nil {
			return errors.Errorf(
				"Selector failed: Can't find resource %s/%s",
				gctlResource.APIVersion, gctlResource.Resource,
			)
		}

		lblSel := labels.Everything()
		// Convert the label selector to the internal form.
		if gctlResource.LabelSelector != nil {
			lblSel, err = metav1.LabelSelectorAsSelector(gctlResource.LabelSelector)
			if err != nil {
				return errors.Wrapf(
					err, "Label selector for %s/%s failed",
					gctlResource.APIVersion, gctlResource.Resource,
				)
			}
		}
		s.labelSelectors.Set(gctlResObj.Group, gctlResObj.Kind, lblSel)

		annSel := labels.Everything()
		// Convert the annotation selector to a label selector, then to
		// internal form.
		if gctlResource.AnnotationSelector != nil {
			labelSelector := &metav1.LabelSelector{
				MatchLabels:      gctlResource.AnnotationSelector.MatchAnnotations,
				MatchExpressions: gctlResource.AnnotationSelector.MatchExpressions,
			}
			annSel, err = metav1.LabelSelectorAsSelector(labelSelector)
			if err != nil {
				return errors.Wrapf(
					err, "Annotation selector for %s/%s failed",
					gctlResource.APIVersion, gctlResource.Resource,
				)
			}
		}
		s.annotationSelectors.Set(gctlResObj.Group, gctlResObj.Kind, annSel)

		// Set NameSelector to everything if it is empty
		//
		// NOTE:
		// 	Empty nameselector evaluates to true for any names
		nameSel := gctlResource.NameSelector
		if nameSel == nil {
			nameSel = []string{}
		}
		s.nameSelectors.Set(gctlResObj.Group, gctlResObj.Kind, nameSel)

		return nil
	}
}

// NewSelector returns a new instance of Selector
func NewSelector(options ...SelectorOption) (*Selector, error) {
	s := &Selector{}

	// init all the selector strategies
	//
	// NOTE:
	// 	Ensure that each option point to different resource kind
	s.nameSelectors = NameSelectorsByGK(make(map[string]v1alpha1.NameSelector))
	s.labelSelectors = LabelSelectorsByGK(make(map[string]labels.Selector))
	s.annotationSelectors = AnnotationSelectorsByGK(make(map[string]labels.Selector))

	for _, o := range options {
		err := o(s)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Matches returns true if the provided unstruct instance match this
// selector settings
func (s *Selector) Matches(obj *unstructured.Unstructured) bool {
	// Look up the label and annotation selectors for this object.
	// Use only Group and Kind. Ignore Version.
	apiGroup, _ := common.ParseAPIVersionToGroupVersion(obj.GetAPIVersion())

	nameSelector := s.nameSelectors.Get(apiGroup, obj.GetKind())
	labelSelector := s.labelSelectors.Get(apiGroup, obj.GetKind())
	annotationSelector := s.annotationSelectors.Get(apiGroup, obj.GetKind())

	// It must match all selectors.
	return labelSelector.Matches(labels.Set(obj.GetLabels())) &&
		annotationSelector.Matches(labels.Set(obj.GetAnnotations())) &&
		nameSelector.ContainsOrTrue(obj.GetName())
}

// makeSelectorKeyFromGK returns a formatted string suitable to be
// used as a key of form 'kind.apigroup'
//
// The returned key is based on a combination of api group & kind
func makeSelectorKeyFromGK(apiGroup, kind string) string {
	return fmt.Sprintf("%s.%s", kind, apiGroup)
}
