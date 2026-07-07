// Package infra groups the technology adapters implementing the domain
// ports, each in its own subpackage (added in phase 2):
//
//   - sqlite: repositories over modernc.org/sqlite (pure Go, CGO-free)
//   - docker: ContainerRuntime over the official Docker SDK
//   - restic: SnapshotEngine driving Restic through ephemeral workers
//   - crypto: Argon2id password hashing and AES-256-GCM secret cipher
package infra
