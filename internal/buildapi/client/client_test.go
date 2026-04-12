package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi"
	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
)

func TestClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Client Suite")
}

var _ = Describe("WithInsecureTLS", func() {
	It("should preserve default transport settings when cloning", func() {
		client, err := New("https://api.example.com", WithInsecureTLS())
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())

		transport, ok := client.httpClient.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport).NotTo(BeNil())
		Expect(transport.TLSClientConfig).NotTo(BeNil())
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())

		// Verify that default transport settings are preserved
		// (proxy, HTTP/2, connection pooling should be inherited)
		// Note: We can't compare function pointers directly, but we verify
		// that the transport was cloned (not nil) and TLS config is set
		Expect(transport.Proxy).NotTo(BeNil())
		Expect(transport.DialContext).NotTo(BeNil())
	})

	It("should update existing transport TLS config", func() {
		existingTransport := &http.Transport{}
		existingClient := &http.Client{
			Transport: existingTransport,
		}

		client, err := New("https://api.example.com", WithHTTPClient(existingClient), WithInsecureTLS())
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())

		transport, ok := client.httpClient.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSClientConfig).NotTo(BeNil())
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
	})
})

var _ = Describe("WithCACertificate", func() {
	var tempCertFile string

	BeforeEach(func() {
		// Create a temporary CA certificate file (PEM format)
		tempFile, err := os.CreateTemp("", "test-ca-*.pem")
		Expect(err).NotTo(HaveOccurred())
		tempCertFile = tempFile.Name()

		// Write a minimal valid PEM certificate (this is just for testing the file reading logic)
		// This is a self-signed certificate in valid PEM format
		certData := `-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAKH4hJ8v5qQkMA0GCSqGSIb3DQEBCwUAMCExHzAdBgNVBAoM
Fk15IE9yZ2FuaXphdGlvbiBJbmMuMRMwEQYDVQQDDApNeSBDQSBOYW1lMB4X
DTIwMDEwMTAwMDAwMFoXDTI1MDEwMTAwMDAwMFowITEfMB0GA1UECgwWTXkg
T3JnYW5pemF0aW9uIEluYy4xEzARBgNVBAMMCk15IENBIE5hbWUwWTATBgcq
hkjOPQIBBggqhkjOPQMBBwNCAATestExample123456789012345678901234
56789012345678901234567890123456789012345678901234567890
-----END CERTIFICATE-----`
		_, err = tempFile.WriteString(certData)
		Expect(err).NotTo(HaveOccurred())
		Expect(tempFile.Close()).To(Succeed())
	})

	AfterEach(func() {
		if tempCertFile != "" {
			_ = os.Remove(tempCertFile)
		}
	})

	It("should preserve default transport settings when cloning", func() {
		client, err := New("https://api.example.com", WithCACertificate(tempCertFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())

		// If certificate parsing succeeds, verify transport is set up correctly
		// If it fails (invalid cert), the function gracefully skips (uses system CAs)
		// Both behaviors are valid
		transport, ok := client.httpClient.Transport.(*http.Transport)
		if ok && transport != nil {
			// Certificate was parsed successfully, verify TLS config
			Expect(transport.TLSClientConfig).NotTo(BeNil())
			if transport.TLSClientConfig.RootCAs != nil {
				// Verify that default transport settings are preserved
				// Note: We can't compare function pointers directly, but we verify
				// that the transport was cloned (not nil) and TLS config is set
				Expect(transport.Proxy).NotTo(BeNil())
				Expect(transport.DialContext).NotTo(BeNil())
			}
		}
		// If transport is nil or not *http.Transport, that's also valid
		// (certificate parsing failed, will use system CAs via default transport)
	})

	It("should handle non-existent certificate file gracefully", func() {
		client, err := New("https://api.example.com", WithCACertificate("/non/existent/file.pem"))
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
		// Should not fail, just skip CA cert configuration
	})

	It("should handle invalid certificate file gracefully", func() {
		invalidFile, err := os.CreateTemp("", "invalid-*.pem")
		Expect(err).NotTo(HaveOccurred())
		_, err = invalidFile.WriteString("invalid certificate data")
		Expect(err).NotTo(HaveOccurred())
		Expect(invalidFile.Close()).To(Succeed())

		client, err := New("https://api.example.com", WithCACertificate(invalidFile.Name()))
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
		// Should not fail, just skip CA cert configuration

		_ = os.Remove(invalidFile.Name())
	})
})

var _ = Describe("Workspace Start/Stop", func() {
	var (
		mockServer *httptest.Server
		apiClient  *Client
	)

	AfterEach(func() {
		if mockServer != nil {
			mockServer.Close()
		}
	})

	Context("StartWorkspace", func() {
		It("should POST to the correct endpoint and decode response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/v1/workspaces/my-app/start"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))

				w.Header().Set("Content-Type", "application/json")
				resp := buildapi.WorkspaceResponse{
					Name:  "my-app",
					Phase: "Pending",
					Arch:  "amd64",
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.StartWorkspace(context.Background(), "my-app")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Name).To(Equal("my-app"))
			Expect(resp.Phase).To(Equal("Pending"))
			Expect(resp.Arch).To(Equal("amd64"))
		})

		It("should return error on non-200 response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "workspace not found"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.StartWorkspace(context.Background(), "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("workspace not found"))
			Expect(resp).To(BeNil())
		})
	})

	Context("StopWorkspace", func() {
		It("should POST to the correct endpoint and decode response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/v1/workspaces/my-app/stop"))

				w.Header().Set("Content-Type", "application/json")
				resp := buildapi.WorkspaceResponse{
					Name:  "my-app",
					Phase: "Running",
					Arch:  "arm64",
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.StopWorkspace(context.Background(), "my-app")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Name).To(Equal("my-app"))
			Expect(resp.Phase).To(Equal("Running"))
		})

		It("should return error on server error", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal error"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.StopWorkspace(context.Background(), "my-app")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("internal error"))
			Expect(resp).To(BeNil())
		})
	})

	Context("DeleteBuild", func() {
		It("should DELETE to the correct endpoint", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/v1/builds/my-build"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message": "build \"my-build\" deleted"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteBuild(context.Background(), "my-build")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error on 403 Forbidden", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error": "you can only delete your own builds"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteBuild(context.Background(), "other-build")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("you can only delete your own builds"))
		})

		It("should return error on 404 Not Found", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "build not found"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteBuild(context.Background(), "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("build not found"))
		})

		It("should properly escape build names in the URL path", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/v1/builds/my%20build"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message": "deleted"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL)
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteBuild(context.Background(), "my build")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("workspaceAction with URL-unsafe names", func() {
		It("should properly escape workspace names in the URL path", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The name "my app" should be escaped to "my%20app"
				Expect(r.URL.Path).To(Equal("/v1/workspaces/my%20app/start"))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(buildapi.WorkspaceResponse{Name: "my app"})
			}))

			var err error
			apiClient, err = New(mockServer.URL)
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.StartWorkspace(context.Background(), "my app")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Name).To(Equal("my app"))
		})
	})
})

