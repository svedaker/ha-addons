package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// UbusClient handles JSON-RPC communication with an OpenWrt ubus HTTP endpoint.
type UbusClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	session    string
	mu         sync.Mutex
	rpcID      int
}

// UbusRequest is the JSON-RPC request envelope.
type UbusRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

// UbusResponse is the JSON-RPC response envelope.
type UbusResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *UbusError      `json:"error,omitempty"`
}

// UbusError represents a JSON-RPC error.
type UbusError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	ubusAccessDenied = 6
	sessionExpired   = "00000000000000000000000000000000"
)

// NewUbusClient creates a new ubus JSON-RPC client.
func NewUbusClient(host, scheme, username, password string) *UbusClient {
	return &UbusClient{
		baseURL:  fmt.Sprintf("%s://%s/ubus", scheme, host),
		username: username,
		password: password,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // Self-signed certs on LAN
				},
			},
			Timeout: 10 * time.Second,
		},
		session: sessionExpired,
	}
}

// call makes a raw JSON-RPC call and returns the parsed response.
func (u *UbusClient) call(method string, params []interface{}) (*UbusResponse, error) {
	u.rpcID++
	req := UbusRequest{
		JSONRPC: "2.0",
		ID:      u.rpcID,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := u.httpClient.Post(u.baseURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http post %s: %w", u.baseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var ubusResp UbusResponse
	if err := json.Unmarshal(data, &ubusResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &ubusResp, nil
}

// Login authenticates and stores the session token.
func (u *UbusClient) Login() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.login()
}

func (u *UbusClient) login() error {
	params := []interface{}{
		sessionExpired,
		"session",
		"login",
		map[string]string{
			"username": u.username,
			"password": u.password,
		},
	}

	resp, err := u.call("call", params)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	// Result is [status_code, {ubus_rpc_session: "..."}]
	var result []json.RawMessage
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse login result: %w", err)
	}

	if len(result) < 2 {
		return fmt.Errorf("login: unexpected result length %d", len(result))
	}

	// Check status code
	var statusCode int
	if err := json.Unmarshal(result[0], &statusCode); err != nil {
		return fmt.Errorf("parse login status: %w", err)
	}
	if statusCode != 0 {
		return fmt.Errorf("login failed: ubus status %d", statusCode)
	}

	// Extract session token
	var sessionData struct {
		Token string `json:"ubus_rpc_session"`
	}
	if err := json.Unmarshal(result[1], &sessionData); err != nil {
		return fmt.Errorf("parse session token: %w", err)
	}

	u.session = sessionData.Token
	return nil
}

// Call makes an authenticated ubus call. Auto-relogins on access denied.
func (u *UbusClient) Call(object, method string, args map[string]interface{}) (json.RawMessage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Ensure we have a session
	if u.session == sessionExpired {
		if err := u.login(); err != nil {
			return nil, err
		}
	}

	result, err := u.callWithSession(object, method, args)
	if err != nil {
		return nil, err
	}

	// Check for access denied → re-login once
	statusCode, data, err := parseCallResult(result)
	if err != nil {
		return nil, err
	}
	if statusCode == ubusAccessDenied {
		if err := u.login(); err != nil {
			return nil, fmt.Errorf("re-login after access denied: %w", err)
		}
		result, err = u.callWithSession(object, method, args)
		if err != nil {
			return nil, err
		}
		statusCode, data, err = parseCallResult(result)
		if err != nil {
			return nil, err
		}
		if statusCode != 0 {
			return nil, fmt.Errorf("ubus %s.%s failed after re-login: status %d", object, method, statusCode)
		}
	} else if statusCode != 0 {
		return nil, fmt.Errorf("ubus %s.%s: status %d", object, method, statusCode)
	}

	return data, nil
}

func (u *UbusClient) callWithSession(object, method string, args map[string]interface{}) (json.RawMessage, error) {
	if args == nil {
		args = map[string]interface{}{}
	}

	params := []interface{}{
		u.session,
		object,
		method,
		args,
	}

	resp, err := u.call("call", params)
	if err != nil {
		return nil, err
	}

	return resp.Result, nil
}

// parseCallResult extracts [status_code, data] from a ubus call result.
func parseCallResult(raw json.RawMessage) (int, json.RawMessage, error) {
	var result []json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return -1, nil, fmt.Errorf("parse call result: %w", err)
	}

	if len(result) == 0 {
		return -1, nil, fmt.Errorf("empty call result")
	}

	var statusCode int
	if err := json.Unmarshal(result[0], &statusCode); err != nil {
		return -1, nil, fmt.Errorf("parse status code: %w", err)
	}

	var data json.RawMessage
	if len(result) > 1 {
		data = result[1]
	}

	return statusCode, data, nil
}
