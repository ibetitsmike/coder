package sandbox_test

import (
	"bytes"
	"testing"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

func BenchmarkParseSpec(b *testing.B) {
	valid := []byte(`version: 1
container:
  image: docker.io/library/alpine:3.20
  entrypoint: ["/bin/sh", "-c", "sleep infinity"]
agent:
  name: main
lifecycle:
  stop_after_idle_minutes: 30
`)

	b.Run("Valid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := sandbox.ParseSpec(valid)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	invalid := append([]byte("version: [1,\n"), bytes.Repeat([]byte("x\n"), 2048)...)
	b.Run("Invalid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = sandbox.ParseSpec(invalid)
		}
	})
}
