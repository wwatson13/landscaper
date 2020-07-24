// Copyright 2020 Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package helm

import (
	"context"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"

	lsv1alpha1 "github.com/gardener/landscaper/pkg/apis/core/v1alpha1"
	containerv1alpha1 "github.com/gardener/landscaper/pkg/apis/deployer/container/v1alpha1"
	"github.com/gardener/landscaper/pkg/landscaper/registry"
	"github.com/gardener/landscaper/pkg/utils"
	ocireg "github.com/gardener/landscaper/pkg/landscaper/registry/oci"
)

func NewActuator(log logr.Logger, config *containerv1alpha1.Configuration) (reconcile.Reconciler, error) {

	reg, err := ocireg.New(log, config.OCI)
	if err != nil {
		return nil, err
	}

	return &actuator{
		log:      log,
		config:   config,
		registry: reg,
	}, nil
}

type actuator struct {
	log    logr.Logger
	c      client.Client
	scheme *runtime.Scheme
	config *containerv1alpha1.Configuration

	registry registry.Registry
}

var _ inject.Client = &actuator{}

var _ inject.Scheme = &actuator{}

// InjectClients injects the current kubernetes registry into the actuator
func (a *actuator) InjectClient(c client.Client) error {
	a.c = c
	return nil
}

// InjectScheme injects the current scheme into the actuator
func (a *actuator) InjectScheme(scheme *runtime.Scheme) error {
	a.scheme = scheme
	return nil
}

func (a *actuator) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	ctx := context.Background()
	defer ctx.Done()
	a.log.Info("reconcile", "resource", req.NamespacedName)

	deployItem := &lsv1alpha1.DeployItem{}
	if err := a.c.Get(ctx, req.NamespacedName, deployItem); err != nil {
		if apierrors.IsNotFound(err) {
			a.log.V(5).Info(err.Error())
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if deployItem.Spec.Type != Type {
		return reconcile.Result{}, nil
	}

	if deployItem.Status.ObservedGeneration == deployItem.Generation {
		return reconcile.Result{}, nil
	}

	if err := a.reconcile(ctx, deployItem); err != nil {
		a.log.Error(err, "unable to reconcile deploy item")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (a *actuator) reconcile(ctx context.Context, deployItem *lsv1alpha1.DeployItem) error {
	container, err := New(a.log, a.c, a.registry, a.config, deployItem)
	if err != nil {
		return err
	}

	if !deployItem.DeletionTimestamp.IsZero() {
		// todo: handle deletion
		return nil
	} else if !utils.HasFinalizer(deployItem, lsv1alpha1.LandscaperFinalizer) {
		controllerutil.AddFinalizer(deployItem, lsv1alpha1.LandscaperFinalizer)
		if err := a.c.Update(ctx, deployItem); err != nil {
			return err
		}
		return nil
	}

	// todo: handle reconcile

	return nil
}