package handlers

import (
	"strings"
	"testing"
)

func TestSummarize_AnonymousDownload(t *testing.T) {
	policy := `{
		"Version": "2012-10-17",
		"Statement": [
			{"Effect": "Allow", "Principal": {"AWS": ["*"]},
			 "Action": ["s3:GetBucketLocation", "s3:ListBucket"],
			 "Resource": ["arn:aws:s3:::products"]},
			{"Effect": "Allow", "Principal": {"AWS": ["*"]},
			 "Action": ["s3:GetObject"],
			 "Resource": ["arn:aws:s3:::products/*"]}
		]
	}`
	clauses := summarizeBucketPolicy(policy)
	if len(clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(clauses))
	}
	for i, c := range clauses {
		if !c.Allow {
			t.Errorf("clause %d should be Allow", i)
		}
		if !strings.Contains(c.Summary, "任何人") {
			t.Errorf("clause %d should mention anonymous principal: %q", i, c.Summary)
		}
	}
	if !strings.Contains(clauses[0].Summary, "列出对象") {
		t.Errorf("clause 0 should mention list, got %q", clauses[0].Summary)
	}
	if !strings.Contains(clauses[1].Summary, "下载") {
		t.Errorf("clause 1 should mention download, got %q", clauses[1].Summary)
	}
	// ARN should be shortened — no long arn:aws:s3::: prefix in summary
	for i, c := range clauses {
		if strings.Contains(c.Summary, "arn:aws:s3:::") {
			t.Errorf("clause %d still contains full ARN: %q", i, c.Summary)
		}
	}
}

func TestSummarize_PutOnlyScopedPolicy(t *testing.T) {
	policy := `{
		"Version": "2012-10-17",
		"Statement": [
			{"Effect": "Allow",
			 "Action": ["s3:PutObject"],
			 "Resource": ["arn:aws:s3:::products/*"]}
		]
	}`
	clauses := summarizeBucketPolicy(policy)
	if len(clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(clauses))
	}
	if !strings.Contains(clauses[0].Summary, "上传") {
		t.Errorf("should mention upload: %q", clauses[0].Summary)
	}
}

func TestSummarize_UnparseableReturnsNil(t *testing.T) {
	if got := summarizeBucketPolicy("{not json"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
	if got := summarizeBucketPolicy(""); got != nil {
		t.Errorf("expected nil for empty, got %+v", got)
	}
}
