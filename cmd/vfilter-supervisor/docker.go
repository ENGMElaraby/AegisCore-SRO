package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// dockerClient is the minimal Docker Engine API client the supervisor needs: inspect, start, stop, restart.
type dockerClient struct {
	http *http.Client
	base string
}

const (
	dockerAPIVersion = "v1.41"
	stopGraceSeconds = 30
)

func newDockerClient(socket string) *dockerClient {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &dockerClient{http: &http.Client{Transport: transport, Timeout: (stopGraceSeconds + 15) * time.Second}, base: "http://docker/" + dockerAPIVersion}
}

type containerState struct {
	Running   bool   `json:"Running"`
	Status    string `json:"Status"`
	ExitCode  int    `json:"ExitCode"`
	OOMKilled bool   `json:"OOMKilled"`
	Error     string `json:"Error"`
}

func (d *dockerClient) inspect(ctx context.Context, container string) (containerState, error) {
	var body struct {
		State containerState `json:"State"`
	}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json")
	if err != nil {
		return body.State, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return body.State, fmt.Errorf("inspect %s: %s", container, resp.Status)
	}
	err = json.NewDecoder(resp.Body).Decode(&body)
	return body.State, err
}

// action runs start, stop or restart. Docker answers 304 when the container is already in that state.
func (d *dockerClient) action(ctx context.Context, container, verb string) (changed bool, err error) {
	path := "/containers/" + url.PathEscape(container) + "/" + verb
	if verb != "start" {
		path += fmt.Sprintf("?t=%d", stopGraceSeconds)
	}
	resp, err := d.do(ctx, http.MethodPost, path)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotModified:
		return false, nil
	default:
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("%s %s: %s %s", verb, container, resp.Status, detail)
	}
}

func (d *dockerClient) do(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, nil)
	if err != nil {
		return nil, err
	}
	return d.http.Do(req)
}
