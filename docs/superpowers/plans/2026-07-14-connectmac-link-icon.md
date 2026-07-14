# ConnectMac Link Icon Implementation Plan

> **For agentic workers:** Implement task-by-task with focused tests and review.

**Goal:** Add one reusable ConnectMac link mark and consistent action icons to Web, packaged Local Agent resources, and documentation.

**Architecture:** Keep the icon as a static SVG asset with no runtime dependency. The Web manager references it directly, the Debian package includes it, the local agent serves it read-only, and the README references the repository asset. Action controls use accessible inline SVG symbols while retaining text labels.

### Task 1: Add shared icon asset and package it

- [x] Add `web/assets/connectmac-mark.svg`.
- [x] Copy the Web asset into the Debian package and serve `/icon.svg` from the local agent.
- [x] Add tests for asset existence and local-agent icon response.

### Task 2: Update Web branding and controls

- [x] Add favicon and header mark.
- [x] Add link/display/transfer icons to action buttons with accessible labels and tooltips.
- [x] Add Web contract tests and verify responsive layout.

### Task 3: Document and verify

- [x] Add README project mark and local-agent resource note.
- [x] Run SVG/XML, JavaScript, Go, vet, and diff checks.
- [x] Commit the completed icon change without changing AWS behavior.
