// Copyright 2022,2024 The kpt Authors
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

package apiserver

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.opentelemetry.io/otel"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/util"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const repositoryValidationEndpoint = "/validate-repository"

var (
	cert        tls.Certificate
	certModTime time.Time
)

var tracer = otel.Tracer("repository-webhook")

// WebhookConfig defines the configuration for the Repository validation webhook
type WebhookConfig struct {
	Port                 int32
	RepositoryPath       string
	RepoServiceName      string
	RepoServiceNamespace string
	RepoHost             string
	CertStorageDir       string
	CertManWebhook       bool
	timeout              int32
}

// newWebhookConfig creates a new WebhookConfig object filled with values read from environment variables
func newWebhookConfig(ctx context.Context) *WebhookConfig {
	var cfg WebhookConfig
	cfg.RepositoryPath = repositoryValidationEndpoint
	cfg.RepoServiceName, cfg.RepoServiceNamespace = webhookServiceName(ctx)
	cfg.RepoHost = fmt.Sprintf("%s.%s.svc", cfg.RepoServiceName, cfg.RepoServiceNamespace)

	cfg.Port = getEnvInt32("WEBHOOK_PORT", 8443)
	cfg.CertStorageDir = getEnv("CERT_STORAGE_DIR", "/tmp/cert")
	cfg.CertManWebhook = getEnvBool("USE_CERT_MAN_FOR_WEBHOOK", false)
	return &cfg
}

// webhookServiceName returns the name and namespace of Kubernetes service belonging to the webhook
func webhookServiceName(ctx context.Context) (serviceName, serviceNamespace string) {
	var apiSvcNs string

	// the webhook service namespace gets it value from the following sources in order of precedence:
	// - WEBHOOK_SERVICE_NAME environment variable
	// - the name of the service referenced in porch's APIService object
	serviceName = os.Getenv("WEBHOOK_SERVICE_NAME")
	if serviceName == "" { // empty value and unset envvar are the same for our purposes
		// if WEBHOOK_SERVICE_NAME is not set, try to use the porch API service name
		apiSvc, err := util.GetPorchApiServiceKey(ctx)
		if err != nil {
			panic(fmt.Sprintf("WEBHOOK_SERVICE_NAME environment variable is not set, and could not find porch's APIservice: %v", err))
		}
		serviceName = apiSvc.Name
		apiSvcNs = apiSvc.Namespace // cache the namespace value to avoid duplicate calls of GetPorchApiServiceKey()
	}

	// the webhook service namespace gets it value from the following sources in order of precedence:
	// - WEBHOOK_SERVICE_NAMESPACE environment variable
	// - CERT_NAMESPACE environment variable
	// - the namespace of the service referenced in porch's APIService object
	// - namespace of the current process (if running in a pod)
	serviceNamespace = os.Getenv("WEBHOOK_SERVICE_NAMESPACE")
	if serviceNamespace == "" {
		serviceNamespace = os.Getenv("CERT_NAMESPACE")
	}
	if serviceNamespace == "" {
		serviceNamespace = apiSvcNs
	}
	if serviceNamespace == "" {
		apiSvc, err := util.GetPorchApiServiceKey(ctx)
		if err == nil {
			serviceNamespace = apiSvc.Namespace
		}
	}
	if serviceNamespace == "" {
		var err error
		serviceNamespace, err = util.GetInClusterNamespace()
		if err != nil {
			// this was our last resort, so panic if failed
			panic(fmt.Sprintf("WEBHOOK_SERVICE_NAMESPACE environment variable is not set, and couldn't deduce its value either: %v", err))
		}
	}
	// theoretically this should never happen, but this is a failsafe
	if serviceName == "" || serviceNamespace == "" {
		panic("Couldn't automatically determine a valid value for WEBHOOK_SERVICE_NAME and WEBHOOK_SERVICE_NAMESPACE environment variables. Please set them explicitly!")
	}
	return
}

func setupWebhooks(ctx context.Context, clientReader client.Reader) error {
	cfg := newWebhookConfig(ctx)
	// TODO: Refactor webhook setup to support optional webhooks and better separation of concerns.
	// Currently webhooks are always enabled and required for Repository validation.
	// Consider: 1) Making webhooks optional via explicit flag, 2) Separating cert management from webhook lifecycle,
	// 3) Supporting webhook-less mode for development/testing.
	if !cfg.CertManWebhook {
		caBytes, err := createCerts(cfg)
		if err != nil {
			return err
		}
		if err := createValidatingWebhook(ctx, cfg, caBytes); err != nil {
			return err
		}
	}

	if err := runWebhookServer(ctx, cfg, clientReader); err != nil {
		return err
	}
	return nil
}

