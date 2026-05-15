package minioadm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// ObjectInfo is a slimmed view of minio.ObjectInfo for templates / handlers.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
	StorageClass string
}

// ListObjectsResult is one page of objects + optional common prefixes when a
// delimiter is in use. NextCursor is empty when there are no more pages; pass
// it back as startAfter to fetch the next page.
type ListObjectsResult struct {
	Objects    []ObjectInfo
	Prefixes   []string
	NextCursor string
	Truncated  bool
	Scanned    int
}

// AllowedPageSizes are the choices the UI offers. Keep in sync with the
// <select> options in templates/objects.html.
var AllowedPageSizes = []int{100, 200, 500, 1000}

// NormalizePageSize clamps user input to one of AllowedPageSizes, defaulting to
// 100. This is enforced server-side so the UI's <select> can't be bypassed.
func NormalizePageSize(n int) int {
	for _, v := range AllowedPageSizes {
		if v == n {
			return v
		}
	}
	return 100
}

// ListObjectsPage returns a single page of objects under prefix. The minio-go
// channel API doesn't expose continuation tokens directly — we read pageSize
// items, then use the last key as startAfter for the next page. Mixed with
// minio-go's own MaxKeys it produces a clean cursor scheme for the UI.
//
// When delimiter is "/", listing is "directory style": only the immediate
// children of `prefix` are returned, with sub-prefixes in Prefixes. When
// delimiter is "" the listing is recursive.
func (c *Client) ListObjectsPage(ctx context.Context, bucket, prefix, delimiter, startAfter string, pageSize int) (*ListObjectsResult, error) {
	pageSize = NormalizePageSize(pageSize)
	// Cancel the inner ListObjects goroutine as soon as we have a full page,
	// otherwise it keeps streaming objects we no longer read.
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts := minio.ListObjectsOptions{
		Prefix:     prefix,
		Recursive:  delimiter == "",
		StartAfter: startAfter,
		MaxKeys:    pageSize, // hint to minio-go; we still enforce the cap
	}
	res := &ListObjectsResult{}
	prefixSeen := make(map[string]bool)
	for obj := range c.S3.ListObjects(listCtx, bucket, opts) {
		if obj.Err != nil {
			return nil, fmt.Errorf("ListObjects: %w", obj.Err)
		}
		res.Scanned++
		// minio-go marks common prefixes by trailing "/" in Key and Size == 0
		// when Recursive=false. Surface them separately.
		if delimiter == "/" && strings.HasSuffix(obj.Key, "/") {
			if !prefixSeen[obj.Key] {
				prefixSeen[obj.Key] = true
				res.Prefixes = append(res.Prefixes, obj.Key)
			}
		} else {
			res.Objects = append(res.Objects, ObjectInfo{
				Key:          obj.Key,
				Size:         obj.Size,
				LastModified: obj.LastModified,
				ETag:         strings.Trim(obj.ETag, `"`),
				ContentType:  obj.ContentType,
				StorageClass: obj.StorageClass,
			})
		}
		if len(res.Objects)+len(res.Prefixes) >= pageSize {
			res.Truncated = true
			// Cursor: the last object key OR last prefix, whichever appeared
			// last in the listing (the channel yields in lexicographic order).
			res.NextCursor = obj.Key
			break
		}
	}
	return res, nil
}

// StatObject returns metadata for a single object. Wraps minio-go's StatObject
// to project into our trimmed ObjectInfo type and decode its error shape.
func (c *Client) StatObject(ctx context.Context, bucket, key string) (*ObjectInfo, error) {
	st, err := c.S3.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	return &ObjectInfo{
		Key:          st.Key,
		Size:         st.Size,
		LastModified: st.LastModified,
		ETag:         strings.Trim(st.ETag, `"`),
		ContentType:  st.ContentType,
		StorageClass: st.StorageClass,
	}, nil
}