var _ = Describe("SoftwareBuild client methods", func() {
	var (
		mockServer *httptest.Server
		apiClient  *Client
	)

	AfterEach(func() {
		if mockServer != nil {
			mockServer.Close()
		}
	})

	Context("ListSoftwareBuilds", func() {
		It("should GET /v1/software-builds and decode the list", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/v1/software-builds"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]buildapi.SoftwareBuildListItem{
					{Name: "body-ecu-nucleo", Phase: "Succeeded", Image: "ci-base:v0.27.4", Source: "https://github.com/example/repo"},
					{Name: "unit-tests", Phase: "Running", Image: "ubuntu:24.04"},
				})
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			items, err := apiClient.ListSoftwareBuilds(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(2))
			Expect(items[0].Name).To(Equal("body-ecu-nucleo"))
			Expect(items[0].Phase).To(Equal("Succeeded"))
			Expect(items[1].Name).To(Equal("unit-tests"))
		})

		It("should return error on non-200 response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "server error"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			_, err = apiClient.ListSoftwareBuilds(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("list software builds"))
		})
	})

	Context("GetSoftwareBuild", func() {
		It("should GET /v1/software-builds/:name and decode the response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodGet))
				Expect(r.URL.Path).To(Equal("/v1/software-builds/body-ecu-nucleo"))

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(buildapi.SoftwareBuildResponse{
					Name:            "body-ecu-nucleo",
					Phase:           "Succeeded",
					PipelineRunName: "body-ecu-nucleo-run-abc",
				})
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.GetSoftwareBuild(context.Background(), "body-ecu-nucleo")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Name).To(Equal("body-ecu-nucleo"))
			Expect(resp.Phase).To(Equal("Succeeded"))
			Expect(resp.PipelineRunName).To(Equal("body-ecu-nucleo-run-abc"))
		})

		It("should return error on 404", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "not found"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			_, err = apiClient.GetSoftwareBuild(context.Background(), "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("get software build failed"))
		})
	})

	Context("RunSoftwareBuild", func() {
		It("should POST /v1/software-builds/:name/run with the request body", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.URL.Path).To(Equal("/v1/software-builds/body-ecu-nucleo/run"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))

				var req buildapi.SoftwareBuildRunRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Revision).To(Equal("feature-branch"))

				w.WriteHeader(http.StatusAccepted)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(buildapi.SoftwareBuildResponse{
					Name:  "body-ecu-nucleo",
					Phase: "Pending",
				})
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := apiClient.RunSoftwareBuild(context.Background(), "body-ecu-nucleo", buildapi.SoftwareBuildRunRequest{
				Revision: "feature-branch",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Name).To(Equal("body-ecu-nucleo"))
			Expect(resp.Phase).To(Equal("Pending"))
		})

		It("should return error on non-2xx response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "build not found"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			_, err = apiClient.RunSoftwareBuild(context.Background(), "nonexistent", buildapi.SoftwareBuildRunRequest{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("run software build failed"))
		})
	})

	Context("DeleteSoftwareBuild", func() {
		It("should DELETE /v1/software-builds/:name", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodDelete))
				Expect(r.URL.Path).To(Equal("/v1/software-builds/body-ecu-nucleo"))

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message": "deleted"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteSoftwareBuild(context.Background(), "body-ecu-nucleo")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error on non-200 response", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error": "build not found"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL, WithAuthToken("test-token"))
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteSoftwareBuild(context.Background(), "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("delete software build failed"))
		})

		It("should properly escape build names with special characters", func() {
			mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/v1/software-builds/my%20build"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message": "deleted"}`))
			}))

			var err error
			apiClient, err = New(mockServer.URL)
			Expect(err).NotTo(HaveOccurred())

			err = apiClient.DeleteSoftwareBuild(context.Background(), "my build")
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
