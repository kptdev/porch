// Copyright 2022 The kpt Authors
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

package main

import (
	"os"

	porchotel "github.com/kptdev/porch/internal/otel"
	"github.com/kptdev/porch/pkg/cmd/server"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	code := run()
	os.Exit(code)
}

func run() int {
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := genericapiserver.SetupSignalContext()
	err := porchotel.SetupOpenTelemetry(ctx)
	if err != nil {
		genericapiserver.RequestShutdown()
		klog.Errorf("%v\n", err)
		return 1
	}
	options := server.NewPorchServerOptions(os.Stdout, os.Stderr)
	cmd := server.NewCommandStartPorchServer(ctx, options)
	code := cli.Run(cmd)
	return code
}
