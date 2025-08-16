# raw-cacher-go

**raw-cacher-go** is a lightweight HTTP reverse proxy and caching service written in Go.
It caches responses from arbitrary upstream domains (via path routing), stores them in **MinIO** (or other storage backends in the future), and serves cached content with configurable TTLs and negative-caching support.

---

## ✨ Features

* Route requests like `/example.com/path/to/file.js` → fetch `https://example.com/path/to/file.js`
* Cache responses in **MinIO** (pluggable storage layer)
* Configurable TTL for normal responses (`TTLDefault`) and `404` responses (`TTL404`)
* Negative caching for upstream 404s
* Conditional requests using `ETag` and `Last-Modified`
* Concurrent request deduplication (using `singleflight`)
* `/healthz` endpoint for monitoring
* Ready for Docker & CI/CD (semantic-release + Docker Hub + GitHub Actions)

---

## 🏗 Architecture

```
Client ---> raw-cacher-go ---> Upstream (https://domain/route)
                │
                ▼
           MinIO Storage
```

1. Parse incoming path `/domain/route`
2. Build upstream URL (`https://domain/route`)
3. Serve from cache if available (and fresh)
4. If not cached, fetch from upstream:

    * Store in MinIO with metadata (ETag, Last-Modified, TTL)
    * Serve back to client
5. Handle 404s with negative caching
6. Expose `/healthz` for service checks

---

## 🚀 Getting Started

### 1. Run with Docker

```bash
docker run -d \
  --name minio \
  -e MINIO_ROOT_USER=minio \
  -e MINIO_ROOT_PASSWORD=minio12345 \
  -p 9000:9000 \
  -p 9001:9001 \
  --restart unless-stopped \
  -v minio_data:/data \
  minio/minio:latest \
  server /data --console-address ":9001"
```
Then run the raw-cacher-go service:

```bash
docker run -d \
  -e MINIO_ENDPOINT=minio:9000 \
  -e MINIO_ACCESS_KEY=minio \
  -e MINIO_SECRET_KEY=minio123 \
  -e MINIO_BUCKET=proxy-cache \
  -p 8080:8080 \
  aliharirian/raw-cacher-go:latest
```

### 2. Example Request

```bash
curl http://localhost:8080/example.com/index.html
```

This will:

* Fetch `https://example.com/index.html` if not cached
* Save it in MinIO
* Return it from cache on subsequent requests

### 3. Health Check

```bash
curl http://localhost:8080/healthz
```

Response:

```json
{"status":"up"}
```

---

## ⚙️ Configuration

Environment variables:

| Variable           | Description                     | Default          |
| ------------------ | ------------------------------- | ---------------- |
| `MINIO_ENDPOINT`   | MinIO endpoint (host\:port)     | `localhost:9000` |
| `MINIO_ACCESS_KEY` | MinIO access key                | `minio`          |
| `MINIO_SECRET_KEY` | MinIO secret key                | `minio123`       |
| `MINIO_BUCKET`     | Bucket name                     | `proxy-cache`    |
| `TTL_DEFAULT`      | Cache TTL for normal responses  | `3600` (1h)      |
| `TTL_404`          | TTL for caching 404 responses   | `60` (1m)        |
| `SERVE_IF_PRESENT` | Serve cached object immediately | `true`           |

---

## 🔮 Roadmap

* [ ] Filesystem storage backend
* [ ] Configurable cache policies per domain
* [ ] Metrics (Prometheus exporter)

---

## 🤝 Contributing

PRs are welcome!
Please follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) for commit messages (used by semantic-release).

---

## 📄 License

```
Copyright 2025 Ali Haririan

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
