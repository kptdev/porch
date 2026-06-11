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

package evaluator

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/fn/runtime"
	pb "github.com/kptdev/porch/func/proto"
	. "github.com/kptdev/porch/func/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testImageName = "test-image"
)

type fakeLister struct {
	tags map[string][]string
	err  string
}

func (frc *fakeLister) List(_ context.Context, image string) ([]string, error) {
	if frc.err != "" {
		return nil, errors.New(frc.err)
	}

	if frc.tags == nil {
		return []string{}, nil
	}

	tags, ok := frc.tags[image]
	if !ok {
		return []string{}, nil
	}

	return tags, nil
}

func (frc *fakeLister) Name() string {
	return "fake"
}

func TestTagResolution(t *testing.T) {
	t.Run("failed to list tags for the image", func(t *testing.T) {
		const sleep = 2 * time.Second

		ctx := t.Context()

		addr, err := startFakeServer(ctx, t, sleep, "")
		require.NoError(t, err, "failed to start fake server")

		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithNoProxy(),
		)
		require.NoError(t, err, "grpc dial failed")

		reqCh := make(chan *ConnectionRequest, 2)
		go func() {
			counter := &atomic.Int32{}
			for req := range reqCh {
				req.ResponseCh <- &ConnectionResponse{
					PodData: PodData{
						Image:          req.Image,
						GrpcConnection: conn,
						PodKey:         ptr.To(client.ObjectKey{}),
					},
					ConcurrentEvaluations: counter,
					Err:                   nil,
				}
			}
		}()

		pe := &podEvaluator{requestCh: reqCh,
			podCacheManager: &podCacheManager{
				podManager: &podManager{
					tagResolver: runtime.TagResolver{},
					// The lister got uninitialized, which causes failure when trying to list the tags for the image.
				},
			},
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(`{}`),
			Image:        defaultKRMImagePrefix + testImageName,
			Tag:          ">= 0.4.0 < 0.5.0",
		}
		_, err = pe.EvaluateFunction(ctx, req)
		assert.Contains(t, err.Error(), fmt.Sprintf("failed to resolve tag for image %q", req.Image))
	})
	t.Run("successful tag listing and execution", func(t *testing.T) {
		const sleep = 0 * time.Second

		ctx := t.Context()

		addr, err := startFakeServer(ctx, t, sleep, "")
		require.NoError(t, err, "failed to start fake server")

		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithNoProxy(),
		)
		require.NoError(t, err, "grpc dial failed")

		reqCh := make(chan *ConnectionRequest, 2)
		go func() {
			counter := &atomic.Int32{}
			for req := range reqCh {
				req.ResponseCh <- &ConnectionResponse{
					PodData: PodData{
						Image:          req.Image,
						GrpcConnection: conn,
						PodKey:         ptr.To(client.ObjectKey{}),
					},
					ConcurrentEvaluations: counter,
					Err:                   nil,
				}
			}
		}()

		pcm := &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{
					Listers: []runtime.TagLister{
						&fakeLister{
							err: "",
							tags: map[string][]string{
								defaultKRMImagePrefix + testImageName: {"v0.3.0", "v0.4.0", "v0.4.1", "v0.4.2", "v0.5.0"},
							},
						}},
				},
			},
		}

		pe := &podEvaluator{
			requestCh:       reqCh,
			podCacheManager: pcm,
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(`{}`),
			Image:        defaultKRMImagePrefix + testImageName,
			Tag:          ">= 0.4.0 < 0.5.0",
		}

		resp, err := pe.EvaluateFunction(ctx, req)

		assert.NotNil(t, resp)
		assert.NoError(t, err)
	})
}