func createCerts(cfg *WebhookConfig) ([]byte, error) {
	klog.Infof("creating self-signing TLS cert and key for %q in directory %s", cfg.RepoHost, cfg.CertStorageDir)
	commonName := cfg.RepoHost
	dnsNames := []string{commonName}
	dnsNames = append(dnsNames, cfg.RepoServiceName)
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s", cfg.RepoServiceName, cfg.RepoServiceNamespace))
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc", cfg.RepoServiceName, cfg.RepoServiceNamespace))
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc.cluster.local", cfg.RepoServiceName, cfg.RepoServiceNamespace))

	var caPEM, serverCertPEM, serverPrivateKeyPEM *bytes.Buffer
	// CA config
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2020),
		Subject: pkix.Name{
			Organization: []string{"google.com"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		return nil, err
	}
	caBytes, err := x509.CreateCertificate(cryptorand.Reader, ca, ca, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}
	caPEM = new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	// server cert config
	cert := &x509.Certificate{
		DNSNames:     dnsNames,
		SerialNumber: big.NewInt(1658),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"google.com"},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	serverPrivateKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		return nil, err
	}
	serverCertBytes, err := x509.CreateCertificate(cryptorand.Reader, cert, ca, &serverPrivateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}
	serverCertPEM = new(bytes.Buffer)
	_ = pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})
	serverPrivateKeyPEM = new(bytes.Buffer)
	_ = pem.Encode(serverPrivateKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivateKey),
	})

	err = os.MkdirAll(cfg.CertStorageDir, 0750)
	if err != nil {
		return nil, err
	}
	err = WriteFile(filepath.Join(cfg.CertStorageDir, "tls.crt"), serverCertPEM.Bytes())
	if err != nil {
		return nil, err
	}
	err = WriteFile(filepath.Join(cfg.CertStorageDir, "tls.key"), serverPrivateKeyPEM.Bytes())
	if err != nil {
		return nil, err
	}

	return caPEM.Bytes(), nil
}

// WriteFile writes data in the file at the given path
func WriteFile(filepath string, c []byte) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(c)
	if err != nil {
		return err
	}
	return nil
}

func createValidatingWebhook(ctx context.Context, cfg *WebhookConfig, caCert []byte) error {

	klog.Infof("Creating validating webhook for %s:%d", cfg.RepoHost, cfg.Port)

	kubeConfig := ctrl.GetConfigOrDie()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to setup kubeClient: %v", err)
	}
	// Set max timeout value for ValidatingWebhooks
	cfg.timeout = 30
	var (
		repositoryCfgName = "repository-validating-webhook"
		fail              = admissionregistrationv1.Fail
		none              = admissionregistrationv1.SideEffectClassNone
	)

	// Webhook for Repository validation
	repositoryWebhook := admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: repositoryCfgName,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name: "porchrepositorywebhook.kpt.dev",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				CABundle: caCert,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{
					admissionregistrationv1.Create,
					admissionregistrationv1.Update,
				},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{configapi.GroupVersion.Group},
					APIVersions: []string{configapi.GroupVersion.Version},
					Resources:   []string{"repositories"},
				},
			}},
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &none,
			FailurePolicy:           &fail,
			TimeoutSeconds:          &cfg.timeout,
		}},
	}

	// Set service for repository webhook
	repositoryWebhook.Webhooks[0].ClientConfig.Service = &admissionregistrationv1.ServiceReference{
		Name:      cfg.RepoServiceName,
		Namespace: cfg.RepoServiceNamespace,
		Path:      &cfg.RepositoryPath,
		Port:      &cfg.Port,
	}

	// Delete and recreate repository webhook to allow updates in webhook configurations
	_ = kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(ctx, repositoryCfgName, metav1.DeleteOptions{})

	if _, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(ctx, &repositoryWebhook, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create repository validation webhook: %w", err)
	}

	return nil
}

// load the certificate & keep note of time loaded for reload on new cert details
func loadCertificate(certPath, keyPath string) (tls.Certificate, error) {
	certInfo, err := os.Stat(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	if certInfo.ModTime().After(certModTime) {
		newCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, err
		}
		cert = newCert
		certModTime = certInfo.ModTime()
	}
	return cert, nil
}

// watch for changes on the mount path of the secret as volume
func watchCertificates(ctx context.Context, directory, certFile, keyFile string) {
	// Set up a watcher for the certificate directory
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		klog.Errorf("failed to start certificate watcher: %v", err)
		return
	}
	defer watcher.Close()
	// Start watching the directory
	err = watcher.Add(directory)
	if err != nil {
		klog.Errorf("invalid certificate watcher directory : %v", err)
		return
	}
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return // Exit if the watcher.Events channel was closed
				}
				if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
					_, err := loadCertificate(certFile, keyFile)
					if err != nil {
						klog.Errorf("Failed to load updated certificate: %v", err)
					} else {
						klog.Info("Certificate reloaded successfully")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return // Exit if the watcher.Errors channel was closed
				}
				klog.Errorf("Error watching certificates: %v", err)
			case <-ctx.Done():
				return
			}
		}
	}()
	// Wait for the context to be canceled before returning and cleaning up
	<-ctx.Done()
	klog.Info("Shutting down certificate watcher")
}

func getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return &cert, nil
}

func runWebhookServer(ctx context.Context, cfg *WebhookConfig, clientReader client.Reader) error {
	certFile := filepath.Join(cfg.CertStorageDir, "tls.crt")
	keyFile := filepath.Join(cfg.CertStorageDir, "tls.key")
	// load the cert for the first time

	_, err := loadCertificate(certFile, keyFile)
	if err != nil {
		klog.Errorf("failed to load certificate: %v", err)
		return err
	}
	if cfg.CertManWebhook {
		go watchCertificates(ctx, cfg.CertStorageDir, certFile, keyFile)
	}
	klog.Infoln("Starting webhook server")
	http.HandleFunc(cfg.RepositoryPath, func(w http.ResponseWriter, r *http.Request) {
		validateRepository(w, r, clientReader)
	})
	server := http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port),
		TLSConfig: &tls.Config{
			GetCertificate: getCertificate,
			MinVersion:     tls.VersionTLS12,
		},
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		err = server.ListenAndServeTLS("", "")
		if err != nil {
			klog.Errorf("could not start server: %v", err)
		}
	}()
	return err

}

func decodeAdmissionReview(r *http.Request) (*admissionv1.AdmissionReview, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("expected Content-Type 'application/json'")
	}
	var requestData []byte
	if r.Body != nil {
		var err error
		requestData, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
	}
	admissionReviewRequest := &admissionv1.AdmissionReview{}
	deserializer := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
	if _, _, err := deserializer.Decode(requestData, nil, admissionReviewRequest); err != nil {
		return nil, err
	}
	if admissionReviewRequest.Request == nil {
		return nil, fmt.Errorf("admission review request is empty")
	}
	return admissionReviewRequest, nil
}

func constructResponse(response *admissionv1.AdmissionResponse,
	request *admissionv1.AdmissionReview) ([]byte, error) {
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = response
	admissionReviewResponse.SetGroupVersionKind(request.GroupVersionKind())
	admissionReviewResponse.Response.UID = request.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		return nil, fmt.Errorf("error marshalling response json: %v", err)
	}
	return resp, nil
}

func writeErr(errMsg string, w *http.ResponseWriter) {
	klog.Errorf("%s", errMsg)
	(*w).WriteHeader(500)
	if _, err := (*w).Write([]byte(errMsg)); err != nil { // #nosec G705
		klog.Errorf("could not write error message: %v", err)
	}
}

func getEnv(key string, defaultValue string) string {
	value, found := os.LookupEnv(key)
	if !found {
		return defaultValue
	}
	return value
}

func getEnvBool(key string, defaultValue bool) bool {
	value, found := os.LookupEnv(key)
	if !found {
		return defaultValue
	}
	boolean, err := strconv.ParseBool(value)
	if err != nil {
		panic("could not parse boolean from environment variable: " + key)
	}
	return boolean
}

func getEnvInt32(key string, defaultValue int32) int32 {
	value, found := os.LookupEnv(key)
	if !found {
		return defaultValue
	}
	i64, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		panic("could not parse int32 from environment variable: " + key)
	}
	return int32(i64) // this is safe because of the size parameter of the ParseInt call
}

