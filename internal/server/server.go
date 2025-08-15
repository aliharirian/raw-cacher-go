package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/sync/singleflight"

	"github.com/yourname/raw-cacher-go/internal/cache"
	"github.com/yourname/raw-cacher-go/internal/httpx"
	"github.com/yourname/raw-cacher-go/internal/storage"
)

type Server struct {
	Store          *storage.Store
	Client         *http.Client
	TTLDefault     int
	TTL404         int
	ServeIfPresent bool
	sf             singleflight.Group
}

func NewServer(store *storage.Store, ttlDefault, ttl404 int, serveIf bool) *Server {
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

	domain, route, err := splitDomainAndRoute(r.URL.Path)
	if err != nil {
		http.Error(w, "path must be /<domain>/<route>", http.StatusBadRequest)
		return
	}
	upstreamURL := buildUpstreamURL(domain, route, r.URL.RawQuery)

	objKey := cache.ObjectKey(domain, route)
	metaKey := cache.MetaKey(domain, route)

	// Serve directly from cache if configured
	if s.ServeIfPresent {
		if ok, _ := s.Store.HasObject(ctx, objKey); ok {
			if s.serveFromCache(ctx, w, objKey) {
				return
			}
		}
	}

	// Load metadata
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

	// Consolidate concurrent MISS for the same key
	v, err, _ := s.sf.Do(objKey, func() (any, error) {
		// Re-check inside singleflight
		meta, hasMeta, _ = s.Store.ReadMeta(ctx, metaKey)
		if hasMeta && cache.IsNegativeFresh(meta, s.TTL404) {
			return fetchResult{kind: kindNotFound}, nil
		}
		if hasMeta && cache.IsFresh(meta, s.TTLDefault) {
			if ok, _ := s.Store.HasObject(ctx, objKey); ok {
				return fetchResult{kind: kindServeCache}, nil
			}
		}

		// Prepare conditional request
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
		if hasMeta {
			if meta.ETag != "" {
				req.Header.Set("If-None-Match", meta.ETag)
			}
			if meta.LastModified != "" {
				req.Header.Set("If-Modified-Since", meta.LastModified)
			}
		}

		resp, err := s.Client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		// 304 → refresh meta timestamp and serve from cache
		if resp.StatusCode == http.StatusNotModified && hasMeta {
			meta.CachedAt = cache.NowISO()
			_ = s.Store.WriteMeta(ctx, metaKey, meta)
			return fetchResult{kind: kindServeCache}, nil
		}

		// 404 → negative cache
		if resp.StatusCode == http.StatusNotFound {
			_ = s.Store.WriteMeta(ctx, metaKey, cache.Meta{
				CachedAt: cache.NowISO(),
				TTL:      s.TTL404,
				Neg:      true,
			})
			return fetchResult{kind: kindNotFound}, nil
		}

		// Other non-2xx
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fetchResult{kind: kindUpstreamError, status: resp.StatusCode}, nil
		}

		// Read full body (simple path). For large files, consider streaming.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		ct := resp.Header.Get("Content-Type")
		etag := resp.Header.Get("ETag")
		lm := resp.Header.Get("Last-Modified")

		if err := s.Store.PutObject(ctx, objKey, body, ct); err != nil {
			return nil, err
		}
		_ = s.Store.WriteMeta(ctx, metaKey, cache.Meta{
			ETag:         etag,
			LastModified: lm,
			CachedAt:     cache.NowISO(),
			TTL:          s.TTLDefault,
			Size:         int64(len(body)),
			Neg:          false,
		})

		return fetchResult{
			kind:         kindWroteBody,
			body:         body,
			contentType:  ct,
			etag:         etag,
			lastModified: lm,
		}, nil
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
		return

	case kindNotFound:
		http.Error(w, "Upstream 404", http.StatusNotFound)
		return

	case kindUpstreamError:
		if res.status >= 400 && res.status <= 599 {
			http.Error(w, "Upstream error", res.status)
			return
		}
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return

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
		return

	default:
		http.Error(w, "unexpected state", http.StatusInternalServerError)
		return
	}
}

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

func splitDomainAndRoute(path string) (string, string, error) {
	p := strings.TrimPrefix(path, "/")
	i := strings.IndexByte(p, '/')
	if i <= 0 {
		return "", "", http.ErrNotSupported
	}
	return p[:i], p[i+1:], nil
}

func buildUpstreamURL(domain, route, rawQuery string) string {
	u := "https://" + strings.TrimRight(domain, "/") + "/" + strings.TrimLeft(route, "/")
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

type fetchKind int

const (
	kindServeCache fetchKind = iota + 1
	kindNotFound
	kindUpstreamError
	kindWroteBody
)

type fetchResult struct {
	kind         fetchKind
	status       int
	body         []byte
	contentType  string
	etag         string
	lastModified string
}
