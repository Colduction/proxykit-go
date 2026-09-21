package structuralindex_test

import (
	"fmt"
	"testing"

	"github.com/colduction/proxykit-go/internal/structuralindex"
)

func BenchmarkBuild(b *testing.B) {
	const text = "http://customer-alice-cc-us:Zx9kQ2mP7vL4@gate.example.net:7777aa"
	for _, backend := range vectorBackends(b) {
		for _, n := range []int{16, 29, 44, 64} {
			b.Run(fmt.Sprintf("%v/%d", backend, n), func(b *testing.B) {
				structuralindex.SetBackend(backend)
				var ix structuralindex.Index
				b.ReportAllocs()
				for b.Loop() {
					ix.Build(text[:n])
				}
			})
		}
	}
}

func BenchmarkIsHostName(b *testing.B) {
	for _, name := range []string{"a.io", "proxy.example.com", "gate.residential-pool-7.example.net"} {
		b.Run(fmt.Sprint(len(name)), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				structuralindex.IsHostName(name)
			}
		})
	}
}

func BenchmarkIsPrintable(b *testing.B) {
	for _, text := range []string{"alice", "alice:s3cr3t", "customer-alice-cc-us-sessid-8f3a91c2d7:Zx9kQ2mP7vL4"} {
		b.Run(fmt.Sprint(len(text)), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				structuralindex.IsPrintable(text)
			}
		})
	}
}
