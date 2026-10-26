//go:build darwin

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

func agentPeerCredentials(conn *net.UnixConn) (AgentPeerCredentials, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return AgentPeerCredentials{}, err
	}
	var (
		credentials AgentPeerCredentials
		socketErr   error
	)
	if err := rawConn.Control(func(fd uintptr) {
		peer, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			socketErr = err
			return
		}
		credentials.UID = peer.Uid
		if peer.Ngroups > 0 {
			credentials.GID = peer.Groups[0]
		}
		credentials.Available = true
	}); err != nil {
		return AgentPeerCredentials{}, err
	}
	if socketErr != nil {
		return AgentPeerCredentials{}, socketErr
	}
	return credentials, nil
}
