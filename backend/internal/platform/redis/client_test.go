package redis

import (
	"strings"
	"testing"
)

func TestOptions_HostPortAndURL(t *testing.T) {
	o, err := options("localhost:6380")
	if err != nil || o.Addr != "localhost:6380" || o.Password != "" || o.TLSConfig != nil {
		t.Fatalf("host:port: %+v %v", o, err)
	}
	o, err = options("redis://default:s3cret@cache.internal:6379/2")
	if err != nil || o.Addr != "cache.internal:6379" || o.Password != "s3cret" || o.Username != "default" || o.DB != 2 || o.TLSConfig != nil {
		t.Fatalf("redis URL: %+v %v", o, err)
	}
	o, err = options("rediss://:pw@managed.example:6380")
	if err != nil || o.TLSConfig == nil || o.Password != "pw" {
		t.Fatalf("rediss URL must use TLS: %+v %v", o, err)
	}
}

func TestOptions_ABadURLNeverEchoesThePassword(t *testing.T) {
	_, err := options("redis://default:s3cret@host:notaport")
	if err == nil {
		t.Fatal("a bad port must fail")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("the error leaks the password: %v", err)
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("redis://default:s3cret@host:6379"); strings.Contains(got, "s3cret") || !strings.Contains(got, "host:6379") {
		t.Fatalf("redacted: %q", got)
	}
	if got := Redact("localhost:6379"); got != "localhost:6379" {
		t.Fatalf("plain address: %q", got)
	}
}
