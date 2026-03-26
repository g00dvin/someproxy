# VK Account Tokens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support VK account access tokens (0-16) for faster, rate-limit-free TURN credential acquisition alongside the existing anonymous flow.

**Architecture:** New optional `TokenAuthProvider` interface implemented by VK provider. Authorized flow uses `messages.getCallToken` → `auth.anonymLogin(v3)` → `joinConversationByLink` (3 steps vs 6). Token connections allocated in parallel, anonymous via `AllocateGradual`, both concurrent. Single shared signaling client.

**Tech Stack:** Go, VK API (`messages.getCallToken`), OK API (`calls.okcdn.ru`), existing `pion/turn/v4`

**Spec:** `docs/superpowers/specs/2026-03-26-vk-account-tokens-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/provider/provider.go` | New `TokenAuthProvider` optional interface |
| `internal/provider/vk/vk.go` | `FetchJoinInfoWithToken` — 3-step authorized flow |
| `internal/turn/manager.go` | `AllocateWithCredentials` — TURN alloc from pre-fetched creds |
| `internal/client/client.go` | Dual-goroutine relay: token phase + anonymous phase |
| `internal/server/server.go` | Reactive token allocation in `acceptOneClient` |
| `mobile/bind/tunnel.go` | `VKTokens` in TunnelConfig + token relay flow |
| `cmd/client/main.go` | `--vk-token` repeatable flag + `VK_TOKENS` env |
| `cmd/server/main.go` | Same flags |
| `cmd/token/main.go` | Token acquisition helper (new) |
| `GET_TOKEN.md` | User-facing token guide (new) |

---

### Task 1: TokenAuthProvider interface

**Files:**
- Modify: `internal/provider/provider.go`

- [ ] **Step 1: Add `TokenAuthProvider` interface**

After the `Service` interface (around line 119), add:

```go
// TokenAuthProvider is an optional interface for providers that support
// authenticated token-based credential acquisition (fewer API calls, no rate-limit risk).
type TokenAuthProvider interface {
	FetchJoinInfoWithToken(ctx context.Context, token string) (*JoinInfo, error)
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/provider/...`
Expected: success, no consumers yet

- [ ] **Step 3: Commit**

```
feat(provider): add TokenAuthProvider optional interface
```

---

### Task 2: VK authorized flow — helper functions

**Files:**
- Modify: `internal/provider/vk/vk.go`

- [ ] **Step 1: Add token classification helper**

After the existing constants block, add:

```go
// classifyToken detects token format by prefix.
// Returns "vk" for VK access tokens (vk1.a.*), "ok" for OK auth tokens ($*).
func classifyToken(token string) string {
	if strings.HasPrefix(token, "vk1.a.") {
		return "vk"
	}
	if strings.HasPrefix(token, "$") {
		return "ok"
	}
	return "unknown"
}
```

- [ ] **Step 2: Add `vkGetCallToken` — calls `messages.getCallToken`**

Uses `vkAPIPost` for rate-limit detection (codes 6/9/14/29), plus explicit check for error code 5 (token expired) which is NOT a rate-limit error:

