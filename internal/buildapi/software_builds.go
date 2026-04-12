package buildapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (a *APIServer) handleListSoftwareBuilds(c *gin.Context) {
	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create client: %v", err)})
		return
	}

	namespace := resolveNamespace()
	var sbList automotivev1alpha1.SoftwareBuildList
	if err := k8sClient.List(c.Request.Context(), &sbList, client.InNamespace(namespace)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list software builds: %v", err)})
		return
	}

	sort.Slice(sbList.Items, func(i, j int) bool {
		return sbList.Items[j].CreationTimestamp.Before(&sbList.Items[i].CreationTimestamp)
	})

	items := make([]SoftwareBuildListItem, 0, len(sbList.Items))
	for _, sb := range sbList.Items {
		source := ""
		if sb.Spec.Source.Type == automotivev1alpha1.SoftwareBuildSourceGit && sb.Spec.Source.Git != nil {
			source = sb.Spec.Source.Git.URL
		} else if sb.Spec.Source.Type == automotivev1alpha1.SoftwareBuildSourcePVC && sb.Spec.Source.PVC != nil {
			source = "pvc:" + sb.Spec.Source.PVC.ClaimName
		}
		items = append(items, SoftwareBuildListItem{
			Name:      sb.Name,
			Phase:     string(sb.Status.Phase),
			Image:     sb.Spec.Runtime.Image,
			Source:    source,
			CreatedAt: sb.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, items)
}

func (a *APIServer) handleGetSoftwareBuild(c *gin.Context) {
	name := c.Param("name")

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create client: %v", err)})
		return
	}

	namespace := resolveNamespace()
	var sb automotivev1alpha1.SoftwareBuild
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := k8sClient.Get(c.Request.Context(), key, &sb); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("software build %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get software build: %v", err)})
		return
	}

	c.JSON(http.StatusOK, softwareBuildToResponse(&sb))
}

func (a *APIServer) handleRunSoftwareBuild(c *gin.Context) {
	name := c.Param("name")

	var req SoftwareBuildRunRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}
	}

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create client: %v", err)})
		return
	}

	namespace := resolveNamespace()
	var sb automotivev1alpha1.SoftwareBuild
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := k8sClient.Get(c.Request.Context(), key, &sb); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("software build %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get software build: %v", err)})
		return
	}

	if sb.Annotations == nil {
		sb.Annotations = make(map[string]string)
	}
	sb.Annotations["automotive.sdv.cloud.redhat.com/trigger-time"] = time.Now().UTC().Format(time.RFC3339)

	if req.Revision != "" && sb.Spec.Source.Git != nil {
		sb.Annotations["automotive.sdv.cloud.redhat.com/revision-override"] = req.Revision
	}

	requestedBy := a.resolveRequester(c)
	sb.Annotations["automotive.sdv.cloud.redhat.com/requested-by"] = requestedBy

	sb.Status.PipelineRunName = ""
	sb.Status.Phase = ""
	sb.Status.FailureReason = ""
	sb.Status.Stages = nil

	if err := k8sClient.Update(c.Request.Context(), &sb); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to trigger build: %v", err)})
		return
	}

	if err := k8sClient.Status().Update(c.Request.Context(), &sb); err != nil {
		a.log.Error(err, "failed to reset status after trigger", "name", name)
	}

	c.JSON(http.StatusAccepted, softwareBuildToResponse(&sb))
}

func (a *APIServer) handleStreamSoftwareBuildLogs(c *gin.Context) {
	name := c.Param("name")

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create client: %v", err)})
		return
	}

	namespace := resolveNamespace()
	var sb automotivev1alpha1.SoftwareBuild
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := k8sClient.Get(c.Request.Context(), key, &sb); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("software build %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get software build: %v", err)})
		return
	}

	if sb.Status.PipelineRunName == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no pipeline run found for this build"})
		return
	}

	a.streamSoftwareBuildPipelineLogs(c, sb.Status.PipelineRunName, namespace, name)
}

func (a *APIServer) handleDeleteSoftwareBuild(c *gin.Context) {
	name := c.Param("name")

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create client: %v", err)})
		return
	}

	namespace := resolveNamespace()
	sb := &automotivev1alpha1.SoftwareBuild{}
	sb.Name = name
	sb.Namespace = namespace

	if err := k8sClient.Delete(c.Request.Context(), sb); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("software build %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete software build: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("software build %q deleted", name)})
}

