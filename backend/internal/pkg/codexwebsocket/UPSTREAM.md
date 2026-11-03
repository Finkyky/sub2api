# Isolated Codex WebSocket transport

This package is a source copy of `github.com/coder/websocket v1.8.14` from the
project's pinned module cache, including its upstream tests and `LICENSE.txt`.
Imports are rewritten to this internal package. The ordinary coder/websocket
dependency and all other provider/frontend WebSocket paths remain unchanged.
This is compiled into the existing Go binary; it adds no deployment process.

Local changes are limited to `dial.go`, `compress.go` and `write.go`:

- Opt-in `DialOptions.CompressionClientMaxWindowBits` offers the RFC 7692
  `client_max_window_bits` parameter and implements negotiated windows 9..15
  using the existing pinned klauspost/compress dependency, rather than only
  changing the advertised header.
- Validate negotiated window sizes, duplicate parameters and unoffered client
  window restrictions; retain the inherited decompressor and context-takeover
  semantics.
- Opt-in `DialOptions.SingleFrameMessages` sends each buffered `Write` as one
  final data frame, preserving compression context across messages. Streaming
  `Writer` retains its existing streaming behavior.
- Existing default options retain the upstream compressor and framing path.

Both options are enabled only by the fixed-profile Codex outbound adapter.
Compressed byte streams are not claimed identical to the official Rust
implementation. Upstream security fixes must be explicitly ported to this copy.

The copied browser integration test is opt-in via `CODEX_WEBSOCKET_WASM_TESTS`
instead of generic `CI`, so normal backend CI does not require a browser runner.
Additional local tests cover window negotiation, actual DEFLATE back-reference
distances for windows 8..15, context takeover, mixed streaming/buffered writes,
and a real official wire fixture at the service boundary.

Negotiation follows the pinned official fork `openai-oss-forks/tungstenite-rs`,
revision `4fffad30fe373adbdcffab9545e9e9bf4f2fc19f`,
`src/extensions/compression/deflate/config.rs`: reject window 8 in Codex mode,
and accept a valueless server `client_max_window_bits` as no restriction.
The underlying compressor is also tested at 8 bits but Codex negotiation never
selects it. The matching `deflate/mod.rs` compresses even empty data messages.

Tests: `go test ./internal/pkg/codexwebsocket/...` from `backend`.
