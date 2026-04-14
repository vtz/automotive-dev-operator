// Package softwarecmd provides handlers for software build CLI commands.
package softwarecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	common "github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/common"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/config"
	buildapi "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi"
	buildapiclient "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// HandlerOptions wires software handlers to caller-owned state and callbacks.
type HandlerOptions struct {
	ServerURL       *string
	AuthToken       *string
	InsecureSkipTLS *bool

	HandleError func(error)
}

// Handler implements software subcommand run functions.
type Handler struct {
	opts HandlerOptions
}

// NewHandler creates a software workflow handler.
func NewHandler(opts HandlerOptions) *Handler {
	return &Handler{opts: opts}
}

func (h *Handler) handleError(err error) {
	if h.opts.HandleError != nil {
		h.opts.HandleError(err)
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func (h *Handler) mergeTokenFromCmd(cmd *cobra.Command) {
	if h.opts.AuthToken == nil {
		return
	}
	t, _ := cmd.Flags().GetString("token")
	if strings.TrimSpace(t) != "" {
		*h.opts.AuthToken = t
	}
}

func (h *Handler) resolveServerURL(cmd *cobra.Command) string {
	s, _ := cmd.Flags().GetString("server")
	s = strings.TrimSpace(s)
	if s != "" {
		return s
	}
	if h.opts.ServerURL != nil {
		s = strings.TrimSpace(*h.opts.ServerURL)
	}
	if s != "" {
		return s
	}
	if env := strings.TrimSpace(os.Getenv("CAIB_SERVER")); env != "" {
		return env
	}
	return strings.TrimSpace(config.DefaultServerWithDerive())
}

func (h *Handler) insecureTLS() bool {
	if h.opts.InsecureSkipTLS == nil {
		return false
	}
	return *h.opts.InsecureSkipTLS
}

// RunList handles `caib software list`.
func (h *Handler) RunList(cmd *cobra.Command, _ []string) {
	h.mergeTokenFromCmd(cmd)
	serverURL := h.resolveServerURL(cmd)
	if serverURL == "" {
		h.handleError(fmt.Errorf("server URL required: use --server, set CAIB_SERVER, or run 'caib login <server-url>'"))
		return
	}
	if h.opts.AuthToken == nil {
		h.handleError(fmt.Errorf("internal error: auth token option is not configured"))
		return
	}

	ctx := context.Background()
	var items []buildapi.SoftwareBuildListItem
	err := common.ExecuteWithReauth(serverURL, h.opts.AuthToken, h.insecureTLS(), func(api *buildapiclient.Client) error {
		var listErr error
		items, listErr = api.ListSoftwareBuilds(ctx)
		return listErr
	})
	if err != nil {
		h.handleError(err)
		return
	}

	if len(items) == 0 {
		fmt.Println("No software builds found.")
		return
	}

	fmt.Printf("%-30s %-12s %-40s %s\n", "NAME", "PHASE", "IMAGE", "SOURCE")
	for _, item := range items {
		fmt.Printf("%-30s %-12s %-40s %s\n", item.Name, item.Phase, item.Image, item.Source)
	}
}

// RunShow handles `caib software show`.
func (h *Handler) RunShow(cmd *cobra.Command, args []string) {
	h.mergeTokenFromCmd(cmd)
	serverURL := h.resolveServerURL(cmd)
	if serverURL == "" {
		h.handleError(fmt.Errorf("server URL required: use --server, set CAIB_SERVER, or run 'caib login <server-url>'"))
		return
	}
	if h.opts.AuthToken == nil {
		h.handleError(fmt.Errorf("internal error: auth token option is not configured"))
		return
	}

	ctx := context.Background()
	var resp *buildapi.SoftwareBuildResponse
	err := common.ExecuteWithReauth(serverURL, h.opts.AuthToken, h.insecureTLS(), func(api *buildapiclient.Client) error {
		var getErr error
		resp, getErr = api.GetSoftwareBuild(ctx, args[0])
		return getErr
	})
	if err != nil {
		h.handleError(err)
		return
	}

	outputFmt, _ := cmd.Flags().GetString("output")
	switch strings.ToLower(outputFmt) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(resp); encErr != nil {
			h.handleError(encErr)
		}
	case "yaml", "yml":
		enc := yaml.NewEncoder(os.Stdout)
		defer enc.Close()
		if encErr := enc.Encode(resp); encErr != nil {
			h.handleError(encErr)
		}
	case "table":
		fmt.Printf("Name:         %s\n", resp.Name)
		fmt.Printf("Phase:        %s\n", resp.Phase)
		if resp.Message != "" {
			fmt.Printf("Message:      %s\n", resp.Message)
		}
		if resp.PipelineRunName != "" {
			fmt.Printf("PipelineRun:  %s\n", resp.PipelineRunName)
		}
		if resp.Source != nil {
			fmt.Printf("Source:       %s %s@%s\n", resp.Source.Type, resp.Source.URL, resp.Source.Revision)
		}
		if resp.Runtime != nil {
			fmt.Printf("Image:        %s\n", resp.Runtime.Image)
		}
		if resp.ArtifactURI != "" {
			fmt.Printf("Artifact:     %s\n", resp.ArtifactURI)
		}
		if resp.FailureReason != "" {
			fmt.Printf("Failure:      %s\n", resp.FailureReason)
		}
		if len(resp.Stages) > 0 {
			fmt.Println("\nStages:")
			for _, s := range resp.Stages {
				fmt.Printf("  %-12s %s  %s\n", s.Name, s.State, s.Message)
			}
		}
	default:
		h.handleError(fmt.Errorf("invalid output format %q (supported: table, json, yaml)", outputFmt))
	}
}

// RunBuild handles `caib software run`.
func (h *Handler) RunBuild(cmd *cobra.Command, args []string) {
	h.mergeTokenFromCmd(cmd)
	serverURL := h.resolveServerURL(cmd)
	if serverURL == "" {
		h.handleError(fmt.Errorf("server URL required: use --server, set CAIB_SERVER, or run 'caib login <server-url>'"))
		return
	}
	if h.opts.AuthToken == nil {
		h.handleError(fmt.Errorf("internal error: auth token option is not configured"))
		return
	}

	revision, _ := cmd.Flags().GetString("revision")
	image, _ := cmd.Flags().GetString("image")
	noCompliance, _ := cmd.Flags().GetBool("no-compliance")
	local, _ := cmd.Flags().GetBool("local")
	workspace, _ := cmd.Flags().GetString("workspace")

	req := buildapi.SoftwareBuildRunRequest{
		Revision:       revision,
		Image:          image,
		SkipCompliance: noCompliance,
	}
	if local {
		req.LocalWorkspace = workspace
		if !cmd.Flags().Changed("no-compliance") {
			req.SkipCompliance = true
		}
	}

	ctx := context.Background()
	var resp *buildapi.SoftwareBuildResponse
	err := common.ExecuteWithReauth(serverURL, h.opts.AuthToken, h.insecureTLS(), func(api *buildapiclient.Client) error {
		var runErr error
		resp, runErr = api.RunSoftwareBuild(ctx, args[0], req)
		return runErr
	})
	if err != nil {
		h.handleError(err)
		return
	}

	fmt.Printf("Build triggered: %s (phase: %s)\n", resp.Name, resp.Phase)
}

// RunLogs handles `caib software logs`.
func (h *Handler) RunLogs(cmd *cobra.Command, args []string) {
	h.mergeTokenFromCmd(cmd)
	serverURL := h.resolveServerURL(cmd)
	if serverURL == "" {
		h.handleError(fmt.Errorf("server URL required: use --server, set CAIB_SERVER, or run 'caib login <server-url>'"))
		return
	}
	if h.opts.AuthToken == nil {
		h.handleError(fmt.Errorf("internal error: auth token option is not configured"))
		return
	}

	ctx := context.Background()
	var stream io.ReadCloser
	err := common.ExecuteWithReauth(serverURL, h.opts.AuthToken, h.insecureTLS(), func(api *buildapiclient.Client) error {
		var logErr error
		stream, logErr = api.StreamSoftwareBuildLogs(ctx, args[0])
		return logErr
	})
	if err != nil {
		h.handleError(err)
		return
	}
	defer stream.Close()

	if _, copyErr := io.Copy(os.Stdout, stream); copyErr != nil {
		if !strings.Contains(copyErr.Error(), "closed") {
			fmt.Fprintf(os.Stderr, "\nLog stream ended: %v\n", copyErr)
		}
	}
}

// RunDelete handles `caib software delete`.
func (h *Handler) RunDelete(cmd *cobra.Command, args []string) {
	h.mergeTokenFromCmd(cmd)
	serverURL := h.resolveServerURL(cmd)
	if serverURL == "" {
		h.handleError(fmt.Errorf("server URL required: use --server, set CAIB_SERVER, or run 'caib login <server-url>'"))
		return
	}
	if h.opts.AuthToken == nil {
		h.handleError(fmt.Errorf("internal error: auth token option is not configured"))
		return
	}

	ctx := context.Background()
	err := common.ExecuteWithReauth(serverURL, h.opts.AuthToken, h.insecureTLS(), func(api *buildapiclient.Client) error {
		return api.DeleteSoftwareBuild(ctx, args[0])
	})
	if err != nil {
		h.handleError(err)
		return
	}

	fmt.Printf("Software build %q deleted.\n", args[0])
}

// RunCreate handles `caib software create`.
func (h *Handler) RunCreate(cmd *cobra.Command, _ []string) {
	name, _ := cmd.Flags().GetString("name")
	source, _ := cmd.Flags().GetString("source")
	revision, _ := cmd.Flags().GetString("source-revision")
	image, _ := cmd.Flags().GetString("runtime-image")
	fetch, _ := cmd.Flags().GetString("fetch")
	prebuild, _ := cmd.Flags().GetString("prebuild")
	buildCmd, _ := cmd.Flags().GetString("build")
	postbuild, _ := cmd.Flags().GetString("postbuild")
	deploy, _ := cmd.Flags().GetString("deploy")
	outFmt, _ := cmd.Flags().GetString("output")

	if fetch == "" {
		fetch = "echo 'Source cloned by pipeline'"
	}
	if prebuild == "" {
		prebuild = "echo 'No prebuild step'"
	}
	if postbuild == "" {
		postbuild = "echo 'Build complete'"
	}
	if deploy == "" {
		deploy = "echo 'No deploy step'"
	}

	cr := map[string]interface{}{
		"apiVersion": "automotive.sdv.cloud.redhat.com/v1alpha1",
		"kind":       "SoftwareBuild",
		"metadata": map[string]interface{}{
			"name": name,
		},
		"spec": map[string]interface{}{
			"runtime": map[string]interface{}{
				"image": image,
			},
			"source": map[string]interface{}{
				"type": "git",
				"git": map[string]interface{}{
					"url":      source,
					"revision": revision,
				},
			},
			"stages": map[string]interface{}{
				"fetch":     map[string]interface{}{"command": fetch},
				"prebuild":  map[string]interface{}{"command": prebuild},
				"build":     map[string]interface{}{"command": buildCmd},
				"postbuild": map[string]interface{}{"command": postbuild},
				"deploy":    map[string]interface{}{"command": deploy},
			},
			"destination": map[string]interface{}{
				"type": "sharedFolder",
				"path": "/workspace/artifacts",
			},
		},
	}

	switch strings.ToLower(outFmt) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cr); err != nil {
			h.handleError(err)
		}
	default:
		enc := yaml.NewEncoder(os.Stdout)
		defer enc.Close()
		if err := enc.Encode(cr); err != nil {
			h.handleError(err)
		}
	}
}
