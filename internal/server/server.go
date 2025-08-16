package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/yourname/raw-cacher-go/internal/cache"
	"github.com/yourname/raw-cacher-go/internal/httpx"
)

// Store is the minimal storage interface satisfied by your MinIO store.
// You can swap implementations (e.g., filesystem) without changing the handler.
type Store interface {
	HasObject(ctx context.Context, key string) (bool, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, map[string]string, error)
	PutObject(ctx context.Context, key string, data []byte, contentType string) error
	ReadMeta(ctx context.Context, key string) (cache.Meta, bool, error)
	WriteMeta(ctx context.Context, key string, m cache.Meta) error
}

type Server struct {
	Store          Store
	Client         *http.Client
	TTLDefault     int
	TTL404         int
	ServeIfPresent bool
	sf             singleflight.Group
}

func NewServer(store Store, ttlDefault, ttl404 int, serveIf bool) *Server {
	return &Server{
		Store:          store,
		Client:         httpx.NewUpstreamClient(),
		TTLDefault:     ttlDefault,
		TTL404:         ttl404,
		ServeIfPresent: serveIf,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	domain, route, upstreamURL, err := parseAndBuildUpstream(r.URL.Path, r.URL.RawQuery)
	if err != nil {
		http.Error(w, "path must be /<domain>/<route>", http.StatusBadRequest)
		return
	}

	objKey := cache.ObjectKey(domain, route)
	metaKey := cache.MetaKey(domain, route)

	// Fast path: serve from cache if present (optional policy)
	if s.ServeIfPresent {
		if ok, _ := s.Store.HasObject(ctx, objKey); ok {
			if s.serveFromCache(ctx, w, objKey) {
				return
			}
		}
	}

	// Load metadata and decide based on TTL/negative cache
	meta, hasMeta, _ := s.Store.ReadMeta(ctx, metaKey)
	if hasMeta && cache.IsNegativeFresh(meta, s.TTL404) {
		http.Error(w, "Upstream negative-cached 404", http.StatusNotFound)
		return
	}
	if hasMeta && cache.IsFresh(meta, s.TTLDefault) {
		if ok, _ := s.Store.HasObject(ctx, objKey); ok {
			if s.serveFromCache(ctx, w, objKey) {
				return
			}
		}
	}

	// Consolidate concurrent misses per key
	v, err, _ := s.sf.Do(objKey, func() (any, error) {
		// Re-check under singleflight
		meta, hasMeta, _ = s.Store.ReadMeta(ctx, metaKey)
		if hasMeta && cache.IsNegativeFresh(meta, s.TTL404) {
			return fetchResult{kind: kindNotFound}, nil
		}
		if hasMeta && cache.IsFresh(meta, s.TTLDefault) {
			if ok, _ := s.Store.HasObject(ctx, objKey); ok {
				return fetchResult{kind: kindServeCache}, nil
			}
		}

		fr, err := download(ctx, s.Client, upstreamURL, meta)
		if err != nil {
			return nil, err
		}

		switch {
		case fr.notModified && hasMeta:
			meta.CachedAt = cache.NowISO()
			_ = s.Store.WriteMeta(ctx, metaKey, meta)
			return fetchResult{kind: kindServeCache}, nil

		case fr.status == http.StatusNotFound:
			_ = s.Store.WriteMeta(ctx, metaKey, cache.Meta{
				CachedAt: cache.NowISO(),
				TTL:      s.TTL404,
				Neg:      true,
			})
			return fetchResult{kind: kindNotFound}, nil

		case fr.status < 200 || fr.status >= 300:
			return fetchResult{kind: kindUpstreamError, status: fr.status}, nil

		default:
			if err := persist(ctx, s.Store, objKey, metaKey, fr, s.TTLDefault); err != nil {
				return nil, err
			}
			return fetchResult{
				kind:         kindWroteBody,
				body:         fr.body,
				contentType:  fr.contentType,
				etag:         fr.etag,
				lastModified: fr.lastModified,
			}, nil
		}
	})

	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}

	res, _ := v.(fetchResult)
	switch res.kind {
	case kindServeCache:
		if s.serveFromCache(ctx, w, objKey) {
			return
		}
		http.Error(w, "cache read failed", http.StatusInternalServerError)

	case kindNotFound:
		http.Error(w, "Upstream 404", http.StatusNotFound)

	case kindUpstreamError:
		if res.status >= 400 && res.status <= 599 {
			http.Error(w, "Upstream error", res.status)
		} else {
			http.Error(w, "Upstream error", http.StatusBadGateway)
		}

	case kindWroteBody:
		ct := res.contentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		if res.etag != "" {
			w.Header().Set("ETag", res.etag)
		}
		if res.lastModified != "" {
			w.Header().Set("Last-Modified", res.lastModified)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(res.body)), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(res.body)

	default:
		http.Error(w, "unexpected state", http.StatusInternalServerError)
	}
}

