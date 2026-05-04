package handlers

import (
	"encoding/json"
	"sort"
	"strings"
)

// PolicyClause is one human-readable description of a single statement in a
// bucket policy. The template renders these as plain sentences and offers the
// raw JSON in a <details> block for the curious.
type PolicyClause struct {
	Allow   bool
	Summary string // 一行中文摘要
}

// summarizeBucketPolicy parses an S3 bucket policy JSON and emits one clause
// per Statement. Unparseable input returns nil so the template falls back to
// showing only the raw JSON. We never error — display is best-effort.
func summarizeBucketPolicy(policyJSON string) []PolicyClause {
	if strings.TrimSpace(policyJSON) == "" {
		return nil
	}
	var doc struct {
		Statement []struct {
			Effect    string          `json:"Effect"`
			Principal json.RawMessage `json:"Principal"`
			Action    json.RawMessage `json:"Action"`
			Resource  json.RawMessage `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(policyJSON), &doc); err != nil {
		return nil
	}
	out := make([]PolicyClause, 0, len(doc.Statement))
	for _, s := range doc.Statement {
		actions := flattenStrings(s.Action)
		resources := flattenStrings(s.Resource)
		principal := principalText(s.Principal)
		allow := strings.EqualFold(s.Effect, "Allow")

		clause := PolicyClause{
			Allow:   allow,
			Summary: humanizeStatement(allow, principal, actions, resources),
		}
		out = append(out, clause)
	}
	return out
}

// humanizeStatement turns the (effect, principal, actions, resources) tuple
// into a single Chinese sentence. Designed for the patterns we actually use
// (anonymous download, scoped put-only) — falls back to a literal listing for
// anything unrecognized.
func humanizeStatement(allow bool, principal string, actions, resources []string) string {
	var b strings.Builder
	if allow {
		b.WriteString("✅ 允许 ")
	} else {
		b.WriteString("❌ 拒绝 ")
	}
	b.WriteString(principal)
	b.WriteString(" 对 ")
	b.WriteString(joinShortResources(resources))
	b.WriteString(" 执行 ")
	b.WriteString(humanActions(actions))
	return b.String()
}

func principalText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "（未指定）"
	}
	// Principal can be a string or an object like {"AWS": ["*"]}.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "*" {
			return "任何人（匿名）"
		}
		return s
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, v := range obj {
			items := flattenStrings(v)
			for _, it := range items {
				if it == "*" {
					return "任何人（匿名）"
				}
			}
			if len(items) > 0 {
				return strings.Join(items, ", ")
			}
		}
	}
	return string(raw)
}

func humanActions(actions []string) string {
	// Map common actions to a short Chinese description; group reads together
	// so "GetObject + ListBucket" reads as "下载 + 列出对象" not a long string.
	hasRead, hasList, hasWrite, hasDel := false, false, false, false
	other := []string{}
	for _, a := range actions {
		switch a {
		case "s3:GetObject":
			hasRead = true
		case "s3:ListBucket", "s3:GetBucketLocation":
			hasList = true
		case "s3:PutObject":
			hasWrite = true
		case "s3:DeleteObject":
			hasDel = true
		default:
			other = append(other, a)
		}
	}
	parts := []string{}
	if hasWrite {
		parts = append(parts, "上传")
	}
	if hasRead {
		parts = append(parts, "下载")
	}
	if hasList {
		parts = append(parts, "列出对象")
	}
	if hasDel {
		parts = append(parts, "删除")
	}
	for _, o := range other {
		parts = append(parts, o)
	}
	if len(parts) == 0 {
		return "（无 action）"
	}
	return strings.Join(parts, " / ")
}

func joinShortResources(resources []string) string {
	if len(resources) == 0 {
		return "（任意资源）"
	}
	short := make([]string, 0, len(resources))
	for _, r := range resources {
		// Shorten ARNs: arn:aws:s3:::products/* → products/*
		const prefix = "arn:aws:s3:::"
		if strings.HasPrefix(r, prefix) {
			short = append(short, r[len(prefix):])
		} else {
			short = append(short, r)
		}
	}
	sort.Strings(short)
	return strings.Join(short, ", ")
}

// flattenStrings accepts either a JSON string or a JSON array of strings and
// returns it as a Go slice. Anything else returns nil.
func flattenStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}
