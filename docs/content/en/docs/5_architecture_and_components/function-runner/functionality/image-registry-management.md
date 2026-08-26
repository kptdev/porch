---
title: "Image and Registry Management"
type: docs
weight: 3
description: |
  Detailed architecture of image metadata caching, registry authentication, and secret management.
---

## Overview

The Function Runner manages container image metadata and registry authentication to optimize pod creation and support private registries. The system caches image metadata (digest and entrypoint) to avoid repeated registry calls, handles authentication for private registries using Docker config format, and supports TLS configuration for secure registry connections.

### High-Level Architecture

```
┌──────────────────────────────────────────────────────────┐
│          Image and Registry Management                   │
│                                                          │
│  ┌──────────────────┐      ┌──────────────────┐          │
│  │     Image        │      │    Registry      │          │
│  │    Metadata      │ ───> │ Authentication   │          │
│  │    Caching       │      │                  │          │
│  │                  │      │  • Default       │          │
│  │  • Digest        │      │  • Custom Auth   │          │
│  │  • Entrypoint    │      │  • TLS Config    │          │
│  │  • In-Memory     │      │                  │          │
│  └──────────────────┘      └──────────────────┘          │
│           │                         │                    │
│           └────────┬────────────────┘                    │
│                    ↓                                     │
│          ┌───────────────────┐                           │
│          │      Secret       │                           │
│          │   Management      │                           │
│          │                   │                           │
│          │  • Creation       │                           │
│          │  • Synchronization│                           │
│          │  • Pod Injection  │                           │
│          └───────────────────┘                           │
└──────────────────────────────────────────────────────────┘
```

## Image Metadata Caching

The pod manager caches image metadata to avoid repeated registry API calls during pod creation.

### Cache Structure

```
Pod Manager
    ↓
imageMetadataCache (sync.Map)
    ↓
image → digestAndEntrypoint
    ↓
  • digest: SHA256 hex string
  • entrypoint: Command array
```

**Cache characteristics:**
- **In-memory storage**: sync.Map for thread-safe concurrent access
- **Key**: Function image name (full image reference)
- **Value**: Digest and entrypoint struct
- **Lifetime**: Persists for Function Runner process lifetime
- **No expiration**: Entries never evicted (immutable image metadata)

**Cached metadata:**
- **Digest**: SHA256 hash of image manifest (immutable identifier)
- **Entrypoint**: Command and arguments from image config

### Cache Population

```
Pod Creation Request
        ↓
  Check imageMetadataCache
        ↓
  Found? ──Yes──> Use Cached Metadata
        │
        No
        ↓
  Fetch from Registry
        ↓
  Parse → Authenticate → Fetch Manifest/Config
        ↓
  Extract Digest + Entrypoint
        ↓
  Store in Cache
        ↓
  Return Metadata
```

**Population process:**
- Fast O(1) lookup by image name
- Cache miss triggers registry fetch
- Metadata extracted from manifest and config
- Subsequent requests served from cache

**Metadata extraction:**
- Digest calculated from image manifest SHA256
- Entrypoint extracted from image config (Config.Entrypoint or Config.Cmd fallback)
- Both values immutable for given image

## Registry Authentication

The Function Runner supports both default and custom registry authentication.

### Authentication Strategy

```
Image Reference
        ↓
  Default Registry? ──Yes──> Default Keychain
        │
        No
        ↓
  Private Registries Enabled? ──No──> Default Keychain
        │
       Yes
        ↓
  Custom Authentication
```

**Authentication decision:**
- **Default registry** (ghcr.io/kptdev/krm-functions-catalog/): Uses default keychain
- **Custom registry**: Requires private registry support enabled
- **Disabled private registries**: Falls back to default keychain

### Default Keychain

**Keychain characteristics:**
- Uses system-level credentials
- Supports multiple credential sources:
  - Docker config.json
  - Cloud provider credentials
  - Service account tokens
- Automatic credential resolution
- No explicit configuration needed

### Custom Authentication

**Authentication mechanism:**
- Docker config JSON format mounted as secret
- Credentials extracted for specific registry
- Authenticator created from config
- Used for all registry API calls

**Docker config format:**
- Standard Docker config.json structure
- Contains auths map with registry credentials
- Supports multiple registries
- Mounted as secret into Function Runner pod

## Secret Management

The pod manager creates and manages Kubernetes secrets for private registry authentication.

### Secret Lifecycle

```
Ensure Auth Secret
        ↓
  Secret Exists? ──Yes──> Inspect and Update if Changed
        │
        No
        ↓
  Read Docker Config
        ↓
  Create Secret
        ↓
  • Type: kubernetes.io/dockerconfigjson
  • Data: .dockerconfigjson
```

**Lifecycle management:**
- Secret created on first use
- Inspected and updated if Docker config changes
- Synchronized with mounted configuration
- Persists across Function Runner restarts

### Image Pull Secret Injection

```
Pod Creation
        ↓
  Custom Registry? ──No──> No Secret Needed
        │
       Yes
        ↓
  Secret Already Attached? ──Yes──> Skip
        │
        No
        ↓
  Append ImagePullSecret
```

