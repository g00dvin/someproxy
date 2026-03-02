package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Connect sends a signaling request to the server and returns the response
// containing the server's TURN relay addresses. The server will create its
// own TURN allocations and establish permissions for relay-to-relay communication.
func Connect(ctx context.Context, signalURL string, req ConnectRequest) (*ConnectResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, signalURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("signal request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("signal server: %s", errResp.Error)
		}
		return nil, fmt.Errorf("signal server returned %d", resp.StatusCode)
	}

	var connectResp ConnectResponse
	if err := json.Unmarshal(respBody, &connectResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &connectResp, nil
}
