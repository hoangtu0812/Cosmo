package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// A tool call is a request this server makes to an address a user chose. That
// is the shape of a server-side request forgery, so the destination is checked
// twice: once when the tool is saved, and again at the moment the socket is
// opened. The second check is the one that matters - a name that resolved to a
// public address at save time can resolve to 169.254.169.254 later.
//
// Redirects go through the same dialer, so a public URL cannot bounce the
// request into the private network either.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT and the IPv4 broadcast address are neither private nor
	// loopback by Go's definition but are not somewhere a tool should reach.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		if v4.Equal(net.IPv4bcast) {
			return true
		}
	}
	return false
}

// EgressPolicy decides where a tool may reach. The zero value denies every
// private address, which is what a hosted deployment wants.
//
// An on-premises deployment is the reason this is not a constant: its APIs are
// on the internal network by definition, so a blanket refusal would make tools
// useless there. An operator names the hosts that are allowed; naming none
// leaves the default in place.
type EgressPolicy struct {
	// Hosts that may be reached even though they resolve privately. Matched on
	// the hostname as written, so "10.0.0.5" and "erp.internal" are both valid
	// entries and neither opens anything else.
	AllowedHosts []string
}

func (policy EgressPolicy) allows(host string) bool {
	for _, allowed := range policy.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return true
		}
	}
	return false
}

// CheckEgress refuses a destination before it is stored, so a mistake is
// reported while the reader is still looking at the field they typed it into.
func (policy EgressPolicy) CheckEgress(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ErrBaseURL
	}
	host := parsed.Hostname()
	if host == "" {
		return ErrBaseURL
	}
	if policy.allows(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return ErrPrivateAddress
		}
		return nil
	}
	addresses, err := net.LookupIP(host)
	if err != nil {
		// A name we cannot resolve is not proof of anything; the dial-time
		// check will catch it if it ever resolves somewhere it should not.
		return nil
	}
	for _, ip := range addresses {
		if isBlockedIP(ip) {
			return ErrPrivateAddress
		}
	}
	return nil
}

// guardedClient dials normally but refuses the connection if the address the
// name resolved to is one this policy does not allow. The check happens here,
// at the moment the socket opens, because that is the only point a name cannot
// have changed underneath us since.
func (policy EgressPolicy) guardedClient(allowedIPs map[string]bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrPrivateAddress
			}
			// An allowed host is allowed on any port: the operator named a
			// machine, and its ports are the same machine.
			if allowedIPs[host] {
				return nil
			}
			if isBlockedIP(net.ParseIP(host)) {
				return ErrPrivateAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   CallTimeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// Invoke calls one action with the arguments a model or a reader supplied and
// returns what came back. The response is bounded: an endpoint that streams
// forever must not be able to exhaust this process or the model's context.
func (repository *Repository) Invoke(ctx context.Context, tool Tool, action Action, arguments map[string]any) (CallResult, error) {
	// A built-in does the work here and an MCP server is asked in its own
	// protocol; everything below builds a plain HTTP request instead.
	switch tool.Kind {
	case KindBuiltin:
		return repository.invokeBuiltin(ctx, action, arguments)
	case KindMCP:
		return repository.invokeMCP(ctx, tool, action, arguments)
	}

	target, body, err := buildRequest(repository.egress, tool, action, arguments)
	if err != nil {
		return CallResult{}, err
	}

	request, err := http.NewRequestWithContext(ctx, action.Method, target, body.reader)
	if err != nil {
		return CallResult{}, ErrCallFailed
	}
	if body.isJSON {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.8")

	if err := repository.authorise(ctx, tool, request); err != nil {
		return CallResult{}, err
	}

	started := time.Now()
	response, err := repository.client().Do(request)
	if err != nil {
		return CallResult{}, ErrCallFailed
	}
	defer response.Body.Close()

	// One byte past the cap tells us the body was cut rather than merely
	// happening to end exactly at the limit.
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return CallResult{}, ErrCallFailed
	}
	truncated := len(raw) > MaxResponseBytes
	if truncated {
		raw = raw[:MaxResponseBytes]
	}
	return CallResult{
		Status:      response.StatusCode,
		DurationMS:  time.Since(started).Milliseconds(),
		Body:        string(raw),
		IsTruncated: truncated,
	}, nil
}

type requestBody struct {
	reader io.Reader
	isJSON bool
}

// buildRequest places each argument where its parameter says it goes. Path
// parameters are substituted into the path, query parameters are encoded, and
// the rest become a JSON body - but only for verbs that carry one.
func buildRequest(policy EgressPolicy, tool Tool, action Action, arguments map[string]any) (string, requestBody, error) {
	path := action.Path
	query := url.Values{}
	payload := map[string]any{}

	for _, parameter := range action.Parameters {
		// A fixed parameter is the tool's own value and is not offered to the
		// model, so a call that names it anyway does not get to override it.
		var value any
		if parameter.IsFixed() {
			value = parameter.Value
		} else {
			supplied, ok := arguments[parameter.Name]
			if !ok || supplied == nil {
				continue
			}
			value = supplied
		}
		switch parameter.In {
		case "path":
			// Escaped, so a value cannot add path segments of its own.
			path = strings.ReplaceAll(path, "{"+parameter.Name+"}", url.PathEscape(asText(value)))
		case "body":
			payload[parameter.Name] = value
		default:
			query.Set(parameter.Name, asText(value))
		}
	}

	target := tool.BaseURL + path
	if len(query) > 0 {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + query.Encode()
	}
	// The base URL was checked when it was stored, but the path is joined
	// here, so the result is checked again before it is dialled.
	if err := policy.CheckEgress(target); err != nil {
		return "", requestBody{}, err
	}

	if len(payload) == 0 || action.Method == "GET" || action.Method == "DELETE" {
		return target, requestBody{}, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", requestBody{}, ErrCallFailed
	}
	return target, requestBody{reader: bytes.NewReader(encoded), isJSON: true}, nil
}

// client is the one place a request leaves this process, so the allowlist is
// resolved here rather than at each call site.
func (repository *Repository) client() *http.Client {
	return repository.egress.guardedClient(repository.allowedIPs())
}

// allowedIPs resolves the named hosts to the addresses the dialler will see.
// Resolving here rather than trusting the name means an allowed entry opens
// only the machine it actually points at.
func (repository *Repository) allowedIPs() map[string]bool {
	allowed := map[string]bool{}
	for _, host := range repository.egress.AllowedHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			allowed[ip.String()] = true
			continue
		}
		addresses, err := net.LookupIP(host)
		if err != nil {
			continue
		}
		for _, ip := range addresses {
			allowed[ip.String()] = true
		}
	}
	return allowed
}

func asText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%v", typed), ".0")
	}
}
