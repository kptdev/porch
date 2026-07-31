---
title: "Functionality"
type: docs
weight: 3
description: |
  Detailed operational behavior of the PackageRevision Controller.
---

This section covers the runtime behaviour of the PR Controller: how it creates packages, renders content, manages lifecycle transitions, and handles edge cases.

Key aspects covered:

- How source execution creates initial package content (init, clone, copy, upgrade)
- How rendering is triggered, bounded, and protected against staleness
- How lifecycle transitions and deletion gating work
- How status conditions and labels are managed
