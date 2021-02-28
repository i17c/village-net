package node

import (
	"fmt"
	"github.com/vishvananda/netlink"
	"net"
	"os"
)

var (
	HostName               = ""
	BridgeParentInterface  = "eth0"
	MacVlanParentInterface = "eth0"
)

func init() {
	if HostName == "" {
		HostName, _ = os.Hostname()
	}

	bridgeIf := os.Getenv("BRIDGE_PARENT_INTERFACE")
	if bridgeIf != "" {
		BridgeParentInterface = bridgeIf
	}

	mvIf := os.Getenv("MACVLAN_PARENT_INTERFACE")
	if mvIf != "" {
		MacVlanParentInterface = mvIf
	}
}

type DiscoverFn func(addr netlink.Addr) bool

func BridgePublicIP(fn DiscoverFn) (*net.IP, error) {
	link, err := netlink.LinkByName(BridgeParentInterface)
	if err != nil {
		return nil, fmt.Errorf("query interface failed: %s", err.Error())
	}

	// TODO: support v6
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("get first addr failed: %s", err.Error())
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addr got")
	}

	for _, addr := range addrs {
		if fn(addr) {
			ipInfo := addr.IP
			return &ipInfo, nil
		}
	}

	return nil, fmt.Errorf("no addr found")
}
