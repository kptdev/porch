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

package types

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PodData struct {
	// the OCI image name of the KRM function
	Image string
	// connection to the grpc server running in the fn evaluator pod
	GrpcConnection *grpc.ClientConn
	// namespaced name of the pod
	PodKey *client.ObjectKey
	// namespaced name of the service
	ServiceKey *client.ObjectKey
}

type ConnectionRequest struct {
	// the OCI image name of the KRM function
	Image string
	// ResponseCh is the channel to send the response back.
	ResponseCh chan<- *ConnectionResponse
}

type ConnectionResponse struct {
	PodData
	// the number of currently ongoing and waiting fn evaluations in the pod
	ConcurrentEvaluations *atomic.Int32
	// Err indicates the error that prevents us to allocate a pod for the fn evaluator
	Err error
}

type PodReadyResponse struct {
	PodData
	// Err indicates the error that prevents us to allocate a pod for the fn evaluator
	Err error
}

type DigestAndEntrypoint struct {
	// Digest is a hex string
	Digest string
	// Entrypoint is the Entrypoint of the image
	Entrypoint []string
}

// DockerConfig represents the structure of Docker config.json
type DockerConfig struct {
	Auths map[string]authn.AuthConfig `json:"auths"`
}

// FunctionInfo holds the list of all pod instances for the same KRM function image.
type FunctionInfo struct {
	// status of all Pods belonging to the same KRM function image
	Pods []FunctionPodInfo
	// RoundRobinIdx is used to distribute requests across Pods when all have equal load
	RoundRobinIdx int
}

// FunctionPodInfo represents the state of a single pod instance.
type FunctionPodInfo struct {
	// PodData contains the information about the pod, returned by the podManager
	// It is nil until the pod is actually started
	*PodData
	// Waitlist is used to temporarily store connection requests until the pod is started
	Waitlist []chan<- *ConnectionResponse
	// time of last function evaluation, used by the garbage collector to identify idle pods
	LastActivity time.Time
	// the number of currently ongoing and waiting fn evaluations in the pod
	ConcurrentEvaluations *atomic.Int32
}

// SendResponse sends a reply to the connection request containing the pod data.
// If err != nil it sends `err` as an error response.
// It sends an error response if the pod is not ready yet (this shouldn't happen).
func (pod *FunctionPodInfo) SendResponse(responseCh chan<- *ConnectionResponse, err error) {
	switch {
	case err != nil:
		responseCh <- &ConnectionResponse{
			Err: err,
		}
	case pod.PodData == nil:
		responseCh <- &ConnectionResponse{
			Err: fmt.Errorf("pod is not ready, connection response sent prematurely. This is logical error in the code"),
		}
	default:
		responseCh <- &ConnectionResponse{
			PodData:               *pod.PodData,
			ConcurrentEvaluations: pod.ConcurrentEvaluations,
			Err:                   nil,
		}
	}
}

// WaitlistLen returns with the number of fn evaluations currently handled by the pod
func (pod *FunctionPodInfo) WaitlistLen() int {
	return int(pod.ConcurrentEvaluations.Load())
}
