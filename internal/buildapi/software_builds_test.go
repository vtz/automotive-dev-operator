package buildapi

import (
	"testing"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSoftwareBuildToResponse_GitSource(t *testing.T) {
	sb := &automotivev1alpha1.SoftwareBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name: "body-ecu-nucleo",
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/requested-by": "dev@example.com",
			},
		},
		Spec: automotivev1alpha1.SoftwareBuildSpec{
			Runtime: automotivev1alpha1.SoftwareBuildRuntimeSpec{Image: "ghcr.io/zephyrproject-rtos/ci-base:v0.27.4"},
			Source: automotivev1alpha1.SoftwareBuildSourceSpec{
				Type: automotivev1alpha1.SoftwareBuildSourceGit,
				Git: &automotivev1alpha1.SoftwareBuildGitSource{
					URL:      "https://github.com/example/body-ecu",
					Revision: "main",
				},
			},
		},
		Status: automotivev1alpha1.SoftwareBuildStatus{
			Phase:           automotivev1alpha1.SoftwareBuildPhaseSucceeded,
			PipelineRunName: "body-ecu-nucleo-run-abc",
			ArtifactURI:     "pvc://artifacts/zephyr.bin",
			Stages: []automotivev1alpha1.SoftwareBuildStageStatus{
				{Name: "fetch", State: "Succeeded"},
				{Name: "build", State: "Succeeded"},
			},
			Conditions: []metav1.Condition{
				{Type: "Ready", Message: "Build completed successfully"},
			},
		},
	}

	resp := softwareBuildToResponse(sb)

	if resp.Name != "body-ecu-nucleo" {
		t.Errorf("Name = %q, want %q", resp.Name, "body-ecu-nucleo")
	}
	if resp.Phase != "Succeeded" {
		t.Errorf("Phase = %q, want %q", resp.Phase, "Succeeded")
	}
	if resp.RequestedBy != "dev@example.com" {
		t.Errorf("RequestedBy = %q, want %q", resp.RequestedBy, "dev@example.com")
	}
	if resp.PipelineRunName != "body-ecu-nucleo-run-abc" {
		t.Errorf("PipelineRunName = %q, want %q", resp.PipelineRunName, "body-ecu-nucleo-run-abc")
	}
	if resp.ArtifactURI != "pvc://artifacts/zephyr.bin" {
		t.Errorf("ArtifactURI = %q, want %q", resp.ArtifactURI, "pvc://artifacts/zephyr.bin")
	}
	if resp.Source == nil {
		t.Fatal("Source is nil")
	}
	if resp.Source.Type != "git" {
		t.Errorf("Source.Type = %q, want %q", resp.Source.Type, "git")
	}
	if resp.Source.URL != "https://github.com/example/body-ecu" {
		t.Errorf("Source.URL = %q, want %q", resp.Source.URL, "https://github.com/example/body-ecu")
	}
	if resp.Source.Revision != "main" {
		t.Errorf("Source.Revision = %q, want %q", resp.Source.Revision, "main")
	}
	if resp.Runtime == nil || resp.Runtime.Image != "ghcr.io/zephyrproject-rtos/ci-base:v0.27.4" {
		t.Errorf("Runtime.Image = %q, want %q", resp.Runtime.Image, "ghcr.io/zephyrproject-rtos/ci-base:v0.27.4")
	}
	if resp.Message != "Build completed successfully" {
		t.Errorf("Message = %q, want %q", resp.Message, "Build completed successfully")
	}
	if len(resp.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(resp.Stages))
	}
	if resp.Stages[0].Name != "fetch" || resp.Stages[0].State != "Succeeded" {
		t.Errorf("Stage[0] = %+v, want {fetch Succeeded}", resp.Stages[0])
	}
}

func TestSoftwareBuildToResponse_PVCSource(t *testing.T) {
	sb := &automotivev1alpha1.SoftwareBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "local-build"},
		Spec: automotivev1alpha1.SoftwareBuildSpec{
			Runtime: automotivev1alpha1.SoftwareBuildRuntimeSpec{Image: "ubuntu:24.04"},
			Source: automotivev1alpha1.SoftwareBuildSourceSpec{
				Type: automotivev1alpha1.SoftwareBuildSourcePVC,
				PVC: &automotivev1alpha1.SoftwareBuildPVCSource{
					ClaimName: "body-ecu-workspace",
				},
			},
		},
		Status: automotivev1alpha1.SoftwareBuildStatus{
			Phase: automotivev1alpha1.SoftwareBuildPhasePending,
		},
	}

	resp := softwareBuildToResponse(sb)

	if resp.Source == nil {
		t.Fatal("Source is nil")
	}
	if resp.Source.Type != "pvc" {
		t.Errorf("Source.Type = %q, want %q", resp.Source.Type, "pvc")
	}
	if resp.Source.URL != "body-ecu-workspace" {
		t.Errorf("Source.URL = %q, want %q", resp.Source.URL, "body-ecu-workspace")
	}
}

func TestSoftwareBuildToResponse_NoAnnotations(t *testing.T) {
	sb := &automotivev1alpha1.SoftwareBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "bare-build"},
		Spec: automotivev1alpha1.SoftwareBuildSpec{
			Runtime: automotivev1alpha1.SoftwareBuildRuntimeSpec{Image: "alpine:3"},
			Source: automotivev1alpha1.SoftwareBuildSourceSpec{
				Type: automotivev1alpha1.SoftwareBuildSourceGit,
				Git:  &automotivev1alpha1.SoftwareBuildGitSource{URL: "https://example.com/repo"},
			},
		},
	}

	resp := softwareBuildToResponse(sb)

	if resp.RequestedBy != "" {
		t.Errorf("RequestedBy = %q, want empty", resp.RequestedBy)
	}
	if resp.FailureReason != "" {
		t.Errorf("FailureReason = %q, want empty", resp.FailureReason)
	}
	if len(resp.Stages) != 0 {
		t.Errorf("len(Stages) = %d, want 0", len(resp.Stages))
	}
}

func TestSoftwareBuildToResponse_FailedBuild(t *testing.T) {
	sb := &automotivev1alpha1.SoftwareBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "failed-build"},
		Spec: automotivev1alpha1.SoftwareBuildSpec{
			Runtime: automotivev1alpha1.SoftwareBuildRuntimeSpec{Image: "gcc:13"},
			Source: automotivev1alpha1.SoftwareBuildSourceSpec{
				Type: automotivev1alpha1.SoftwareBuildSourceGit,
				Git:  &automotivev1alpha1.SoftwareBuildGitSource{URL: "https://example.com/repo"},
			},
		},
		Status: automotivev1alpha1.SoftwareBuildStatus{
			Phase:           automotivev1alpha1.SoftwareBuildPhaseFailed,
			FailureReason:   "build stage exited with code 2",
			PipelineRunName: "failed-build-run-xyz",
			Stages: []automotivev1alpha1.SoftwareBuildStageStatus{
				{Name: "fetch", State: "Succeeded"},
				{Name: "build", State: "Failed", Message: "exit code 2"},
			},
		},
	}

	resp := softwareBuildToResponse(sb)

	if resp.Phase != "Failed" {
		t.Errorf("Phase = %q, want %q", resp.Phase, "Failed")
	}
	if resp.FailureReason != "build stage exited with code 2" {
		t.Errorf("FailureReason = %q, want expected message", resp.FailureReason)
	}
	if len(resp.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(resp.Stages))
	}
	if resp.Stages[1].State != "Failed" {
		t.Errorf("Stage[1].State = %q, want Failed", resp.Stages[1].State)
	}
}