```go
// vkGetCallToken exchanges a VK access_token for an OK auth_token via messages.getCallToken.
// Returns the OK auth_token and api_base_url.
func vkGetCallToken(ctx context.Context, client *http.Client, ua string, accessToken string) (authToken string, apiBaseURL string, err error) {
	data := url.Values{
		"env":          {"production"},
		"access_token": {accessToken},
	}
	endpoint := fmt.Sprintf("https://api.vk.com/method/messages.getCallToken?v=%s&client_id=%s", vkAPIVersion, vkClientID)
	// Use httpPost (not vkAPIPost) so we can distinguish error 5 from rate-limit errors
	body, err := httpPost(ctx, client, ua, endpoint, data)
	if err != nil {
		return "", "", err
	}
	// Check for error 5 (expired token) first — distinct from rate-limit errors
	var errResp vkErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != nil {
		if errResp.Error.Code == 5 {
			return "", "", fmt.Errorf("VK token expired (error 5): %s", errResp.Error.Msg)
		}
		// Then check rate limits
		if rle := checkVKRateLimit(body); rle != nil {
			return "", "", rle
		}
		return "", "", fmt.Errorf("VK API error %d: %s", errResp.Error.Code, errResp.Error.Msg)
	}
	var resp struct {
		Response struct {
			Token      string `json:"token"`
			APIBaseURL string `json:"api_base_url"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", fmt.Errorf("parse getCallToken response: %w", err)
	}
	if resp.Response.Token == "" {
		return "", "", fmt.Errorf("empty token in getCallToken response: %s", string(body))
	}
	return resp.Response.Token, resp.Response.APIBaseURL, nil
}
```

- [ ] **Step 3: Add `okAuthWithToken` — auth.anonymLogin with version 3**

```go
// okAuthWithToken performs OK auth.anonymLogin with an auth_token (version 3).
// This is the authorized path — no anonymous VK token chain needed.
func okAuthWithToken(ctx context.Context, client *http.Client, ua string, authToken string, deviceID string) (string, error) {
	sessionData := fmt.Sprintf(`{"version":3,"device_id":"%s","client_version":1.1,"client_type":"SDK_JS","auth_token":"%s"}`, deviceID, authToken)
	data := url.Values{
		"session_data":    {sessionData},
		"method":          {"auth.anonymLogin"},
		"format":          {"JSON"},
		"application_key": {okAppKey},
	}
	body, err := httpPost(ctx, client, ua, "https://calls.okcdn.ru/fb.do", data)
	if err != nil {
		return "", err
	}
	var resp struct {
		SessionKey string `json:"session_key"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse auth response: %w", err)
	}
	if resp.SessionKey == "" {
		return "", fmt.Errorf("empty session_key in auth response: %s", string(body))
	}
	return resp.SessionKey, nil
}
```

- [ ] **Step 4: Refactor `okJoinConference` to accept optional anonymToken**

Instead of creating a near-duplicate `okJoinConferenceNoAnon`, modify the existing `okJoinConference` to accept an optional `anonymToken` parameter (empty string = omit):

```go
// okJoinConference joins a VK call conference.
// If anonymToken is empty, joins without it (authenticated token flow).
func okJoinConference(ctx context.Context, client *http.Client, ua string, link, anonymToken, sessionKey string) (*joinConferenceResult, error) {
	data := url.Values{
		"joinLink":        {link},
		"isVideo":         {"false"},
		"protocolVersion": {"5"},
		"method":          {"vchat.joinConversationByLink"},
		"format":          {"JSON"},
		"application_key": {okAppKey},
		"session_key":     {sessionKey},
	}
	if anonymToken != "" {
		data.Set("anonymToken", anonymToken)
	}
	// ... rest unchanged (parse response, extract TURN creds)
```

Update the existing call site in `FetchJoinInfo` to pass `token4` as anonymToken explicitly.

- [ ] **Step 5: Build**

Run: `go build ./internal/provider/...`
Expected: success

- [ ] **Step 6: Commit**

```
feat(vk): add authorized flow helper functions

vkGetCallToken, okAuthWithToken, okJoinConferenceNoAnon, classifyToken
```

---

### Task 3: FetchJoinInfoWithToken — main authorized flow

**Files:**
- Modify: `internal/provider/vk/vk.go`

- [ ] **Step 1: Add `resolveOKAuthToken` helper**

```go
// resolveOKAuthToken converts a token to an OK auth_token.
// For "vk" tokens: calls messages.getCallToken API.
// For "ok" tokens: returns as-is with a TTL warning.
func resolveOKAuthToken(ctx context.Context, client *http.Client, ua string, token string) (authToken string, err error) {
	switch classifyToken(token) {
	case "vk":
		authToken, _, err = vkGetCallToken(ctx, client, ua, token)
		return authToken, err
	case "ok":
		slog.Warn("using OK auth_token directly — TTL unknown, token may expire without warning")
		return token, nil
	default:
		return "", fmt.Errorf("unrecognized token format (expected vk1.a.* or $*): %.20s...", token)
	}
}
```

- [ ] **Step 2: Add `FetchJoinInfoWithToken` method on Service**

```go
// FetchJoinInfoWithToken implements provider.TokenAuthProvider.
// Uses the authorized 3-step flow: resolve OK auth_token → auth.anonymLogin(v3) → joinConference.
func (s *Service) FetchJoinInfoWithToken(ctx context.Context, token string) (*provider.JoinInfo, error) {
	ua := randomUserAgent()
	client := &http.Client{
		Timeout:   httpTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	// Step 1: Resolve token to OK auth_token
	authToken, err := resolveOKAuthToken(ctx, client, ua, token)
	if err != nil {
		return nil, fmt.Errorf("resolve auth token: %w", err)
	}

	// Step 2: OK auth with token (version 3)
	deviceID := uuid.New().String()
	sessionKey, err := okAuthWithToken(ctx, client, ua, authToken, deviceID)
	if err != nil {
		return nil, fmt.Errorf("ok auth with token: %w", err)
	}

	// Step 3: Join conference without anonymToken (empty string = omit)
	result, err := okJoinConference(ctx, client, ua, s.callLink, "", sessionKey)
	if err != nil {
		return nil, fmt.Errorf("join conference: %w", err)
	}

	return &provider.JoinInfo{
		Credentials: *result.creds,
		WSEndpoint:  result.endpoint,
		ConvID:      result.convID,
		DeviceIdx:   result.deviceIdx,
	}, nil
}
```

- [ ] **Step 3: Build and verify interface satisfaction**

Run: `go build ./internal/provider/...`

Add a compile-time check at the bottom of vk.go:

```go
var _ provider.TokenAuthProvider = (*Service)(nil)
```

Expected: success

- [ ] **Step 4: Commit**

```
feat(vk): implement FetchJoinInfoWithToken — 3-step authorized flow
```

---

### Task 4: AllocateWithCredentials in TURN Manager

**Files:**
- Modify: `internal/turn/manager.go`

- [ ] **Step 1: Refactor `createAllocation` — extract dial+setup into `dialAndAllocate`**

Extract the TURN dial, client setup, Listen, and Allocate logic from `createAllocation` (lines 110-183) into a shared helper that accepts credentials directly. This avoids duplicating the TCP/UDP dial logic, `client.Listen()`, write buffer settings, etc.

```go
// dialAndAllocate creates a TURN allocation from the given credentials.
// Handles TCP vs UDP dialing, client setup, Listen, and Allocate.
func (m *Manager) dialAndAllocate(ctx context.Context, creds *provider.Credentials) (*Allocation, error) {
	addr := net.JoinHostPort(creds.Host, creds.Port)

	var conn net.Conn
	var turnConn net.PacketConn
	var err error
	if m.useTCP {
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial TURN server: %w", err)
		}
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.SetWriteBuffer(16384)
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(10 * time.Second)
		}
		turnConn = pionTurn.NewSTUNConn(conn)
	} else {
		turnConn, err = net.ListenPacket("udp4", "")
		if err != nil {
			return nil, fmt.Errorf("listen UDP: %w", err)
		}
		conn = turnConn.(net.Conn)
	}

	cfg := &pionTurn.ClientConfig{
		STUNServerAddr: addr,
		TURNServerAddr: addr,
		Conn:           turnConn,
		Username:       creds.Username,
		Password:       creds.Password,
		LoggerFactory:  logging.NewDefaultLoggerFactory(),
	}

	client, err := pionTurn.NewClient(cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create TURN client: %w", err)
	}

	if err := client.Listen(); err != nil {
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("TURN listen: %w", err)
	}

	relayConn, err := client.Allocate()
	if err != nil {
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("TURN allocate: %w", err)
	}

	return &Allocation{
		Client:    client,
		RelayConn: relayConn,
		RelayAddr: relayConn.LocalAddr(),
		Creds:     creds,
		CreatedAt: time.Now(),
		conn:      conn,
	}, nil
}
```

Then simplify `createAllocation` to use it:

```go
func (m *Manager) createAllocation(ctx context.Context, idx int) (*Allocation, error) {
	creds, err := m.creds.FetchCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch credentials: %w", err)
	}
	m.logger.Info("connecting to TURN server", "index", idx, "addr", net.JoinHostPort(creds.Host, creds.Port), "username", creds.Username)
	alloc, err := m.dialAndAllocate(ctx, creds)
	if err != nil {
		return nil, err
	}
	m.logger.Info("TURN allocation succeeded", "index", idx, "relay_addr", alloc.RelayAddr.String())
	return alloc, nil
}
```

- [ ] **Step 2: Add `AllocateWithCredentials` public method**

```go
// AllocateWithCredentials creates a single TURN allocation using pre-fetched credentials.
// Used for token-based connections where credentials come from FetchJoinInfoWithToken.
func (m *Manager) AllocateWithCredentials(ctx context.Context, creds *provider.Credentials) (*Allocation, error) {
	alloc, err := m.dialAndAllocate(ctx, creds)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.allocations = append(m.allocations, alloc)
	m.mu.Unlock()
	m.logger.Info("allocated with credentials", "relay", alloc.RelayAddr.String(), "server", net.JoinHostPort(creds.Host, creds.Port))
	return alloc, nil
}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/turn/...`
Expected: success

- [ ] **Step 2: Build**

Run: `go build ./internal/turn/...`
Expected: success

- [ ] **Step 3: Commit**

```
feat(turn): add AllocateWithCredentials for pre-fetched credentials
```

---

### Task 5: Config structs + CLI flags

**Files:**
- Modify: `internal/client/client.go`
- Modify: `internal/server/server.go`
- Modify: `cmd/client/main.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add `VKTokens` to client Config**

In `internal/client/client.go`, add to the `Config` struct (after `AuthToken`):

```go
VKTokens []string // VK account tokens for authenticated TURN credential flow
```

- [ ] **Step 2: Add `VKTokens` to server Config**

In `internal/server/server.go`, add to the `Config` struct (after `AuthToken`):

```go
VKTokens []string // VK account tokens for authenticated TURN credential flow
```

- [ ] **Step 3: Add `--vk-token` repeatable flag to client**

In `cmd/client/main.go`, add a custom StringSlice flag type:

```go
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}
```

In the flag parsing block:

```go
var vkTokens stringSlice
flag.Var(&vkTokens, "vk-token", "VK account token (repeatable, 0-16)")
```

After `flag.Parse()`, add env var fallback:

```go
if len(vkTokens) == 0 {
	if env := os.Getenv("VK_TOKENS"); env != "" {
		vkTokens = strings.Split(env, ",")
	}
}
if len(vkTokens) > 16 {
	log.Fatal("maximum 16 VK tokens allowed")
}
```

Pass into `client.Config`:

```go
VKTokens: []string(vkTokens),
```

- [ ] **Step 4: Add `--vk-token` to server**

Same pattern in `cmd/server/main.go`. Inline `stringSlice` type. Add `VK_TOKENS` env var fallback. Pass into `server.Config`.

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: success

- [ ] **Step 6: Commit**

```
feat: add VKTokens to Config structs and --vk-token CLI flag
```

---

### Task 6: Client helpers — deduplication and token allocation

**Files:**
- Modify: `internal/client/client.go`

- [ ] **Step 1: Add token deduplication helper**

```go
// deduplicateTokens removes duplicate tokens, logging a warning for each.
func deduplicateTokens(tokens []string, logger *slog.Logger) []string {
	seen := make(map[string]bool, len(tokens))
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			if len(t) > 20 {
				logger.Warn("duplicate VK token ignored", "token", t[:20]+"...")
			} else {
				logger.Warn("duplicate VK token ignored")
			}
			continue
		}
		seen[t] = true
		result = append(result, t)
	}
	return result
}
```

- [ ] **Step 2: Add `allocateWithToken` helper**

```go
// allocateWithToken fetches TURN credentials using an authenticated token and creates a TURN allocation.
func allocateWithToken(ctx context.Context, svc provider.Service, mgr *turn.Manager, token string) (*turn.Allocation, error) {
	tap, ok := svc.(provider.TokenAuthProvider)
	if !ok {
		return nil, fmt.Errorf("provider does not support token auth")
	}
	info, err := tap.FetchJoinInfoWithToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("fetch join info with token: %w", err)
	}
	alloc, err := mgr.AllocateWithCredentials(ctx, &info.Credentials)
	if err != nil {
		return nil, fmt.Errorf("allocate with credentials: %w", err)
	}
	return alloc, nil
}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/client/...`
Expected: success

- [ ] **Step 4: Commit**

```
feat(client): add token deduplication and allocation helpers
```

---

### Task 7: Client relay — integrate tokens into connectRelaySession

**Files:**
- Modify: `internal/client/client.go`

This is the most complex task. Read the current `connectRelaySession` fully before modifying.

- [ ] **Step 1: Update `connectRelaySession` signature**

Add `vkTokens []string` parameter. Update both call sites (in `RunRelayToRelay` — search for calls to `connectRelaySession` and pass `cfg.VKTokens`).

- [ ] **Step 2: Token-aware JoinInfo acquisition**

At the top of `connectRelaySession`, before signaling setup, replace the `FetchJoinInfo` call:

```go
// If tokens available, try first token for JoinInfo (faster, no rate-limit risk).
// Fall back to anonymous if token fails.
var joinInfo *provider.JoinInfo
if len(vkTokens) > 0 {
	tap, _ := svc.(provider.TokenAuthProvider)
	if tap != nil {
		joinInfo, err = tap.FetchJoinInfoWithToken(ctx, vkTokens[0])
		if err != nil {
			logger.Warn("first token failed for JoinInfo, falling back to anonymous", "err", err)
		}
	}
}
if joinInfo == nil {
	joinInfo, err = svc.FetchJoinInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch join info: %w", err)
	}
}
```

- [ ] **Step 3: Token allocation as first batch (sequential, before anonymous)**

After signaling is established and nonce is generated, but before the anonymous `AllocateGradual` loop:

```go
// Deduplicate and cap tokens
vkTokens = deduplicateTokens(vkTokens, logger)
if len(vkTokens) > numConns {
	vkTokens = vkTokens[:numConns]
}
tokenConns := len(vkTokens)
anonConns := numConns - tokenConns

