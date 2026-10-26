//go:build !darwin

package main

import "net"

func agentPeerCredentials(_ *net.UnixConn) (AgentPeerCredentials, error) {
	return AgentPeerCredentials{Available: false}, nil
}
