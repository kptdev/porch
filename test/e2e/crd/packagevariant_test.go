// Copyright 2026 The Nephio Authors
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

package crd

// PackageVariant and PackageVariantSet e2e tests.
//
// These controllers currently operate on v1alpha1 PackageRevision types
// and have not been ported to v1alpha2 CRDs yet. Once ported, this file
// should cover:
//
//   - PV creating a downstream package from an upstream blueprint
//   - PV updating downstream when upstream changes
//   - PV deletion cleaning up downstream packages
//   - PV with injection mutators
//   - PVS fan-out across multiple repositories
//   - PVS with object/repository selectors
//   - PVS adoption of existing packages
//
// The PV/PVS controllers will need to:
//   1. Watch v1alpha2 PackageRevision CRDs instead of v1alpha1
//   2. Create v1alpha2 PackageRevisions (with spec.source) instead of v1alpha1 (with spec.tasks)
//   3. Use spec.lifecycle patches instead of the approval subresource
//
// Tracking: nephio-project/porch GitHub issues