// Phase 1: Token-based allocations (all in parallel, sent as batch 0)
if tokenConns > 0 {
	tokenAllocs := make([]*turn.Allocation, tokenConns)
	var tokenWg sync.WaitGroup
	var tokenErrs []error
	var tokenMu sync.Mutex
	for i, tok := range vkTokens {
		tokenWg.Add(1)
		go func(idx int, token string) {
			defer tokenWg.Done()
			alloc, err := allocateWithToken(ctx, svc, mgr, token)
			tokenMu.Lock()
			defer tokenMu.Unlock()
			if err != nil {
				logger.Warn("token allocation failed", "index", idx, "err", err)
				tokenErrs = append(tokenErrs, err)
				return
			}
			tokenAllocs[idx] = alloc
		}(i, tok)
	}
	tokenWg.Wait()

	// Collect successful relay addresses
	var tokenAddrs []string
	var successAllocs []*turn.Allocation
	for _, a := range tokenAllocs {
		if a != nil {
			tokenAddrs = append(tokenAddrs, a.RelayAddr.String())
			successAllocs = append(successAllocs, a)
		}
	}

	// Send token batch as batch 0 (if any succeeded)
	if len(tokenAddrs) > 0 {
		final := anonConns == 0
		err = sigClient.SendRelayBatch(ctx, tokenAddrs, role, nonce, 0, final)
		if err != nil {
			return nil, fmt.Errorf("send token relay batch: %w", err)
		}
		// Receive server's matching batch
		serverAddrs, _, _, _, err := sigClient.RecvRelayBatch(ctx, skipRole, nonce)
		if err != nil {
			return nil, fmt.Errorf("recv token relay batch: %w", err)
		}
		// DTLS handshake for each pair (same logic as anonymous batches)
		// ... use existing punch+DTLS pattern with successAllocs and serverAddrs
		// punchIdx starts at 0, increments per pair
	}

	// Adjust anonConns for failed tokens (they fall back to anonymous)
	failedTokens := tokenConns - len(successAllocs)
	anonConns += failedTokens
}