// download fetches from the upstream URL with conditional headers if available.
func download(ctx context.Context, client *http.Client, url string, prior cache.Meta) (fetched, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if prior.ETag != "" {
		req.Header.Set("If-None-Match", prior.ETag)
	}
	if prior.LastModified != "" {
		req.Header.Set("If-Modified-Since", prior.LastModified)
	}

	_ = time.Now() // placeholder if you want to add timings/metrics later
	resp, err := client.Do(req)
	if err != nil {
		return fetched{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return fetched{status: resp.StatusCode, notModified: true}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fetched{}, err
	}
	ct, etag, lm := extractHeaders(resp.Header)

	return fetched{
		status:       resp.StatusCode,
		body:         body,
		contentType:  ct,
		etag:         etag,
		lastModified: lm,
	}, nil
}

// persist writes the object and metadata to storage.
func persist(ctx context.Context, st Store, objKey, metaKey string, fr fetched, ttlDefault int) error {
	if err := st.PutObject(ctx, objKey, fr.body, fr.contentType); err != nil {
		return err
	}
	meta := cache.Meta{
		ETag:         fr.etag,
		LastModified: fr.lastModified,
		CachedAt:     cache.NowISO(),
		TTL:          ttlDefault,
		Size:         int64(len(fr.body)),
		Neg:          false,
	}
	return st.WriteMeta(ctx, metaKey, meta)
}

// parseAndBuildUpstream extracts <domain> and <route> from /<domain>/<route>
// and builds https://<domain>/<route>?<rawQuery>.
func parseAndBuildUpstream(path, rawQuery string) (string, string, string, error) {
	p := strings.TrimPrefix(path, "/")
	i := strings.IndexByte(p, '/')
	if i <= 0 {
		return "", "", "", http.ErrNotSupported
	}
	domain := p[:i]
	route := p[i+1:]

	url := "https://" + strings.TrimRight(domain, "/") + "/" + strings.TrimLeft(route, "/")
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	return domain, route, url, nil
}

// serveFromCache streams a cached object to the client.
func (s *Server) serveFromCache(ctx context.Context, w http.ResponseWriter, key string) bool {
	rc, size, hdrs, err := s.Store.GetObject(ctx, key)
	if err != nil {
		return false
	}
	defer rc.Close()
	for k, v := range hdrs {
		if v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
	return true
}

// extractHeaders returns Content-Type, ETag, Last-Modified from response headers.
func extractHeaders(h http.Header) (contentType, etag, lastModified string) {
	contentType = h.Get("Content-Type")
	etag = h.Get("ETag")
	lastModified = h.Get("Last-Modified")
	return
}

type fetchKind int

const (
	kindServeCache fetchKind = iota + 1
	kindNotFound
	kindUpstreamError
	kindWroteBody
)

type fetched struct {
	status       int
	notModified  bool
	body         []byte
	contentType  string
	etag         string
	lastModified string
}

type fetchResult struct {
	kind         fetchKind
	status       int
	body         []byte
	contentType  string
	etag         string
	lastModified string
}
