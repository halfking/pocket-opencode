package adapter

import "context"

type StaticNPSAdapter struct{}

func NewStaticNPSAdapter() *StaticNPSAdapter {
	return &StaticNPSAdapter{}
}

func (a *StaticNPSAdapter) ListClients(ctx context.Context) ([]NPSClient, error) {
	return []NPSClient{
		{ID: 1, Name: "demo-main"},
	}, nil
}

func (a *StaticNPSAdapter) ListTunnels(ctx context.Context) ([]NPSTunnel, error) {
	return []NPSTunnel{
		{ID: 1, ClientID: 1, Type: "http", Remark: "demo", Host: "demo.local", Target: "127.0.0.1:8080"},
	}, nil
}