// Phase 2: Anonymous allocations (existing AllocateGradual flow)
// batchIdx starts at 1 (0 was token batch), punchIdx continues from token count
if anonConns > 0 {
	// ... existing AllocateGradual + batch exchange loop
	// Start batchIdx from 1 (or from next available)
}
```

Key design: token batch is sent as **batch 0** sequentially before anonymous batches. This keeps the signaling protocol serial (no concurrent SendRelayBatch/RecvRelayBatch) and reuses the same punchIdx counter space.

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: success

- [ ] **Step 5: Commit**

```
feat(client): integrate token allocations into connectRelaySession
```

---

### Task 8: Server relay — reactive token allocation

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Add `VKTokens` parameter to `acceptOneClient`**

Update `acceptOneClient` signature to accept `vkTokens []string`. Update its call site in `runPersistentRelaySession`.

- [ ] **Step 2: Modify allocation loop to use tokens reactively**

Inside `acceptOneClient`, before the batch receive loop, initialize a token index:

```go
vkTokens = deduplicateTokens(vkTokens, logger)
tokenIdx := 0
```

In the batch receive loop, where the server currently calls `mgr.Allocate(ctx, pairCount)` to create matching allocations for the received client batch, replace with per-connection allocation:

```go
// Allocate matching connections for this batch
var batchAllocs []*turn.Allocation
for i := 0; i < pairCount; i++ {
	var alloc *turn.Allocation
	var allocErr error

	// Try token-based allocation first
	if tokenIdx < len(vkTokens) {
		if tap, ok := svc.(provider.TokenAuthProvider); ok {
			info, err := tap.FetchJoinInfoWithToken(ctx, vkTokens[tokenIdx])
			if err != nil {
				logger.Warn("server token allocation failed, using anonymous",
					"tokenIdx", tokenIdx, "err", err)
			} else {
				alloc, allocErr = mgr.AllocateWithCredentials(ctx, &info.Credentials)
				if allocErr != nil {
					logger.Warn("server token TURN allocation failed, using anonymous",
						"tokenIdx", tokenIdx, "err", allocErr)
					alloc = nil
				}
			}
			tokenIdx++ // consume token regardless of success
		}
	}

	// Fall back to anonymous allocation
	if alloc == nil {
		alloc, allocErr = mgr.AllocateWithCredentials(ctx, /* anonymous creds from FetchCredentials */)
		// OR use the existing anonymous Allocate pattern
	}

	if allocErr != nil {
		logger.Error("allocation failed completely", "index", i, "err", allocErr)
		continue
	}
	batchAllocs = append(batchAllocs, alloc)
}
```

Note: the exact integration depends on how `acceptOneClient` currently calls `mgr.Allocate(ctx, pairCount)`. Read the implementation and adapt — the key change is replacing bulk `Allocate(n)` with per-connection allocation that prefers tokens.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success

- [ ] **Step 4: Commit**

```
feat(server): reactive token allocation in acceptOneClient
```

---

### Task 9: Mobile tunnel — token support

**Files:**
- Modify: `mobile/bind/tunnel.go`

- [ ] **Step 1: Add VKTokens to TunnelConfig**

Find the `TunnelConfig` struct and add:

```go
VKTokens string // comma-separated VK tokens (0-16), passed from Android UI
```

Note: gomobile doesn't support `[]string`, so we accept comma-separated string and split internally. Parse with:

```go
var tokens []string
if cfg.VKTokens != "" {
	tokens = strings.Split(cfg.VKTokens, ",")
}
```

- [ ] **Step 2: Integrate tokens into `connectRelay`**

Same pattern as client: parse `cfg.VKTokens` into `[]string`, deduplicate, cap to `numConns`. Token allocations run in parallel, anonymous via `AllocateGradual`. Follow the same approach as Task 7 but adapted to the mobile tunnel flow.

- [ ] **Step 3: Build for mobile**

Run: `go build ./mobile/bind/...`
Expected: success

- [ ] **Step 4: Commit**

```
feat(mobile): add VKTokens support to TunnelConfig and connectRelay
```

---

### Task 10: Token acquisition — `cmd/token` and `GET_TOKEN.md`

**Files:**
- Create: `cmd/token/main.go`
- Create: `GET_TOKEN.md`

- [ ] **Step 1: Create `GET_TOKEN.md`**

```markdown
# How to Get a VK Token

