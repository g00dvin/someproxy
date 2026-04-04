package bind

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/call-vpn/call-vpn/internal/captcha"
	"github.com/call-vpn/call-vpn/internal/provider"
)

// CaptchaCallback is implemented by the mobile app (Java/Kotlin) to show
// a WebView with the VK captcha and return the success_token.
type CaptchaCallback interface {
	// ShowCaptcha opens a WebView with redirectURI and blocks until the user
	// solves the captcha. Returns the success_token or "" on failure/cancel.
	ShowCaptcha(redirectURI string) string
}

// callbackSolver adapts a CaptchaCallback into provider.CaptchaSolver.
// It first attempts automatic solving via DirectSolver, falling back to
// the mobile WebView callback if that fails.
type callbackSolver struct {
	cb     CaptchaCallback
	direct *captcha.DirectSolver
}

func (s *callbackSolver) SolveCaptcha(ctx context.Context, ch *provider.CaptchaChallenge) (*provider.CaptchaResult, error) {
	// Try automatic solving first (no user interaction needed).
	if ch.RedirectURI != "" {
		slog.Info("captcha: trying automatic solver first")
		result, err := s.direct.SolveCaptcha(ctx, ch)
		if err == nil {
			slog.Info("captcha: automatic solver succeeded")
			return result, nil
		}
		slog.Warn("captcha: automatic solver failed, falling back to WebView", "err", err)
	}

	// Fall back to interactive WebView.
	uri := ch.RedirectURI
	if uri == "" {
		// No redirect_uri — classic captcha, WebView won't help either.
		return nil, fmt.Errorf("captcha has no redirect_uri, cannot solve")
	}
	token := s.cb.ShowCaptcha(uri)
	if token == "" {
		return nil, fmt.Errorf("captcha cancelled or failed")
	}
	return &provider.CaptchaResult{SuccessToken: token}, nil
}
