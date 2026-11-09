// Package forkdbmock contains a mock of forkdb.Store for tests.
package forkdbmock

//go:generate go tool mockgen -destination ./forkdbmock.go -package forkdbmock github.com/coder/coder/v2/coderd/database/forkdb Store