func validateRepository(w http.ResponseWriter, r *http.Request, clientReader client.Reader) {
	ctx, span := tracer.Start(r.Context(), "validateRepository")
	defer span.End()

	admissionReviewRequest, err := decodeAdmissionReview(r)
	if err != nil {
		writeErr(fmt.Sprintf("error decoding admission review: %v", err), &w)
		return
	}

	operation := strings.ToLower(string(admissionReviewRequest.Request.Operation))
	repoName := fmt.Sprintf("%s/%s", admissionReviewRequest.Request.Namespace, admissionReviewRequest.Request.Name)
	klog.Infof("received request to validate repository %s for %s", repoName, operation)

	if admissionReviewRequest.Request.Resource.Resource != "repositories" {
		writeErr(fmt.Sprintf("unexpected resource: %s", admissionReviewRequest.Request.Resource.Resource), &w)
		return
	}

	var attempted configapi.Repository
	if err := json.Unmarshal(admissionReviewRequest.Request.Object.Raw, &attempted); err != nil {
		klog.Errorf("failed to unmarshal repository object: %v", err)
		writeErr(fmt.Sprintf("could not unmarshal repository: %v", err), &w)
		return
	}

	// NOTE: Immutability checks (URL, branch, directory) are handled by CEL validation in the CRD.
	// This webhook only performs complex cross-resource conflict detection that CEL cannot do.

	// Check for conflicts with existing repositories
	var repoList configapi.RepositoryList
	if err := clientReader.List(ctx, &repoList); err != nil {
		klog.Errorf("failed to list repositories: %v", err)
		writeErr(fmt.Sprintf("could not list repositories: %v", err), &w)
		return
	}

	for _, existing := range repoList.Items {
		if existing.Name == attempted.Name && existing.Namespace == attempted.Namespace {
			continue
		}
		if isConflict(&existing, &attempted) {
			klog.Errorf("repository validation failed: conflict detected between attempted %s/%s and existing %s/%s", attempted.Namespace, attempted.Name, existing.Namespace, existing.Name)
			writeConflictResponse(fmt.Sprintf("Repository conflict with existing repository: %s/%s", existing.Namespace, existing.Name), "RepositoryConflict", admissionReviewRequest, &w)
			return
		}
	}

	klog.V(1).Infof("repository validation passed for %s", repoName)
	resp := &admissionv1.AdmissionResponse{
		Allowed: true,
		Result: &metav1.Status{
			Status:  "Success",
			Message: "Repository validated successfully",
		},
	}
	responseBytes, err := constructResponse(resp, admissionReviewRequest)
	if err != nil {
		errMsg := fmt.Sprintf("error constructing response: %v", err)
		writeErr(errMsg, &w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(responseBytes) // #nosec G705
	if err != nil {
		klog.Errorf("error writing response: %v", err)
		return
	}
}

func normalizeURL(url string) string {
	// Convert URL to cache-safe format
	// Example: http://172.18.255.200:3000/porch/myrepo.git → http---172.18.255.200-3000-porch-myrepo.git
	replace := strings.NewReplacer("://", "---", ":", "-", "/", "-")
	return replace.Replace(url)
}

func isConflict(existing, attempted *configapi.Repository) bool {
	existingURL := normalizeURL(existing.Spec.Git.Repo)
	attemptedURL := normalizeURL(attempted.Spec.Git.Repo)

	existingDir := strings.Trim(existing.Spec.Git.Directory, "/")
	attemptedDir := strings.Trim(attempted.Spec.Git.Directory, "/")

	// Branch defaults to "main" via CRD default, so no need to handle empty values
	existingBranch := existing.Spec.Git.Branch
	attemptedBranch := attempted.Spec.Git.Branch

	// Rule 1: Same URL, branch and directory in same namespace → conflict
	// (Kubernetes only prevents duplicate names, not duplicate Git locations)
	if existingURL == attemptedURL && existingBranch == attemptedBranch && existingDir == attemptedDir &&
		existing.Namespace == attempted.Namespace {
		return true
	}

	// Rule 2: Root directory conflicts with any other directory under same URL and branch
	if existingURL == attemptedURL && existingBranch == attemptedBranch {
		if (existingDir == "" && attemptedDir != "") || (existingDir != "" && attemptedDir == "") {
			return true
		}
	}

	// Rule 3: Nested directory conflicts with its base directory under same URL and branch
	if existingURL == attemptedURL && existingBranch == attemptedBranch {
		if isNestedConflict(existingDir, attemptedDir) {
			return true
		}
	}

	return false
}

func isNestedConflict(a, b string) bool {
	// Check if one path is nested within the other using filepath.Rel
	relAtoB, err1 := filepath.Rel(a, b)
	relBtoA, err2 := filepath.Rel(b, a)

	// If either relative path doesn't start with "../", it means one is nested in the other
	if err1 == nil && !strings.HasPrefix(relAtoB, "../") && relAtoB != "." {
		return true // b is nested within a
	}
	if err2 == nil && !strings.HasPrefix(relBtoA, "../") && relBtoA != "." {
		return true // a is nested within b
	}

	return false
}

func writeConflictResponse(message, reason string, admissionReviewRequest *admissionv1.AdmissionReview, w *http.ResponseWriter) {
	resp := &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Status:  "Failure",
			Message: message,
			Reason:  metav1.StatusReason(reason),
		},
	}
	responseBytes, err := constructResponse(resp, admissionReviewRequest)
	if err != nil {
		klog.Errorf("failed to construct conflict response: %v", err)
		writeErr(fmt.Sprintf("error constructing response: %v", err), w)
		return
	}
	(*w).Header().Set("Content-Type", "application/json")
	_, err = (*w).Write(responseBytes) // #nosec G705
	if err != nil {
		klog.Errorf("error writing response: %v", err)
	}
}
