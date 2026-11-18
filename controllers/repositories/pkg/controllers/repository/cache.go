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

package repository

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	configtypes "github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/pkg/cache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/registry/porch"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *RepositoryReconciler) createCacheFromEnv(ctx context.Context, mgr ctrl.Manager) error {
	log := ctrl.Log.WithName(r.Name())
	if err := r.validateCacheType(); err != nil {
		return err
	}

	coreClient, err := client.NewWithWatch(mgr.GetConfig(), client.Options{
		Scheme: mgr.GetScheme(),
	})
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Read DB configuration from environment variables (only for DB cache)
	var dbOptions configtypes.DBCacheOptions
	if strings.ToUpper(r.Cfg.CacheType) == string(cachetypes.DBCacheType) {
		dbOptions, err = r.setupDBCacheOptionsFromEnv()
		if err != nil {
			return fmt.Errorf("failed to setup DB cache options: %w", err)
		}
	}

	// Setup cache directory for git repositories
	cacheDir := r.determineCacheDirectory()
	log.Info("Using git cache directory", "cacheDir", cacheDir)

	// Create credential resolver for git authentication
	credentialResolver, caBundleResolver := r.createCredentialResolvers(coreClient)

	// Simple user info provider for standalone controller
	userInfoProvider := &simpleUserInfoProvider{}

	options := r.buildCacheOptions(coreClient, dbOptions, cacheDir, credentialResolver, caBundleResolver, userInfoProvider)

	err = retry.OnError(
		wait.Backoff{Duration: time.Second, Factor: 1.5, Steps: 20, Cap: 30 * time.Second},
		func(err error) bool {
			klog.Warningf("failed to create repository cache: %v; wait a sec...", err)
			return true
		},
		func() error {
			var err error
			r.Cache, err = cache.GetCacheImpl(ctx, options)
			return err
		},
	)

	return err
}

// validateCacheType checks if the cache type is valid for standalone controller
func (r *RepositoryReconciler) validateCacheType() error {
	cacheTypeUpper := strings.ToUpper(r.Cfg.CacheType)
	if cacheTypeUpper != string(cachetypes.DBCacheType) && cacheTypeUpper != string(cachetypes.CRCacheType) {
		return fmt.Errorf("invalid cache type: %s (must be DB or CR)", r.Cfg.CacheType)
	}
	return nil
}

// determineCacheDirectory resolves the cache directory.
// Priority: cache directory, git cache directory, user cache dir, temp dir.
func (r *RepositoryReconciler) determineCacheDirectory() string {
	log := ctrl.Log.WithName(r.Name())

	// Priority 1: Use flag value or env var if set
	if r.Cfg.CacheDirectory != "" {
		return r.Cfg.CacheDirectory
	}

	// Priority 2: User cache directory (fallback)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// Priority 3: Temp directory as last resort
		cacheDir = os.TempDir()
		log.V(0).Info("Cannot find user cache directory, using temporary directory", "cacheDir", cacheDir)
	}
	cacheDir = cacheDir + "/porch-repo-controller"
	return cacheDir
}

// createCredentialResolvers creates the credential and CA bundle resolvers
func (r *RepositoryReconciler) createCredentialResolvers(coreClient client.Client) (repository.CredentialResolver, repository.CredentialResolver) {
	resolverChain := []porch.Resolver{
		porch.NewBasicAuthResolver(),
		porch.NewBearerTokenAuthResolver(),
	}
	credentialResolver := porch.NewCredentialResolver(coreClient, resolverChain)
	caBundleResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{porch.NewCaBundleResolver()})
	return credentialResolver, caBundleResolver
}

