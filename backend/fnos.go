package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

const trimSocket = "/var/run/trim_open_gateway_apiscope.socket"

type FnosClient struct {
	appName, token string
	client         *http.Client
}

func NewFnosClient(env *RuntimeEnv) *FnosClient {
	return &FnosClient{
		appName: env.TRIMAppName, token: env.TRIMApiToken,
		client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", trimSocket)
			},
		}},
	}
}

// The user id comes from the authenticated Unix gateway connection.
func (c *FnosClient) GetUserAccessibleFolders(ctx context.Context, uid int) ([]string, error) {
	payload, err := json.Marshal(map[string]any{
		"reqId": fmt.Sprintf("c%d", time.Now().UnixNano()), "req": "trim.file.getUserAccessibleFolders",
		"appName": c.appName, "data": map[string]int{"uid": uid},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/v1/trimapp", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fnOS directory request: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Paths []string `json:"paths"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code == nil {
		return nil, fmt.Errorf("fnOS directory response is missing its status code")
	}
	if *result.Code != 0 {
		return nil, fmt.Errorf("fnOS (%d): %s", *result.Code, result.Msg)
	}
	return result.Data.Paths, nil
}
