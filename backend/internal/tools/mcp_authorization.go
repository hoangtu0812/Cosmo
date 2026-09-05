package tools

import (
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"net/http"
	"strings"
)

// No upstream error_description or body is propagated to the UI/model.
type MCPAuthorizationError struct {
	Status         int
	Code           string
	RequiredScopes []string
}

func (err *MCPAuthorizationError) Error() string {
	if len(err.RequiredScopes) > 0 {
		return "MCP yêu cầu quyền bổ sung: " + strings.Join(err.RequiredScopes, " ") + ". Kiểm tra Scope trong cấu hình tool và kết nối lại tài khoản."
	}
	return ErrToolUnauthorized.Error()
}
func (err *MCPAuthorizationError) Unwrap() error { return ErrToolUnauthorized }

func boundedOAuthScopes(raw string) []string {
	if len(raw) > 2048 {
		return nil
	}
	scopes := strings.Fields(raw)
	if len(scopes) > 32 {
		return nil
	}
	for _, scope := range scopes {
		for _, r := range scope {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return nil
			}
		}
	}
	return scopes
}

func mcpAuthorizationChallenge(response *http.Response) *MCPAuthorizationError {
	result := &MCPAuthorizationError{Status: response.StatusCode}
	challenges, _ := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
	for _, challenge := range challenges {
		if !strings.EqualFold(challenge.Scheme, "bearer") {
			continue
		}
		switch challenge.Params["error"] {
		case "invalid_token", "insufficient_scope":
			result.Code = challenge.Params["error"]
		}
		for _, scope := range boundedOAuthScopes(challenge.Params["scope"]) {
			result.RequiredScopes = appendUnique(result.RequiredScopes, scope)
		}
	}
	return result
}
