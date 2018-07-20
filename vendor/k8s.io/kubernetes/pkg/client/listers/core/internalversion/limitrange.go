/*
Copyright 2018 The Kubernetes Authors.

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

// Code generated by lister-gen. DO NOT EDIT.

package internalversion

import (
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	core "k8s.io/kubernetes/pkg/apis/core"
)

// LimitRangeLister helps list LimitRanges.
type LimitRangeLister interface {
	// List lists all LimitRanges in the indexer.
	List(selector labels.Selector) (ret []*core.LimitRange, err error)
	// LimitRanges returns an object that can list and get LimitRanges.
	LimitRanges(namespace string) LimitRangeNamespaceLister
	LimitRangeListerExpansion
}

// limitRangeLister implements the LimitRangeLister interface.
type limitRangeLister struct {
	indexer cache.Indexer
}

// NewLimitRangeLister returns a new LimitRangeLister.
func NewLimitRangeLister(indexer cache.Indexer) LimitRangeLister {
	return &limitRangeLister{indexer: indexer}
}

// List lists all LimitRanges in the indexer.
func (s *limitRangeLister) List(selector labels.Selector) (ret []*core.LimitRange, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*core.LimitRange))
	})
	return ret, err
}

// LimitRanges returns an object that can list and get LimitRanges.
func (s *limitRangeLister) LimitRanges(namespace string) LimitRangeNamespaceLister {
	return limitRangeNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// LimitRangeNamespaceLister helps list and get LimitRanges.
type LimitRangeNamespaceLister interface {
	// List lists all LimitRanges in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*core.LimitRange, err error)
	// Get retrieves the LimitRange from the indexer for a given namespace and name.
	Get(name string) (*core.LimitRange, error)
	LimitRangeNamespaceListerExpansion
}

// limitRangeNamespaceLister implements the LimitRangeNamespaceLister
// interface.
type limitRangeNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all LimitRanges in the indexer for a given namespace.
func (s limitRangeNamespaceLister) List(selector labels.Selector) (ret []*core.LimitRange, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*core.LimitRange))
	})
	return ret, err
}

// Get retrieves the LimitRange from the indexer for a given namespace and name.
func (s limitRangeNamespaceLister) Get(name string) (*core.LimitRange, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(core.Resource("limitrange"), name)
	}
	return obj.(*core.LimitRange), nil
}