**Injection pattern:**
- Each function pod gets image pull secret reference
- Secret attached during pod creation
- Kubelet uses secret for image pull authentication
- Automatic authentication without pod-level configuration

## TLS Configuration

The Function Runner supports custom TLS certificates for secure registry connections.

### TLS Certificate Management

**Certificate loading:**
- Reads from mounted secret path
- Supports common certificate filenames
- Parses PEM-encoded certificates
- Creates certificate pool with custom CA

**TLS configuration:**
- Custom HTTP transport with TLS config
- Minimum TLS version 1.2
- Validates server certificates against CA
- Used for all registry API calls

### TLS Fallback Strategy

```
Image Pull with TLS
        ↓
  Attempt with Custom Transport
        ↓
  Success? ──Yes──> Return Image
        │
        No
        ↓
  Manifest Unknown? ──Yes──> Return Error
        │
        No (TLS Error)
        ↓
  Retry with Default Transport
```

**Fallback behavior:**
- TLS errors trigger fallback to default transport
- Manifest errors returned immediately (no fallback)
- Logs warning about TLS failure
- Enables graceful degradation

## Performance Optimization

The image management system optimizes performance through caching and efficient operations.

### Metadata Caching Benefits

**Performance improvements:**
- **Eliminates registry calls**: Cached metadata served instantly
- **Reduces network latency**: No round-trip to registry
- **Lowers registry load**: Fewer API requests
- **Faster pod creation**: Metadata available immediately

**Scalability benefits:**
- Supports high pod creation rate
- Handles concurrent requests efficiently
- No registry rate limiting issues
- Consistent performance under load

### Registry Call Reduction

```
Without Cache:          With Cache:

Pod 1 → Registry        Pod 1 → Registry
Pod 2 → Registry        Pod 2 → Cache
Pod 3 → Registry        Pod 3 → Cache
Pod 4 → Registry        Pod 4 → Cache

4 registry calls        1 registry call
```

**Call reduction pattern:**
- First request fetches from registry
- Subsequent requests served from cache
- Dramatic reduction in registry API calls
- Proportional to pod reuse frequency

### Digest-Based Identification

**Digest benefits:**
- **Immutable reference**: Digest never changes for same content
- **Content verification**: Ensures image integrity
- **Cache key**: Stable identifier for caching
- **Pod naming**: Consistent pod names for same image

**Digest usage:**
- Pod ID generation (image name + digest prefix)
- Cache key for metadata
- Image pull verification
- Audit trail for deployed images

### Concurrent Operations

**Concurrency characteristics:**
- Multiple images pulled concurrently
- Each image has independent metadata fetch
- sync.Map provides thread-safe cache access
- No artificial concurrency limits

**Concurrency benefits:**
- Parallel pod creation for different functions
- Faster cache warming at startup
- Efficient resource utilization
- Scales with available network bandwidth

## Cache Consistency

The cache maintains consistency through immutable metadata and thread-safe operations.

### Immutability Guarantees

**Metadata immutability:**
- Image digest is immutable identifier
- Entrypoint fixed for given image
- Cache entries never updated (only added)
- No expiration or eviction needed

**Consistency mechanism:**
- Digest ensures same content always referenced
- Tag mutation prevented by digest-based caching
- No stale data issues (metadata never changes)

### Concurrency Control

**Thread safety:**
- sync.Map provides lock-free concurrent reads
- Concurrent writes handled safely
- No mutex contention
- Scales with concurrent pod creation

**Access patterns:**
- Read-heavy workload (cache hits)
- Writes only on cache miss
- No coordination needed between readers
- Efficient under high concurrency

## Error Handling

The image management system handles errors at multiple levels.

### Registry Errors

**Error types:**
- **Parse errors**: Invalid image reference
- **Authentication errors**: Invalid credentials
- **Network errors**: Registry unreachable
- **Manifest errors**: Image not found
- **TLS errors**: Certificate validation failure

**Error handling strategy:**
- Parse errors returned immediately
- Authentication errors logged and returned
- Network errors may trigger retry (handled by go-containerregistry)
- Manifest errors returned without retry
- TLS errors trigger fallback to default transport

### Secret Management Errors

**Error scenarios:**
- Secret creation failures
- Docker config parse errors
- Secret update conflicts

**Error handling:**
- Errors logged with context
- Pod creation fails if secret required but unavailable
- Retry on transient Kubernetes API errors
- Clear error messages for debugging

## Cache Lifecycle

The image metadata cache follows the Function Runner lifecycle.

### Initialization

**Startup:**
- Cache created at Function Runner startup
- Empty initially (lazy population)
- No pre-warming or pre-loading
- Memory allocated on demand

### Runtime

**Operation:**
- Cache populated on first use per image
- Entries persist for process lifetime
- No maintenance or cleanup needed
- Memory grows with unique image count

### Shutdown

**Cleanup:**
- Cache discarded on Function Runner shutdown
- No persistent storage
- No cleanup operations needed
- Rebuilt from scratch on restart
