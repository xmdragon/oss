package minioadm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScopedPolicyName(t *testing.T) {
	if got := ScopedPolicyName("products", PermWrite); got != "scoped-w-products" {
		t.Errorf("write: got %q", got)
	}
	if got := ScopedPolicyName("test-bucket", PermReadWrite); got != "scoped-rw-test-bucket" {
		t.Errorf("rw: got %q", got)
	}
}

func TestBuildScopedPolicy_Write(t *testing.T) {
	doc := buildScopedPolicy("products", PermWrite)
	var parsed struct {
		Statement []struct {
			Action   []string
			Resource []string
		}
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, doc)
	}
	if len(parsed.Statement) != 1 {
		t.Fatalf("write should emit one statement, got %d", len(parsed.Statement))
	}
	st := parsed.Statement[0]
	if got := strings.Join(st.Action, ","); got != "s3:PutObject" {
		t.Errorf("write actions = %s", got)
	}
	if got := st.Resource[0]; got != "arn:aws:s3:::products/*" {
		t.Errorf("write resource = %s", got)
	}
	// Critically: write-only must NOT include any read action — otherwise
	// the desktop AK could read other clients' uploads.
	if strings.Contains(doc, "GetObject") || strings.Contains(doc, "ListBucket") {
		t.Errorf("write-only policy leaked a read action:\n%s", doc)
	}
}

func TestBuildScopedPolicy_ReadWrite(t *testing.T) {
	doc := buildScopedPolicy("test-bucket", PermReadWrite)
	if !strings.Contains(doc, `"s3:PutObject"`) {
		t.Error("rw missing PutObject")
	}
	if !strings.Contains(doc, `"s3:GetObject"`) {
		t.Error("rw missing GetObject")
	}
	if !strings.Contains(doc, `"s3:ListBucket"`) {
		t.Error("rw missing ListBucket")
	}
	if !strings.Contains(doc, `"arn:aws:s3:::test-bucket"`) {
		t.Error("rw missing bucket-level ARN")
	}
	if !strings.Contains(doc, `"arn:aws:s3:::test-bucket/*"`) {
		t.Error("rw missing object-level ARN")
	}
}

func TestPermissionIsValid(t *testing.T) {
	for _, p := range []Permission{PermRead, PermWrite, PermReadWrite} {
		if !p.IsValid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []Permission{"", "admin", "rwx", "rw "} {
		if p.IsValid() {
			t.Errorf("%q should NOT be valid", p)
		}
	}
}
