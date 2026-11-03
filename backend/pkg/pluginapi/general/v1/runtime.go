// Package generalv1 is the versioned general-plugin wire API, independent of
// both the legacy transport API and host-only domain contracts.
package generalv1

import (
	"context"
	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

const (
	ProtocolVersion = 1
	APIVersion      = 1
	PluginName      = "general_plugin"
	MaxMessageBytes = 2 * 1024 * 1024
)

// HandshakeConfig distinguishes general processes from transport-v1 processes.
var HandshakeConfig = hcplugin.HandshakeConfig{ProtocolVersion: ProtocolVersion, MagicCookieKey: "SUB2API_GENERAL_PLUGIN_COOKIE", MagicCookieValue: "sub2api-general-v1"}

type GRPCPlugin struct {
	hcplugin.NetRPCUnsupportedPlugin
	Impl GeneralPluginServer
}

func (p *GRPCPlugin) GRPCServer(_ *hcplugin.GRPCBroker, server *grpc.Server) error {
	RegisterGeneralPluginServer(server, p.Impl)
	return nil
}
func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *hcplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return NewGeneralPluginClient(conn), nil
}
func ClientPluginMap() map[string]hcplugin.Plugin {
	return map[string]hcplugin.Plugin{PluginName: &GRPCPlugin{}}
}
func Serve(impl GeneralPluginServer) {
	hcplugin.Serve(&hcplugin.ServeConfig{HandshakeConfig: HandshakeConfig, Plugins: map[string]hcplugin.Plugin{PluginName: &GRPCPlugin{Impl: impl}}, GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
		opts = append(opts, grpc.MaxRecvMsgSize(MaxMessageBytes), grpc.MaxSendMsgSize(MaxMessageBytes))
		return grpc.NewServer(opts...)
	}})
}
