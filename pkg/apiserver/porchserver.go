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

package apiserver

import (
	"context"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// PorchServer contains state for a Kubernetes cluster master/api server.
type PorchServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer

	leaderElect bool
	coreClient  client.WithWatch
	cache       cachetypes.Cache
}

var _ manager.Runnable = &PorchServer{}
var _ manager.LeaderElectionRunnable = &PorchServer{}

func (s *PorchServer) Start(ctx context.Context) error {
	return s.GenericAPIServer.PrepareRun().RunWithContext(ctx)
}

func (s *PorchServer) NeedLeaderElection() bool {
	return s.leaderElect
}
