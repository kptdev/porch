package loader

import (
	"testing"
	"time"

	"github.com/kptdev/porch/config/types"
)

func TestControllersBackwardCompatibleDefaults(t *testing.T) {
	var cfg types.Controllers
	if err := Load(&cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.CertDir != "/etc/webhook/certs" {
		t.Fatalf("cert dir %q", cfg.CertDir)
	}
	if cfg.Repository.HealthCheckFrequency.Duration != 5*time.Minute {
		t.Fatalf("health %s", cfg.Repository.HealthCheckFrequency.Duration)
	}
	if cfg.Repository.GoGitCacheMaxFileSize != 512*types.KiB {
		t.Fatalf("file size %d", cfg.Repository.GoGitCacheMaxFileSize)
	}
	if cfg.Repository.DB.Driver != "pgx" {
		t.Fatalf("driver %q", cfg.Repository.DB.Driver)
	}
	if cfg.PackageRevision.MaxGRPCMessageSize != 6*types.MiB {
		t.Fatalf("grpc %d", cfg.PackageRevision.MaxGRPCMessageSize)
	}
	if cfg.FunctionRuntime.DefaultImagePrefix != "ghcr.io/kptdev/krm-functions-catalog" {
		t.Fatalf("prefix %q", cfg.FunctionRuntime.DefaultImagePrefix)
	}
	if cfg.PackageRevision.DefaultImagePrefix != cfg.FunctionRuntime.DefaultImagePrefix {
		t.Fatalf("package revision prefix %q", cfg.PackageRevision.DefaultImagePrefix)
	}

	t.Setenv("DEFAULT_IMAGE_PREFIX", "example.io")
	t.Setenv("DB_HOST", "db.example")
	t.Setenv("ENABLE_PACKAGEVARIANTS", "true")
	var overridden types.Controllers
	if err := Load(&overridden, nil, []string{"--repositories.cache-type=CR"}); err != nil {
		t.Fatal(err)
	}
	if overridden.FunctionRuntime.DefaultImagePrefix != "example.io" || overridden.PackageRevision.DefaultImagePrefix != "example.io" {
		t.Fatalf("shared prefix = %q / %q", overridden.FunctionRuntime.DefaultImagePrefix, overridden.PackageRevision.DefaultImagePrefix)
	}
	if overridden.Repository.Database.Host != "db.example" {
		t.Fatalf("db host %q", overridden.Repository.Database.Host)
	}
	if !overridden.EnablePackageVariants {
		t.Fatal("package variants were not enabled")
	}
	if overridden.Repository.CacheType != "CR" {
		t.Fatalf("cache type %q", overridden.Repository.CacheType)
	}
}
