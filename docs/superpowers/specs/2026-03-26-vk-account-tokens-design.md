# VK Account Tokens — Design Spec

## Problem

Each anonymous VK connection requires 6 HTTP requests (4 to VK API + 2 to OK API). With N parallel connections, VK rate-limits (error codes 6/9/14/29) block for 5+ minutes. `AllocateGradual` mitigates this with batching, but the first batch still takes ~5 seconds and risks rate-limiting.

## Solution

Allow users to provide VK account access tokens. Authorized tokens use a shorter auth flow (1-3 requests instead of 6) through a different API path, with rate-limits tied to the account rather than the anonymous pool. This guarantees at least N connections (where N = number of tokens) connect without rate-limit risk.

## Discovery: Authorized VK Flow

Analysis of VK web client HAR data revealed the authorized flow:

```
Anonymous (current):                          Authorized (new):
1. login.vk.ru get_anonym_token               1. api.vk.com messages.getCallToken
2. api.vk.ru getAnonymousAccessTokenPayload      → {token: "$...", api_base_url}
3. login.vk.ru get_anonym_token               2. calls.okcdn.ru auth.anonymLogin
4. api.vk.ru calls.getAnonymousToken             (version:3, auth_token from step 1)
5. calls.okcdn.ru auth.anonymLogin            3. calls.okcdn.ru joinConversationByLink
   (version:2, no auth_token)                    (NO anonymToken needed)
6. calls.okcdn.ru joinConversationByLink
   (with anonymToken from step 4)
```

Key differences:
- `messages.getCallToken(env=production)` — 1 VK API call returns OK auth_token
- `auth.anonymLogin` with `version:3` + `auth_token` field in `session_data`
- `joinConversationByLink` without `anonymToken` parameter — only `session_key` needed

## Token Formats

Two formats accepted, detected by prefix:

| Format | Prefix | Processing |
|--------|--------|-----------|
| VK access_token | `vk1.a.` | Call `messages.getCallToken` → get OK auth_token |
| OK auth_token | `$` | Use directly, log warning about unknown TTL |

VK access_token lifetime: ~24h (default) or permanent (with `offline` scope).
OK auth_token lifetime: unknown, appears tied to account not session.

On VK API error 5 (expired token): log warning, fall back to anonymous flow for that connection.
Error 5 is distinct from rate-limit errors — needs separate check in `vkGetCallToken`.

## Allocation Strategy

Tokens are allocated in parallel (no rate-limit risk between different accounts). Anonymous connections use `AllocateGradual` with batching. Both phases run concurrently.

```
Example: --conns 8, 3 tokens

Phase 1 (parallel, ~3 sec):
  conn 0 → token1 → FetchJoinInfoWithToken → AllocateWithCredentials → relay batch → DTLS
  conn 1 → token2 → FetchJoinInfoWithToken → AllocateWithCredentials → relay batch → DTLS
  conn 2 → token3 → FetchJoinInfoWithToken → AllocateWithCredentials → relay batch → DTLS
  MUX ready after first successful ↑

Phase 2 (batched via AllocateGradual, concurrent with phase 1):
  conn 3-4 → anonymous batch 1 → SendRelayBatch → DTLS → AddConn
  conn 5-6 → anonymous batch 2 → ...
  conn 7   → anonymous batch 3 (final) → ...
```

Signaling initialization sequencing:
1. Try first token → `FetchJoinInfoWithToken(token1)` → use its `JoinInfo` for signaling
2. If first token fails (expired/error) → fall back to anonymous `FetchJoinInfo()` for signaling
3. Once signaling is established, launch both phases concurrently

All relay addresses exchanged through one signalingClient (same as current flow).

Edge cases:
- `len(tokens) >= numConns` → phase 2 has zero anonymous connections (no `AllocateGradual`)
- `len(tokens) > numConns` → only first `numConns` tokens used
- Duplicate tokens → deduplicate with warning log

## CLI Interface

```bash
# Client — repeatable --vk-token flag (0-16)
callvpn client --link=<vk_link> --vk-token=vk1.a.XXX --vk-token='$YYY' --conns=4

# Server — same flag
callvpn server --link=<vk_link> --vk-token=vk1.a.XXX

# Environment variable alternative
VK_TOKENS=token1,token2 callvpn client --link=<vk_link>

# Token acquisition helper
callvpn token [--app-id=6287487]
```

## Mobile Interface (Android)

