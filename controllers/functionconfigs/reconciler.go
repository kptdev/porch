// Copyright 2026 The kpt Authors
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

package functionconfigs

import (
	"context"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	BaseFinalizer           = "config.porch.kpt.dev/functionconfig"
	ServerFinalizer         = BaseFinalizer + "-porch-server"
	FunctionRunnerFinalizer = BaseFinalizer + "-function-runner"
	ControllerFinalizer     = BaseFinalizer + "-controller"
)

type ReconcilerFor string

const (
	ReconcilerForFunctionRunner ReconcilerFor = "function-runner"
	ReconcilerForServer         ReconcilerFor = "server"
	ReconcilerForController     ReconcilerFor = "controller"
)

type FunctionConfigReconciler struct {
	Client              client.Client
	FunctionConfigStore *FunctionConfigStore
	// For indicates which component the reconciler is collecting the configs for
	// TODO: remove after merging of function-runner into server
	For ReconcilerFor
}

func (r *FunctionConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, finalErr error) {
	klog.Infof("FunctionConfig %q changed", req.NamespacedName)
	obj := &configapi.FunctionConfig{}
	err := r.Client.Get(ctx, req.NamespacedName, obj)
	if apierrors.IsNotFound(err) {
		r.FunctionConfigStore.DeleteByObjName(req.NamespacedName)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if obj.DeletionTimestamp != nil {
		if err := r.removeFinalizer(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}

		r.FunctionConfigStore.Delete(obj.Spec.Image)
		return ctrl.Result{}, nil
	}

	if err := r.addFinalizer(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		patch := client.MergeFrom(obj.DeepCopy())

		if finalErr != nil {
			obj.Status.Error = finalErr.Error()
		} else {
			obj.Status.Error = ""
			switch r.For {
			case ReconcilerForFunctionRunner:
				obj.Status.FunctionRunnerObservedGeneration = obj.Generation
			case ReconcilerForServer:
				obj.Status.ApiServerObservedGeneration = obj.Generation
			case ReconcilerForController:
				obj.Status.ControllerObservedGeneration = obj.Generation
			}
		}

		if err := r.Client.Status().Patch(ctx, obj, patch); err != nil {
			klog.Errorf("Failed to update status of FunctionConfig %q: %v", obj.Name, err)
			if finalErr == nil {
				finalErr = err
			}
		}
	}()

	if err := r.FunctionConfigStore.Store(obj); err != nil {
		klog.Errorf("Failed to store FunctionConfig %q: %v", obj.Name, err)
		// TODO: we shouldn't have a requeue loop here if we can't insert into the cache, but if the user deletes the
		// conflicting config, then this one won't be applied until a new event
		return ctrl.Result{}, IgnoreConflict(err)
	}

	return ctrl.Result{}, nil
}

func (r *FunctionConfigReconciler) removeFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
	patch := client.MergeFrom(obj.DeepCopy())

	switch r.For {
	case ReconcilerForFunctionRunner:
		controllerutil.RemoveFinalizer(obj, FunctionRunnerFinalizer)
	case ReconcilerForServer:
		controllerutil.RemoveFinalizer(obj, ServerFinalizer)
	case ReconcilerForController:
		controllerutil.RemoveFinalizer(obj, ControllerFinalizer)
	}

	if err := r.Client.Patch(ctx, obj, patch); err != nil {
		klog.Errorf("Failed to remove finalizer from FunctionConfig %q: %v", obj.Name, err)
		return err
	}

	return nil
}

func (r *FunctionConfigReconciler) addFinalizer(ctx context.Context, obj *configapi.FunctionConfig) error {
	patch := client.MergeFrom(obj.DeepCopy())

	updated := false
	switch r.For {
	case ReconcilerForFunctionRunner:
		updated = controllerutil.AddFinalizer(obj, FunctionRunnerFinalizer)
	case ReconcilerForServer:
		updated = controllerutil.AddFinalizer(obj, ServerFinalizer)
	case ReconcilerForController:
		updated = controllerutil.AddFinalizer(obj, ControllerFinalizer)
	}

	if updated {
		if err := r.Client.Patch(ctx, obj, patch); err != nil {
			klog.Errorf("Failed to add finalizer to FunctionConfig %q: %v", obj.Name, err)
			return err
		}
	}

	return nil
}

func IgnoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
