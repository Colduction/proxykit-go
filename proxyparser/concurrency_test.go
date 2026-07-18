package proxyparser_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/proxyparser"
)

func TestProxyParserConcurrentUse(t *testing.T) {
	parser, err := proxyparser.New("%t://%u:%p@%h:%d", true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const input = "socks5://user:pass@proxy.example.com:1080"
	inputBytes := []byte(input)
	want := proxykit.Proxy{
		Scheme:   proxykit.SOCKS5,
		Host:     "proxy.example.com:1080",
		Username: "user",
		Password: "pass",
	}
	const (
		workers    = 16
		iterations = 250
	)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Go(func() {
			var into, fromBytes proxykit.Proxy
			for iteration := range iterations {
				parsed, parseErr := parser.Parse(input)
				if parseErr != nil || parsed == nil || *parsed != want {
					failures <- fmt.Errorf("worker %d Parse iteration %d: got (%+v, %v)", worker, iteration, parsed, parseErr)
					return
				}
				value, valueErr := parser.ParseString(input)
				if valueErr != nil || value != want {
					failures <- fmt.Errorf("worker %d ParseString iteration %d: got (%+v, %v)", worker, iteration, value, valueErr)
					return
				}
				if intoErr := parser.ParseInto(input, &into); intoErr != nil || into != want {
					failures <- fmt.Errorf("worker %d ParseInto iteration %d: got (%+v, %v)", worker, iteration, into, intoErr)
					return
				}
				if bytesErr := parser.ParseBytes(inputBytes, &fromBytes); bytesErr != nil || fromBytes != want {
					failures <- fmt.Errorf("worker %d ParseBytes iteration %d: got (%+v, %v)", worker, iteration, fromBytes, bytesErr)
					return
				}
			}
		})
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
