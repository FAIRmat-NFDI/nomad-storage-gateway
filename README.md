# nomad-storage-gateway

Go data-plane for public NOMAD downloads. After NOMAD authenticates a published
upload, it 307s the client here (`GET /zip/{upload_id}`). The gateway looks the
object up in the SeaweedFS filer and 307s again to a SeaweedFS or cloud S3
presigned GET.

`GET /health` is unsigned. Zip routes require a SigV4 query-presigned URL.

## Public signing authority

NOMAD and this gateway must sign and verify against the **same public URL
prefix**. The HMAC binds that reconstructed URL, not the in-cluster host.

| Piece | Must be |
| --- | --- |
| NOMAD `fs.public_fs.redirects.public_endpoint_url` | e.g. `https://nomad-lab.eu/files` |
| Gateway `seaweedfs.public_endpoint` (`NOMAD_SEAWEEDFS__PUBLIC_ENDPOINT`) | the same value |
| Ingress | StripPrefix `/files` so Chi sees `/zip/{upload_id}` |
| SeaweedFS S3 keys | the same `key` / `secret` as `fs.public_fs.extra` |

NOMAD mints `GET {public_endpoint}/zip/{upload_id}?X-Amz-...`. The gateway
verifies `PublicEndpoint +` the Chi path (`/files` + `/zip/{id}`).

A cluster URL, a missing `/files`, a double `/files/files`, or enabling `zip`
without `public_endpoint_url` (hostless `/zip/{id}`) 403s every download.

Lab without an ingress prefix can set both sides to `http://localhost:3333`.

## Run

```bash
just run    # loads config.yaml, env NOMAD_* overrides
just test
```