// streamSoftwareBuildPipelineLogs streams Tekton pod logs for a SoftwareBuild PipelineRun.
// Unlike streamLogs (ImageBuild), the PipelineRun name is already known from SoftwareBuild status.
func (a *APIServer) streamSoftwareBuildPipelineLogs(c *gin.Context, pipelineRunName, namespace, softwareBuildName string) {
	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	tr := strings.TrimSpace(pipelineRunName)
	if tr == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "logs not available yet"})
		return
	}

	sinceTime := parseSinceTime(c.Query("since"))
	streamDuration := time.Duration(a.limits.MaxLogStreamDurationMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(c.Request.Context(), streamDuration)
	defer cancel()

	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	setupLogStreamHeaders(c)

	pipelineRunSelector := "tekton.dev/pipelineRun=" + tr + ",tekton.dev/memberOf=tasks"
	var hadStream bool
	var lastKeepalive time.Time
	streamedContainers := make(map[string]map[string]bool)
	completedPods := make(map[string]bool)
	sb := &automotivev1alpha1.SoftwareBuild{}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: pipelineRunSelector})
		if err != nil {
			if _, writeErr := fmt.Fprintf(c.Writer, "\n[Error listing pods: %v]\n", err); writeErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to write error message: %v\n", writeErr)
			}
			c.Writer.Flush()
			time.Sleep(2 * time.Second)
			continue
		}

		if len(pods.Items) == 0 {
			if !hadStream {
				_, _ = c.Writer.Write([]byte("."))
				c.Writer.Flush()
			}
			time.Sleep(2 * time.Second)
			continue
		}

		sort.Slice(pods.Items, func(i, j int) bool {
			if pods.Items[i].Status.StartTime == nil {
				return false
			}
			if pods.Items[j].Status.StartTime == nil {
				return true
			}
			return pods.Items[i].Status.StartTime.Before(pods.Items[j].Status.StartTime)
		})

		allPodsComplete := true
		for _, pod := range pods.Items {
			if completedPods[pod.Name] {
				continue
			}

			if streamedContainers[pod.Name] == nil {
				streamedContainers[pod.Name] = make(map[string]bool)
			}

			processPodLogs(ctx, c, cs, pod, namespace, sinceTime, streamedContainers[pod.Name], &hadStream)

			stepNames := getStepContainerNames(pod)
			if len(streamedContainers[pod.Name]) == len(stepNames) &&
				(pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed) {
				completedPods[pod.Name] = true
			} else {
				allPodsComplete = false
			}
		}

		if shouldExitSoftwareBuildLogStream(ctx, k8sClient, softwareBuildName, namespace, sb, allPodsComplete) {
			break
		}

		time.Sleep(2 * time.Second)

		if !hadStream {
			_, _ = c.Writer.Write([]byte("."))
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
		} else if allPodsComplete {
			now := time.Now()
			if now.Sub(lastKeepalive) >= 30*time.Second {
				_, _ = c.Writer.Write([]byte("[Waiting for remaining pipeline tasks...]\n"))
				if f, ok := c.Writer.(http.Flusher); ok {
					f.Flush()
				}
				lastKeepalive = now
			}
		}
	}

	writeLogStreamFooter(c, hadStream)
}

func shouldExitSoftwareBuildLogStream(
	ctx context.Context,
	k8sClient client.Client,
	name, namespace string,
	sb *automotivev1alpha1.SoftwareBuild,
	allPodsComplete bool,
) bool {
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sb); err == nil {
		ph := sb.Status.Phase
		if (ph == automotivev1alpha1.SoftwareBuildPhaseSucceeded || ph == automotivev1alpha1.SoftwareBuildPhaseFailed) && allPodsComplete {
			return true
		}
	}
	return false
}

func softwareBuildToResponse(sb *automotivev1alpha1.SoftwareBuild) SoftwareBuildResponse {
	resp := SoftwareBuildResponse{
		Name:            sb.Name,
		Phase:           string(sb.Status.Phase),
		PipelineRunName: sb.Status.PipelineRunName,
		ArtifactURI:     sb.Status.ArtifactURI,
		FailureReason:   sb.Status.FailureReason,
		Runtime:         &SoftwareBuildRuntimeSummary{Image: sb.Spec.Runtime.Image},
	}

	if sb.Annotations != nil {
		resp.RequestedBy = sb.Annotations["automotive.sdv.cloud.redhat.com/requested-by"]
	}

	if sb.Spec.Source.Type == automotivev1alpha1.SoftwareBuildSourceGit && sb.Spec.Source.Git != nil {
		resp.Source = &SoftwareBuildSourceSummary{
			Type:     string(sb.Spec.Source.Type),
			URL:      sb.Spec.Source.Git.URL,
			Revision: sb.Spec.Source.Git.Revision,
		}
	} else if sb.Spec.Source.Type == automotivev1alpha1.SoftwareBuildSourcePVC && sb.Spec.Source.PVC != nil {
		resp.Source = &SoftwareBuildSourceSummary{
			Type: string(sb.Spec.Source.Type),
			URL:  sb.Spec.Source.PVC.ClaimName,
		}
	}

	for _, cond := range sb.Status.Conditions {
		if cond.Type == "Ready" {
			resp.Message = cond.Message
			break
		}
	}

	stages := make([]SoftwareBuildStageStatusAPI, 0, len(sb.Status.Stages))
	for _, s := range sb.Status.Stages {
		stages = append(stages, SoftwareBuildStageStatusAPI{
			Name:    s.Name,
			State:   s.State,
			Message: s.Message,
		})
	}
	resp.Stages = stages

	return resp
}
