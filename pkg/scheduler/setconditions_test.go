package scheduler

import (
	"testing"

	fnresult "github.com/kptdev/kpt/api/fnresult/v1"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	kptfilesdk "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddRenderStatusConditionsToKptfileExitCodeNotNil(t *testing.T) {
	// given
	resources := map[string]string{
		"Kptfile": `
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test
`,
	}
	status := kptfilev1.RenderStatus{}
	item1 := kptfilev1.PipelineStepResult{
		Image:        "test-image-1",
		ExitCode:     0,
		ErrorResults: []fnresult.ResultItem{{Severity: "error"}},
	}
	item2 := kptfilev1.PipelineStepResult{
		Image:    "test-image-2",
		ExitCode: 1,
	}
	status.MutationSteps = []kptfilev1.PipelineStepResult{item1, item2}

	// when
	err := setFinalRenderConditions(resources, &status)

	// then
	assert.NoError(t, err)
	kf, err := kptfilesdk.NewFromPackage(resources)
	require.NoError(t, err)
	renderFinishedCondition, err := kf.GetTypedCondition(RenderFinishedConditionType)
	require.NoError(t, err)
	assert.Equal(t, kptfilev1.ConditionTrue, renderFinishedCondition.Status)
	assert.Contains(t, renderFinishedCondition.Message, "2 and 0 errors")
}

func TestAddRenderStatusConditionsToKptfile_KptfileNotfound(t *testing.T) {
	// given
	status := kptfilev1.RenderStatus{}

	// when
	err := setFinalRenderConditions(map[string]string{}, &status)

	// then
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read Kptfile")
}

func TestSetFinalRenderConditionsKptfile(t *testing.T) {
	updated, err := setFinalRenderConditionsKptfile(minimalKptfile(), &kptfilev1.RenderStatus{})
	require.NoError(t, err)
	assert.Contains(t, updated, "RenderFinished")
}

func TestSetFinalRenderConditionsKptfile_InvalidKptfile(t *testing.T) {
	_, err := setFinalRenderConditionsKptfile("not-a-kptfile", &kptfilev1.RenderStatus{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read Kptfile")
}

func TestSetFinalRenderConditions_WithErrorSummary(t *testing.T) {
	resources := map[string]string{"Kptfile": minimalKptfile()}
	err := setFinalRenderConditions(resources, &kptfilev1.RenderStatus{ErrorSummary: "render failed"})
	require.NoError(t, err)

	kf, err := kptfilesdk.NewFromPackage(resources)
	require.NoError(t, err)
	rendered, err := kf.GetTypedCondition(RenderedConditionType)
	require.NoError(t, err)
	assert.Equal(t, kptfilev1.ConditionFalse, rendered.Status)
}

func TestSetInitialRenderConditions(t *testing.T) {
	// given
	kptfile := minimalKptfile()

	// when
	updated, err := setInitialRenderConditions(kptfile)

	// then
	require.NoError(t, err)
	kf, err := kptfilesdk.NewFromPackage(map[string]string{"Kptfile": updated})
	require.NoError(t, err)
	renderFinishedCondition, err := kf.GetTypedCondition(RenderFinishedConditionType)
	require.NoError(t, err)
	require.NotNil(t, renderFinishedCondition)
	assert.Equal(t, kptfilev1.ConditionFalse, renderFinishedCondition.Status)
	renderedCondition, err := kf.GetTypedCondition(RenderedConditionType)
	require.NoError(t, err)
	assert.Equal(t, kptfilev1.ConditionStatus(""), renderedCondition.Status)
}

func TestSetInitialRenderConditions_InvalidKptfile(t *testing.T) {
	_, err := setInitialRenderConditions("not-valid")
	assert.Error(t, err)
}

func TestSummarizeRenderStatusAndCountErrors(t *testing.T) {
	status := &kptfilev1.RenderStatus{
		MutationSteps: []kptfilev1.PipelineStepResult{
			{ExitCode: 1},
			{ExecutionError: "KRM function execution failed: container exited with status 1"},
			{ExitCode: 0, Stderr: "warning: skipping optional validation step"},
		},
		ValidationSteps: []kptfilev1.PipelineStepResult{
			{ErrorResults: []fnresult.ResultItem{{Severity: "error"}}},
		},
	}
	summary := summarizeRenderStatus(status)
	assert.Contains(t, summary, "Ran 3 mutations and 1 validations with 2 and 1 errors respectively")
	assert.Equal(t, 2, countErrors(status.MutationSteps))
	assert.Equal(t, 1, countErrors(status.ValidationSteps))
}
