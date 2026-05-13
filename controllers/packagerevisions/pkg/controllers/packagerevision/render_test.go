package packagerevision

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func prWithRenderState(annotation, observed, rendering, creationSource string, renderedStatus metav1.ConditionStatus) *porchv1alpha2.PackageRevision {
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft,
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			ObservedPrrResourceVersion:  observed,
			RenderingPrrResourceVersion: rendering,
			CreationSource:              creationSource,
		},
	}
	if annotation != "" {
		pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = annotation
	}
	if renderedStatus != "" {
		pr.Status.Conditions = []metav1.Condition{
			{Type: porchv1alpha2.ConditionRendered, Status: renderedStatus},
		}
	}
	return pr
}

func TestRenderTrigger(t *testing.T) {
	tests := []struct {
		name       string
		pr         *porchv1alpha2.PackageRevision
		wantReq    string
		wantAnno   bool
		wantSource bool
	}{
		{
			name:     "no triggers",
			pr:       prWithRenderState("", "", "", "", ""),
			wantAnno: false, wantSource: false,
		},
		{
			name: "no trigger — not Draft",
			pr: func() *porchv1alpha2.PackageRevision {
				pr := prWithRenderState("v1", "", "", "init", metav1.ConditionUnknown)
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
				return pr
			}(),
			wantAnno: false, wantSource: false,
		},
		{
			name:     "annotation trigger — new annotation",
			pr:       prWithRenderState("v1", "", "", "", ""),
			wantReq:  "v1", wantAnno: true, wantSource: false,
		},
		{
			name:     "annotation trigger — annotation differs from observed",
			pr:       prWithRenderState("v2", "v1", "", "", metav1.ConditionTrue),
			wantReq:  "v2", wantAnno: true, wantSource: false,
		},
		{
			name:     "no annotation trigger — annotation matches observed",
			pr:       prWithRenderState("v1", "v1", "", "", metav1.ConditionTrue),
			wantReq:  "v1", wantAnno: false, wantSource: false,
		},
		{
			name:     "source trigger — creationSource set, not rendered",
			pr:       prWithRenderState("", "", "", "init", metav1.ConditionUnknown),
			wantAnno: false, wantSource: true,
		},
		{
			name:     "source trigger — creationSource set, no conditions",
			pr:       prWithRenderState("", "", "", "clone", ""),
			wantAnno: false, wantSource: true,
		},
		{
			name:     "no source trigger — already rendered",
			pr:       prWithRenderState("", "", "", "init", metav1.ConditionTrue),
			wantAnno: false, wantSource: false,
		},
		{
			name:     "both triggers",
			pr:       prWithRenderState("v1", "", "", "init", metav1.ConditionUnknown),
			wantReq:  "v1", wantAnno: true, wantSource: true,
		},
		{
			name:     "source trigger on render failure",
			pr:       prWithRenderState("", "", "", "clone", metav1.ConditionFalse),
			wantAnno: false, wantSource: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, anno, source := renderTrigger(tt.pr)
			assert.Equal(t, tt.wantReq, req)
			assert.Equal(t, tt.wantAnno, anno)
			assert.Equal(t, tt.wantSource, source)
		})
	}
}


func TestIsRenderStale(t *testing.T) {
	assert.True(t, isRenderStale("v2", "v1"))
	assert.False(t, isRenderStale("v1", "v1"))
	assert.True(t, isRenderStale("", "v1"))
}

func TestObservedVersionAfterRender(t *testing.T) {
	// Annotation trigger: use requested value.
	assert.Equal(t, "v1", observedVersionAfterRender("v1", nil))

	// Source trigger: use annotation if present.
	annos := map[string]string{porchv1alpha2.AnnotationRenderRequest: "v2"}
	assert.Equal(t, "v2", observedVersionAfterRender("", annos))

	// Source trigger: no annotation.
	assert.Equal(t, "", observedVersionAfterRender("", nil))
}