## Quick Method (Browser DevTools)

1. Open https://vk.com and log in
2. Open DevTools (F12) → Console
3. Run: `copy(document.cookie.match(/remixsid=([^;]+)/)?.[1])`
   — this won't work directly for access_token, use method below instead

### Via OAuth (Recommended)

1. Open this URL in your browser (replace CLIENT_ID if you have your own app):

   ```
   https://oauth.vk.com/authorize?client_id=6287487&display=page&redirect_uri=https://oauth.vk.com/blank.html&scope=offline&response_type=token&v=5.274
   ```

2. Click "Allow" to authorize
3. You'll be redirected to a URL like:
   ```
   https://oauth.vk.com/blank.html#access_token=vk1.a.XXXXX&expires_in=0&user_id=12345
   ```
4. Copy the `access_token` value (starts with `vk1.a.`)

### Using Your Own App ID

1. Go to https://vk.com/editapp?act=create
2. Create a Standalone app
3. Note the App ID
4. Use the URL above but replace `client_id=6287487` with your App ID
5. Run: `callvpn-token --app-id=YOUR_APP_ID`

## Usage

```bash
# Single token
callvpn-client --link=<link> --vk-token='vk1.a.XXXXX'

# Multiple tokens (different accounts)
callvpn-client --link=<link> --vk-token='vk1.a.TOKEN1' --vk-token='vk1.a.TOKEN2'

# Via environment variable
export VK_TOKENS='vk1.a.TOKEN1,vk1.a.TOKEN2'
callvpn-client --link=<link>
```

