package proxykit_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/colduction/proxykit-go"
	"github.com/colduction/proxykit-go/proxyparser"
	"github.com/colduction/proxykit-go/proxypool"
)

func TestPoolParseHTTPProxy(t *testing.T) {
	const (
		username = "user@name"
		password = "p:ss/?#%"
		target   = "http://target.invalid:8080/resource?q=1"
	)
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.RequestURI != target || r.URL.String() != target || r.Host != "target.invalid:8080" {
			http.Error(w, "wrong proxy target", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Proxy-Authorization") != wantAuth {
			http.Error(w, "wrong proxy credentials", http.StatusProxyAuthRequired)
			return
		}
		if r.TLS == nil {
			_, _ = io.WriteString(w, "http proxy")
		} else {
			_, _ = io.WriteString(w, "https proxy")
		}
	})
	plain := httptest.NewServer(handler)
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(handler)
	t.Cleanup(secure.Close)
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())
	var lines []string
	for _, endpoint := range []string{plain.URL, secure.URL} {
		scheme, host, ok := strings.Cut(endpoint, "://")
		if !ok {
			t.Fatalf("unexpected test proxy URL %q", endpoint)
		}
		lines = append(lines, scheme+"|"+host+"|"+username+"|"+password)
	}
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\r\n")), 0600); err != nil {
		t.Fatal(err)
	}
	pool, err := proxypool.Open(path, proxypool.Options{SequentialBufferBytes: 32, MaxLineBytes: 256})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	parser, err := proxyparser.New("%t|%h:%d|%u|%p", true)
	if err != nil {
		t.Fatal(err)
	}
	var batch proxypool.Batch
	var count int
	for {
		if err := pool.NextBatch(&batch); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		for line := range batch.Lines() {
			var proxy proxykit.Proxy
			if err := parser.ParseBytes(line, &proxy); err != nil {
				t.Fatal(err)
			}
			exported := proxy.ExportURL().String()
			if !strings.Contains(exported, "user%40name:p%3Ass%2F%3F%23%25@") {
				t.Fatalf("credentials not percent-escaped: %q", exported)
			}
			proxyURL, err := url.Parse(exported)
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{
				Proxy:                 http.ProxyURL(proxyURL),
				TLSClientConfig:       &tls.Config{RootCAs: roots},
				TLSHandshakeTimeout:   2 * time.Second,
				ResponseHeaderTimeout: 2 * time.Second,
			}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("response read: %v; close: %v", readErr, closeErr)
			}
			if response.StatusCode != http.StatusOK || string(body) != string(proxy.Scheme)+" proxy" {
				t.Fatalf("proxy response = %d %q", response.StatusCode, body)
			}
			count++
		}
	}
	if count != len(lines) {
		t.Fatalf("sent %d proxy requests, want %d", count, len(lines))
	}
}

func BenchmarkPoolParse(b *testing.B) {
	const lineCount = 4096
	var source strings.Builder
	want := make(map[string]proxykit.Proxy, lineCount)
	var wantHostBytes int
	for i := range lineCount {
		number := strconv.Itoa(i)
		var host string
		switch i % 4 {
		case 0:
			host = "proxy-" + number + ".zone" + strconv.Itoa(i%64) + ":8080"
		case 1:
			host = "192.0." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256) + ":8080"
		case 2:
			host = "proxy-" + number + ".example.com:8080"
		default:
			host = "[2001:db8::" + strconv.FormatInt(int64(i), 16) + "]:8080"
		}
		line := "http://user:pass@" + host
		source.WriteString(line)
		source.WriteByte('\n')
		want[line] = proxykit.Proxy{Scheme: proxykit.HTTP, Host: host, Username: "user", Password: "pass"}
		wantHostBytes += len(host)
	}
	path := filepath.Join(b.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
		b.Fatal(err)
	}
	parser, err := proxyparser.New("%t://%u:%p@%h:%d", true)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []struct {
		name  string
		value proxypool.Mode
	}{
		{name: "Sequential", value: proxypool.ModeSequential},
		{name: "Shuffled", value: proxypool.ModeShuffled},
	} {
		for _, method := range []string{"NextBytes", "Batch"} {
			b.Run(mode.name+"/"+method, func(b *testing.B) {
				pool, err := proxypool.Open(path, proxypool.Options{
					Mode:                  mode.value,
					Reuse:                 true,
					SequentialBufferBytes: 16 << 10,
					BlockBytes:            16 << 10,
					RegionBytes:           64 << 10,
					MaxLineBytes:          256,
					Seed:                  123,
				})
				if err != nil {
					b.Fatal(err)
				}
				defer pool.Close()
				var buffer []byte
				var batch proxypool.Batch
				var proxy proxykit.Proxy
				verify := true
				scan := func() {
					var count, hostBytes int
					if method == "NextBytes" {
						for count < lineCount {
							buffer, err = pool.NextBytes(buffer[:0])
							if err != nil {
								b.Fatal(err)
							}
							if err := parser.ParseBytes(buffer, &proxy); err != nil {
								b.Fatal(err)
							}
							if verify && proxy != want[string(buffer)] {
								b.Fatalf("parsed proxy differs for %q", buffer)
							}
							hostBytes += len(proxy.Host)
							count++
						}
					} else {
						for count < lineCount {
							if err := pool.NextBatch(&batch); err != nil {
								b.Fatal(err)
							}
							for line := range batch.Lines() {
								if err := parser.ParseBytes(line, &proxy); err != nil {
									b.Fatal(err)
								}
								if verify && proxy != want[string(line)] {
									b.Fatalf("parsed proxy differs for %q", line)
								}
								hostBytes += len(proxy.Host)
								count++
							}
						}
					}
					if count != lineCount || hostBytes != wantHostBytes {
						b.Fatalf("parsed %d lines / %d host bytes, want %d / %d", count, hostBytes, lineCount, wantHostBytes)
					}
				}
				scan()
				verify = false
				b.SetBytes(int64(source.Len()))
				b.ReportAllocs()
				for b.Loop() {
					scan()
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*lineCount), "ns/line")
			})
		}
	}
}