```go
type TunnelConfig struct {
    // ... existing fields ...
    VKTokens []string // 0-16 tokens, stored in connection profile
}
```

Android UI provides input fields with "+" button. Tokens saved in connection profiles.
`mobile/bind/` receives string slice, no UI logic.

## Token Acquisition

`callvpn token` command + `GET_TOKEN.md` documentation:
1. Constructs VK OAuth implicit flow URL with `offline` scope
2. Default `client_id=6287487` (VK's own), overridable via `--app-id`
3. Opens URL in browser (or prints it)
4. User authorizes, copies token from redirect URL fragment
5. Token is permanent (`expires_in=0`) with `offline` scope

Future: embedded OAuth callback server for automatic token capture.

## File Changes

### New files
- `GET_TOKEN.md` — step-by-step token acquisition guide
- `cmd/token/main.go` — `callvpn token` CLI command

### Modified files

**`internal/provider/provider.go`**
- New optional interface (does NOT extend `Service` — avoids breaking Telemost provider):
```go
type TokenAuthProvider interface {
    FetchJoinInfoWithToken(ctx context.Context, token string) (*JoinInfo, error)
}
```
- Call sites use type assertion: `if tap, ok := svc.(provider.TokenAuthProvider); ok { ... }`

**`internal/provider/vk/vk.go`**
- `FetchJoinInfoWithToken(ctx, token)` — 3-step authorized flow
- `vkGetCallToken(ctx, client, ua, accessToken)` — calls `messages.getCallToken`
- `okAuthWithToken(ctx, client, ua, authToken, deviceID)` — `auth.anonymLogin` with version:3
- `okJoinConferenceNoAnon(ctx, client, ua, link, sessionKey)` — join without anonymToken
- `classifyToken(token)` — detect `vk1.a.` vs `$` prefix
- `resolveOKAuthToken(ctx, client, ua, token)` — resolve to OK auth_token (passthrough for `$`, API call for `vk1.a.`)

**`internal/turn/manager.go`**
- `AllocateWithCredentials(ctx context.Context, creds *provider.Credentials) (*Allocation, error)` — create TURN allocation from pre-fetched credentials

**`internal/client/client.go`**
- `Config` struct: add `VKTokens []string`
- `connectRelaySession`: two concurrent goroutines:
  - Token goroutine: parallel `FetchJoinInfoWithToken` + `AllocateWithCredentials` + relay batch exchange
  - Anonymous goroutine: `AllocateGradual` + batched relay exchange (existing flow)

**`internal/server/server.go`**
- `Config` struct: add `VKTokens []string`
- Server uses tokens for its own TURN allocations reactively: when receiving a client batch,
  if tokens are available, use `FetchJoinInfoWithToken` + `AllocateWithCredentials` for the
  matching allocations instead of anonymous `Allocate`. Tokens consumed in order, excess
  batches fall back to anonymous.

**`mobile/bind/tunnel.go`**
- `TunnelConfig.VKTokens []string`
- `connectRelay`: integrate token-based allocation alongside anonymous

**`cmd/client/main.go`**
- Repeatable `--vk-token` flag (StringSlice or custom)
- `VK_TOKENS` env var parsing

**`cmd/server/main.go`**
- Same flags as client

## Error Handling

| Error | Action |
|-------|--------|
| VK API error 5 (token expired) | Warning log, skip token, fall back to anonymous |
| VK API error 6/9/29 (rate limit) | Retry per `createAllocationWithRetry` logic |
| VK API error 14 (captcha) | Skip token, fall back to anonymous |
| OK auth_token rejected | Warning log, skip token, fall back to anonymous |
| All tokens failed | Full anonymous flow (existing behavior) |

## Shell Escaping Note

OK auth_tokens start with `$` which triggers variable expansion in bash.
CLI examples use single quotes: `--vk-token='$YYY'`.
`VK_TOKENS` env var requires escaping: `VK_TOKENS='$token1,vk1.a.token2'`.
`GET_TOKEN.md` documents this explicitly.

## Out of Scope

- `cmd/server-ui/main.go` — token support can be added later if needed
- Automatic token refresh / OAuth callback server
- Token storage/encryption on CLI side

## Backward Compatibility

- Zero tokens = current behavior (fully anonymous)
- No changes to signaling protocol (relay exchange is token-agnostic)
- `relayData` struct unchanged
- Existing `FetchJoinInfo()` untouched
