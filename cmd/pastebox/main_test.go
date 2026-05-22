package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAdminCreateCommandEmitsBootstrapEnvWithoutPassword(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"admin", "create", "--email", "Admin@Example.COM", "--password", "password123"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "created bootstrap admin admin@example.com") {
		t.Fatalf("expected normalized admin email in output, got %q", output)
	}
	if !strings.Contains(output, "PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com") {
		t.Fatalf("expected bootstrap email env, got %q", output)
	}
	if strings.Contains(output, "password123") {
		t.Fatalf("bootstrap output must not echo the password: %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestAdminCreateCommandRequiresEmailAndPassword(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"admin", "create", "--email", "admin@example.com"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected usage exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--email and --password are required") {
		t.Fatalf("expected validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}