// buildCacheOptions constructs the cache options from all components
func (r *RepositoryReconciler) buildCacheOptions(
	coreClient client.WithWatch,
	dbOptions configtypes.DBCacheOptions,
	cacheDir string,
	credentialResolver repository.CredentialResolver,
	caBundleResolver repository.CredentialResolver,
	userInfoProvider repository.UserInfoProvider,
) cachetypes.CacheOptions {
	return cachetypes.CacheOptions{
		CoreClient:     coreClient,
		CacheType:      cachetypes.CacheType(strings.ToUpper(r.Cfg.CacheType)),
		DBCacheOptions: dbOptions,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			LocalDirectory:             cacheDir,
			UseUserDefinedCaBundle:     r.Cfg.UseUserDefinedCaBundle,
			CredentialResolver:         credentialResolver,
			CaBundleResolver:           caBundleResolver,
			UserInfoProvider:           userInfoProvider,
			RepoOperationRetryAttempts: r.Cfg.RepoOperationRetryAttempts,
			GitCacheOptions:            r.Cfg.GitCacheOptions,
		},
		RepoPRChangeNotifier: cachetypes.NewNoOpRepoPRChangeNotifier(),
		DbPushDraftsToGit:    r.Cfg.PushDraftsToGit,
	}
}

func (r *RepositoryReconciler) setupDBCacheOptionsFromEnv() (configtypes.DBCacheOptions, error) {
	log := ctrl.Log.WithName(r.Name())
	if r.Cfg.DB.DataSource != "" {
		driver := r.Cfg.DB.Driver
		if driver == "" {
			driver = cachetypes.DefaultDBCacheDriver
		}
		return configtypes.DBCacheOptions{Driver: driver, DataSource: r.Cfg.DB.DataSource}, nil
	}
	dbDriver := r.Cfg.DB.Driver
	dbHost := r.Cfg.Database.Host
	dbPort := r.Cfg.Database.Port
	dbName := r.Cfg.Database.Name
	dbUser := r.Cfg.Database.User
	dbUserPass := r.Cfg.Database.Password
	dbSSLMode := strings.ToLower(r.Cfg.Database.SSLMode)

	if dbDriver == "" {
		dbDriver = cachetypes.DefaultDBCacheDriver
		log.Info("DB_DRIVER not provided, using default", "dbDriver", dbDriver)
	}

	missingVars := []string{}
	if dbHost == "" {
		missingVars = append(missingVars, "DB_HOST")
	}
	if dbPort == "" {
		missingVars = append(missingVars, "DB_PORT")
	}
	if dbName == "" {
		missingVars = append(missingVars, "DB_NAME")
	}
	if dbUser == "" {
		missingVars = append(missingVars, "DB_USER")
	}
	if dbSSLMode == "" || dbSSLMode == "disable" {
		if dbUserPass == "" {
			missingVars = append(missingVars, "DB_PASSWORD")
		}
	}

	if len(missingVars) > 0 {
		return configtypes.DBCacheOptions{}, fmt.Errorf("missing required environment variables: %v", missingVars)
	}

	// Build connection string
	var connStr string
	switch dbDriver {
	case "pgx":
		if dbSSLMode == "" || dbSSLMode == "disable" {
			connStr = fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", dbUser, dbUserPass, net.JoinHostPort(dbHost, dbPort), dbName)
		} else {
			connStr = fmt.Sprintf("postgres://%s@%s/%s?sslmode=%s", dbUser, net.JoinHostPort(dbHost, dbPort), dbName, dbSSLMode)
		}
	case "mysql":
		connStr = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", dbUser, dbUserPass, dbHost, dbPort, dbName)
	default:
		return configtypes.DBCacheOptions{}, fmt.Errorf("unsupported DB driver: %s", dbDriver)
	}

	return configtypes.DBCacheOptions{
		Driver:     dbDriver,
		DataSource: connStr,
	}, nil
}

// simpleUserInfoProvider provides default user info for git commits
type simpleUserInfoProvider struct{}

func (p *simpleUserInfoProvider) GetUserInfo(ctx context.Context) *repository.UserInfo {
	return &repository.UserInfo{
		Name:  "porch-controller",
		Email: "porch-controller@kpt.dev",
	}
}
