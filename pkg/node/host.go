package node

import (
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

	bridgeIf := os.Getenv("BridgeParentInterface")
	if bridgeIf != "" {
		BridgeParentInterface = bridgeIf
	}

	mvIf := os.Getenv("MacVlanParentInterface")
	if mvIf != "" {
		MacVlanParentInterface = mvIf
	}
}
