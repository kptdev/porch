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

package scheduler

import (
	"fmt"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	kptfilesdk "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	pkgerrors "github.com/pkg/errors"
	"k8s.io/klog/v2"
)

func setInitialRenderConditions(kptfileContent string) (string, error) {
	kf, err := kptfilesdk.NewFromString(kptfileContent)
	if err != nil {
		return "", fmt.Errorf("failed to load Kptfile: %w", err)
	}

	err = kf.SetTypedCondition(kptfileapi.Condition{
		Type:    RenderFinishedConditionType,
		Status:  kptfileapi.ConditionFalse,
		Reason:  "RenderScheduled",
		Message: "Render operation is ongoing",
	})
	if err != nil {
		klog.Errorf("Failed to set RenderFinished condition: %v", err)
		return "", fmt.Errorf("failed to set RenderFinished condition: %w", err)
	}

	err = kf.DeleteConditionByType(RenderedConditionType)
	if err != nil {
		return "", fmt.Errorf("failed to remove Rendered condition: %w", err)
	}

	return kf.String(), nil
}

func setFinalRenderConditionsKptfile(kptfileContent string, status *kptfileapi.RenderStatus) (string, error) {
	kptfile, err := kptfilesdk.NewFromString(kptfileContent)
	if err != nil {
		return "", pkgerrors.Wrap(err, "failed to read Kptfile")
	}
	if err := applyFinalRenderConditions(kptfile, status); err != nil {
		return "", err
	}
	return kptfile.String(), nil
}

func setFinalRenderConditions(resources map[string]string, status *kptfileapi.RenderStatus) error {
	kptfile, err := kptfilesdk.NewFromPackage(resources)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to read Kptfile")
	}
	if err := applyFinalRenderConditions(kptfile, status); err != nil {
		return err
	}
	return kptfile.WriteToPackage(resources)
}

func applyFinalRenderConditions(kptfile *kptfilesdk.KptfileKubeObject, status *kptfileapi.RenderStatus) error {
	statusSummary := summarizeRenderStatus(status)

	renderFinishedCondition := kptfileapi.Condition{
		Type:    RenderFinishedConditionType,
		Status:  kptfileapi.ConditionTrue,
		Reason:  "RenderFinished",
		Message: statusSummary,
	}

	renderedCondition := kptfileapi.Condition{
		Type: RenderedConditionType,
	}
	if status.ErrorSummary == "" {
		renderedCondition.Status = kptfileapi.ConditionTrue
		renderedCondition.Reason = "RenderSuccessful"
		renderedCondition.Message = "Rendered successfully"
	} else {
		renderedCondition.Status = kptfileapi.ConditionFalse
		renderedCondition.Reason = "Render failed"
		renderedCondition.Message = statusSummary + ":\n" + status.ErrorSummary
	}

	if err := kptfile.SetTypedCondition(renderedCondition); err != nil {
		return pkgerrors.Wrap(err, "failed to set Rendered condition in Kptfile")
	}

	if err := kptfile.SetTypedCondition(renderFinishedCondition); err != nil {
		return pkgerrors.Wrap(err, "failed to set RenderFinished condition in Kptfile")
	}

	return nil
}

func summarizeRenderStatus(status *kptfileapi.RenderStatus) string {
	return fmt.Sprintf(`Ran %d mutations and %d validations with %d and %d errors respectively`,
		len(status.MutationSteps), len(status.ValidationSteps),
		countErrors(status.MutationSteps), countErrors(status.ValidationSteps))
}

func countErrors(steps []kptfileapi.PipelineStepResult) int {
	sum := 0
	for _, step := range steps {
		// do not count stderr, as sometimes functions just log there
		if step.ExecutionError != "" || step.ExitCode != 0 || len(step.ErrorResults) > 0 {
			sum++
		}
	}
	return sum
}