// RemoveObject deletes one object. Versioning is not enabled in this
// deployment, so there's no version ID to worry about.
func (c *Client) RemoveObject(ctx context.Context, bucket, key string) error {
	return c.S3.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

// PresignedGetURL returns a presigned GET URL for an object. The URL targets
// PublicHost (so the browser can reach it via Caddy) and is valid for `expires`.
// Returns an error if PublicHost wasn't configured at startup.
func (c *Client) PresignedGetURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	if c.Presign == nil {
		return "", fmt.Errorf("presign disabled: PUBLIC_HOST not configured")
	}
	// reqParams lets us force Content-Disposition; leaving it nil means the
	// browser decides based on Content-Type (preview if known, download otherwise).
	u, err := c.Presign.PresignedGetObject(ctx, bucket, key, expires, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// ScanResult is what BulkDeletePreview returns: the candidate set + totals,
// plus a `Cutoff` echo of the time threshold used (so the confirm form can
// carry it forward verbatim — see ConfirmCutoff in handlers).
type ScanResult struct {
	Cutoff     time.Time
	Prefix     string
	Objects    []ObjectInfo
	Count      int
	TotalBytes int64
	Truncated  bool // hit ScanLimit before listing finished
	Scanned    int  // total objects iterated (matched + skipped)
}

// ScanLimit caps how many *matching* objects a single bulk operation will
// process. Keeps both the preview page and the synchronous delete bounded;
// anything above this should really be expressed as a lifecycle rule.
const ScanLimit = 10000

// ScanByAge walks bucket+prefix and returns objects whose LastModified is
// strictly before cutoff. The scan stops when ScanLimit matches are
// accumulated (Truncated=true) or the bucket is exhausted.
func (c *Client) ScanByAge(ctx context.Context, bucket, prefix string, cutoff time.Time) (*ScanResult, error) {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	res := &ScanResult{Cutoff: cutoff, Prefix: prefix}
	for obj := range c.S3.ListObjects(listCtx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("ListObjects: %w", obj.Err)
		}
		res.Scanned++
		if !obj.LastModified.Before(cutoff) {
			continue
		}
		res.Objects = append(res.Objects, ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ETag:         strings.Trim(obj.ETag, `"`),
		})
		res.Count++
		res.TotalBytes += obj.Size
		if res.Count >= ScanLimit {
			res.Truncated = true
			break
		}
	}
	return res, nil
}

// BulkRemoveResult reports outcomes from RemoveObjects. ErrorCount is the
// total number of failed deletions reported by MinIO; Errors is a *sample*
// capped at errorSampleLimit so audit log lines stay bounded — callers needing
// the true failure count must use ErrorCount, not len(Errors). Submitted <
// Requested means the feeder hit ctx cancellation before queuing every key;
// those unsubmitted keys remain in the bucket and are not counted as either
// success or error.
type BulkRemoveResult struct {
	Requested  int
	Submitted  int
	Removed    int
	ErrorCount int
	Errors     []string
}

const errorSampleLimit = 20

// BulkRemove deletes the given keys via the multi-object delete API. We track
// how many keys actually made it into the feeder channel (Submitted) so that a
// ctx cancellation mid-feed doesn't make us claim we removed more than we did.
// minio-go's RemoveObjects channel emits only errors; success count is derived
// as Submitted − len(errors-from-minio).
func (c *Client) BulkRemove(ctx context.Context, bucket string, keys []string) *BulkRemoveResult {
	result := &BulkRemoveResult{Requested: len(keys)}
	if len(keys) == 0 {
		return result
	}
	// submitted is written only by the feeder goroutine and read only after
	// the error channel from RemoveObjects closes. The errCh close happens-
	// after objCh close (RemoveObjects must drain it first), and objCh close
	// happens-after the feeder's last write, so the read is race-free.
	submitted := 0
	objCh := make(chan minio.ObjectInfo, 1)
	go func() {
		defer close(objCh)
		for _, k := range keys {
			select {
			case objCh <- minio.ObjectInfo{Key: k}:
				submitted++
			case <-ctx.Done():
				return
			}
		}
	}()
	for e := range c.S3.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		if e.Err == nil {
			continue
		}
		result.ErrorCount++
		if len(result.Errors) < errorSampleLimit {
			result.Errors = append(result.Errors, e.ObjectName+": "+e.Err.Error())
		}
	}
	result.Submitted = submitted
	result.Removed = submitted - result.ErrorCount
	if result.Removed < 0 {
		// Defensive: shouldn't happen (minio-go shouldn't emit more errors
		// than objects sent), but clamp so callers never see a negative.
		result.Removed = 0
	}
	return result
}
