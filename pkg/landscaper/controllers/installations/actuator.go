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

package installations

import (
	"context"
	"reflect"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	lsv1alpha1helper "github.com/gardener/landscaper/pkg/apis/core/v1alpha1/helper"
	"github.com/gardener/landscaper/pkg/landscaper/datatype"
	"github.com/gardener/landscaper/pkg/landscaper/installations"
	"github.com/gardener/landscaper/pkg/landscaper/registry"
	"github.com/gardener/landscaper/pkg/utils"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"

	lsv1alpha1 "github.com/gardener/landscaper/pkg/apis/core/v1alpha1"
)

func NewActuator(registry registry.Registry) (reconcile.Reconciler, error) {
	return &actuator{
		registry: registry,
	}, nil
}

type actuator struct {
	log      logr.Logger
	c        client.Client
	scheme   *runtime.Scheme
	registry registry.Registry
}

var _ inject.Client = &actuator{}

var _ inject.Logger = &actuator{}

var _ inject.Scheme = &actuator{}

// InjectClients injects the current kubernetes client into the actuator
func (a *actuator) InjectClient(c client.Client) error {
	a.c = c
	return nil
}

// InjectLogger injects a logging instance into the actuator
func (a *actuator) InjectLogger(log logr.Logger) error {
	a.log = log
	return nil
}

// InjectScheme injects the current scheme into the actuator
func (a *actuator) InjectScheme(scheme *runtime.Scheme) error {
	a.scheme = scheme
	return nil
}

// InjectRegistry injects a Registry into the actuator
func (a *actuator) InjectRegistry(r registry.Registry) error {
	a.registry = r
	return nil
}

func (a *actuator) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	ctx := context.Background()
	defer ctx.Done()
	a.log.Info("reconcile", "resource", req.NamespacedName)

	inst := &lsv1alpha1.Installation{}
	if err := a.c.Get(ctx, req.NamespacedName, inst); err != nil {
		a.log.V(5).Info(err.Error())
		return reconcile.Result{}, err
	}
	old := inst.DeepCopy()

	if inst.DeletionTimestamp.IsZero() && !utils.HasFinalizer(inst, lsv1alpha1.LandscaperFinalizer) {
		controllerutil.AddFinalizer(inst, lsv1alpha1.LandscaperFinalizer)
		if err := a.c.Update(ctx, inst); err != nil {
			return reconcile.Result{Requeue: true}, err
		}
		return reconcile.Result{}, nil
	}

	if lsv1alpha1helper.HasOperation(inst.ObjectMeta, lsv1alpha1.AbortOperation) {
		// todo: handle abort..
	}

	//if lsv1alpha1helper.IsCompletedInstallationPhase(inst.Status.Phase) && inst.Status.ObservedGeneration == inst.Generation {
		// if the inst has the reconcile annotation or if the inst is waiting for dependencies
		// we need to check if all required imports are satisfied.
		if lsv1alpha1helper.HasOperation(inst.ObjectMeta, lsv1alpha1.ReconcileOperation) {
			delete(inst.Annotations, lsv1alpha1.OperationAnnotation)
			if err := a.c.Update(ctx, inst); err != nil {
				return reconcile.Result{Requeue: true}, err
			}
			return reconcile.Result{}, nil
		}

	definition, err := a.registry.GetDefinitionByRef(ctx, inst.Spec.DefinitionRef)
	if err != nil {
		a.log.Error(err, "unable to get definition")
		return reconcile.Result{}, err
	}

	internalInstallation, err := installations.New(inst, definition)
	if err != nil {
		a.log.Error(err, "unable to create internal representation of installation")
		return reconcile.Result{}, err
	}

	datatypeList := &lsv1alpha1.DataTypeList{}
	if err := a.c.List(ctx, datatypeList); err != nil {
		a.log.Error(err, "unable to list all datatypes")
		return reconcile.Result{}, err
	}
	datatypes, err := datatype.CreateDatatypesMap(datatypeList.Items)
	if err != nil {
		a.log.Error(err, "unable to parse datatypes")
		return reconcile.Result{}, err
	}

	instOp, err := installations.NewInstallationOperation(ctx, a.log, a.c, a.scheme, a.registry, datatypes, internalInstallation)
	if err != nil {
		a.log.Error(err, "unable to create installation operation")
		return reconcile.Result{}, err
	}

	if !inst.DeletionTimestamp.IsZero() {
		if err := EnsureDeletion(ctx, instOp); err != nil {
			return reconcile.Result{Requeue: true}, err
		}
		return reconcile.Result{}, nil
	}

	lsConfig, err := instOp.GetLandscapeConfig(ctx, inst.Namespace)
	if err != nil {
		a.log.Error(err, "unable to get landscape configuration")
		return reconcile.Result{}, err
	}

	err = a.Ensure(ctx, instOp, lsConfig, internalInstallation)
	if !reflect.DeepEqual(inst, old) {
		if err2 := a.c.Status().Update(ctx, inst); err2 != nil {
			if err != nil {
				err2 = errors.Wrapf(err, "update error: %s", err.Error())
			}
			return reconcile.Result{}, err2
		}
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}