func TestIsRenderedTrue(t *testing.T) {
	assert.True(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionTrue)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionFalse)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionUnknown)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", "")))
}

func TestReadyCondition(t *testing.T) {
	c := readyCondition(5, metav1.ConditionTrue, porchv1alpha2.ReasonReady, "")
	assert.Equal(t, porchv1alpha2.ConditionReady, c.Type)
	assert.Equal(t, metav1.ConditionTrue, c.Status)
	assert.Equal(t, porchv1alpha2.ReasonReady, c.Reason)
	assert.Equal(t, int64(5), c.ObservedGeneration)
}

func TestRenderedCondition(t *testing.T) {
	c := renderedCondition(3, metav1.ConditionFalse, porchv1alpha2.ReasonRenderFailed, "boom")
	assert.Equal(t, porchv1alpha2.ConditionRendered, c.Type)
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, porchv1alpha2.ReasonRenderFailed, c.Reason)
	assert.Equal(t, "boom", c.Message)
	assert.Equal(t, int64(3), c.ObservedGeneration)
}

func TestRenderRequestChanged(t *testing.T) {
	pred := renderRequestChanged()

	tests := []struct {
		name   string
		oldVal string
		newVal string
		want   bool
	}{
		{"changed", "v1", "v2", true},
		{"unchanged", "v1", "v1", false},
		{"added", "", "v1", true},
		{"removed", "v1", "", true},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			newObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			if tt.oldVal != "" {
				oldObj.Annotations[porchv1alpha2.AnnotationRenderRequest] = tt.oldVal
			}
			if tt.newVal != "" {
				newObj.Annotations[porchv1alpha2.AnnotationRenderRequest] = tt.newVal
			}
			got := pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj})
			assert.Equal(t, tt.want, got)
		})
	}
}


func TestKptfileFromResources(t *testing.T) {
	tests := []struct {
		name      string
		resources map[string]string
		wantName  string
		wantErr   bool
	}{
		{
			name:      "no Kptfile",
			resources: map[string]string{"foo.yaml": "bar"},
			wantName:  "",
		},
		{
			name:      "empty map",
			resources: map[string]string{},
			wantName:  "",
		},
		{
			name:      "empty Kptfile string",
			resources: map[string]string{"Kptfile": ""},
			wantName:  "",
		},
		{
			name: "valid Kptfile",
			resources: map[string]string{
				"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-pkg
info:
  readinessGates:
  - conditionType: Ready
`,
			},
			wantName: "my-pkg",
		},
		{
			name:      "malformed Kptfile",
			resources: map[string]string{"Kptfile": "not: valid: yaml: ["},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kf, err := kptfileFromResources(tt.resources)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, kf.Name)
				assert.Len(t, kf.Info.ReadinessGates, 1)
			}
		})
	}
}


func TestKptRendererSuccess(t *testing.T) {
	r := &kptRenderer{}
	resources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
`,
		"cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
	}

	result, err := r.Render(t.Context(), resources)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.Contains(t, result.resources, "Kptfile")
	assert.Contains(t, result.resources, "cm.yaml")
}

func TestKptRendererNoKptfile(t *testing.T) {
	r := &kptRenderer{}
	resources := map[string]string{"cm.yaml": "apiVersion: v1\nkind: ConfigMap\n"}

	result, err := r.Render(t.Context(), resources)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.Equal(t, resources, result.resources)
	assert.Nil(t, result.results)
}


func TestMockRendererPipelineErr(t *testing.T) {
	m := &mockRenderer{
		resources:   map[string]string{"Kptfile": "rendered"},
		pipelineErr: errors.New("function failed"),
	}
	result, err := m.Render(t.Context(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "rendered", result.resources["Kptfile"])
	assert.EqualError(t, result.err, "function failed")
}

func TestMockRendererInfraErr(t *testing.T) {
	m := &mockRenderer{err: errors.New("cannot write FS")}
	result, err := m.Render(t.Context(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}
