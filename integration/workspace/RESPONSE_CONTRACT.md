# Shared response fixture

Run `go test -count=1 -tags workspace_e2e -run
'^TestWorkspaceUnifiedResponseContract$' -timeout 180s ./integration/workspace`
from fastls-rpc, alongside the reviewed Fetch and MITM sibling checkouts named
`fastls-fetch-ftransport` and `fastls-mitm-ftransport`.

By default all four executables are built with `GOWORK=off`. To test reviewed
development sources, set **both** `GOWORK` and `FASTLS_CONTRACT_GOWORK` to the
same absolute go.work path. The test process and every child build must use the
same dependency selection. CI runs these modes separately on three platforms
and two Go versions. No production module versions or checksums are rewritten.

One H1, TLS-H1 and TLS-H2 fixture serves net/http, core, Fetch, JSON-RPC and
gRPC, directly and through default MITM. Checks cover binary wire bodies,
repeated headers, cookies, trailers, gzip wire/display views, returned and
followed redirects, final URL, actual client-facing protocol, fixed-length
truncation, aborted streams, header/body total deadlines and caller cancellation
reaching the origin. Local certificates are trusted explicitly by the optional
Python adapter; the Go reference client skips verification for these local
fixtures only. This is not a production TLS configuration recommendation.

Known differences are asserted explicitly:

- MITM returns redirects; the client/service owns subsequent requests and the
  final URL. Its downstream protocol is H1 even when the origin negotiated H2.
- Services retain encoded `body_base64` and wire headers while the legacy
  `body` field is a decoded UTF-8 display string. Raw core/net/http bodies do
  not decode gzip unless requested; a malformed gzip body is therefore only a
  decode error at a decoding entry.
- Core uses CONNECT even for an HTTP destination. Default MITM expects TLS
  after CONNECT and does not accept this cleartext CONNECT combination. The
  test requires a request failure for core/Fetch/RPC on that route; it does not
  count it as a successful forwarding case. Ordinary absolute-form HTTP proxy
  forwarding is separately exercised by net/http and MITM's binary suite.
- Service DTOs expose total timeout classification, not separate phase knobs.
  MITM returns 502 before response headers and aborts after headers; it does
  not provide a service error envelope. Core/FFI phase timeouts remain separate.
- Policy MITM intentionally closes its one-shot upstream client per request;
  default CONNECT reuses its upstream connection. The MITM repository uses
  the same wire fixture for both modes with explicit connection expectations.

Optional same-origin Python/C ABI checks are enabled by setting
`FASTLS_CONTRACT_PYTHON` and `FASTLS_CONTRACT_FFI_SCRIPT`. The adapter owns its
library choice and must record the exact binary hash. These optional checks are
not counted as part of the Go CI matrix when those variables are absent.
