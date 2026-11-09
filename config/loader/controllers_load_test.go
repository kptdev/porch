package loader

import (
	"testing"
	"time"

	"github.com/kptdev/porch/config/types/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/config/types"
)

func TestControllersBackwardCompatibleDefaults(t *testing.T) {
	var cfg types.Controllers
	require.NoError(t, Load(&cfg, nil, nil))
	assert.Equal(t, "/etc/webhook/certs", cfg.CertDir)
	assert.Equal(t, 5*time.Minute, cfg.Repository.HealthCheckFrequency.Duration)
	assert.Equal(t, 512*datasize.KiB, cfg.Repository.GoGitCacheMaxFileSize)
	assert.Equal(t, "pgx", cfg.Repository.DB.Driver)

	t.Setenv("DB_HOST", "db.example")
	t.Setenv("ENABLE_PACKAGEVARIANTS", "true")
	var overridden types.Controllers
	require.NoError(t, Load(&overridden, nil, []string{"--repositories.cache-type=CR"}))
	assert.Equal(t, "db.example", overridden.Repository.Database.Host)
	assert.True(t, overridden.EnablePackageVariants)
	assert.Equal(t, "CR", overridden.Repository.CacheType)
}
