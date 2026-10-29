# Consolidated Go candidate

The core revision is pinned with the coordinated Go services in `go.mod`.
Public JSON-RPC/gRPC APIs remain unchanged. The workspace response contract now
requires successful HTTP forwarding through MITM instead of accepting the old
cleartext CONNECT failure. It applies the same body/header/cancellation checks
to that route as to HTTPS and direct requests.

Coordination accepts either legacy `*-ftransport` checkout names or isolated
canonical repository names. CI provides development Linux/Windows feedback and
retains three platforms, both toolchains and Linux race for frozen candidates.
The aggregate gate rejects failed, cancelled, missing or unexpected skipped jobs;
race and coverage share one execution.

JSON-RPC and gRPC archives are generated under RUNNER_TEMP outside the checkout.
Source cleanliness is checked before and after target builds, preserving actual
VCS metadata. Existing v0.1.1 release bytes and their evidence remain unchanged.