## Shell Escaping

If using OK auth_tokens (starting with `$`), use single quotes to prevent shell expansion:
```bash
--vk-token='$xCUTeFZFtk...'
```

## Token Lifetime

- **VK access_token** (`vk1.a.*`): permanent with `offline` scope
- **OK auth_token** (`$*`): unknown lifetime, may expire — use VK tokens when possible
```

- [ ] **Step 2: Create `cmd/token/main.go`**

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

const defaultAppID = "6287487"

func main() {
	appID := flag.String("app-id", defaultAppID, "VK application ID")
	flag.Parse()

	oauthURL := fmt.Sprintf(
		"https://oauth.vk.com/authorize?client_id=%s&display=page&redirect_uri=https://oauth.vk.com/blank.html&scope=offline&response_type=token&v=5.274",
		*appID,
	)

	fmt.Println("Opening VK OAuth page in your browser...")
	fmt.Println()
	fmt.Println("After authorizing, copy the access_token from the URL:")
	fmt.Println("  https://oauth.vk.com/blank.html#access_token=vk1.a.XXXXX&...")
	fmt.Println()
	fmt.Println("URL:", oauthURL)
	fmt.Println()

	// Try to open browser
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", oauthURL)
	case "linux":
		cmd = exec.Command("xdg-open", oauthURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", oauthURL)
	}
	if cmd != nil {
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
			fmt.Println("Please open the URL above manually.")
		}
	}
}
```

- [ ] **Step 3: Build**

Run: `go build ./cmd/token/`
Expected: success

- [ ] **Step 4: Commit**

```
feat: add token acquisition tool and GET_TOKEN.md guide
```

---

### Task 12: Build verification and cleanup

**Files:** all modified files

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: success, no errors

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 3: Manual E2E test (if VK token available)**

```bash
# Get a token first
go run ./cmd/token/

# Test with token (client)
go run ./cmd/client/ --link=<link> --vk-token='vk1.a.XXXXX' --n=4

# Test without tokens (regression — should work as before)
go run ./cmd/client/ --link=<link> --n=4
```

- [ ] **Step 4: Update project memory**

Update `memory/project_rate_limiting.md` or create a new memory entry documenting the VK account tokens feature.

- [ ] **Step 5: Final commit if any cleanup needed**

```
chore: cleanup after VK account tokens implementation
```
