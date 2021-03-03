package types

import (
	"github.com/containernetworking/cni/pkg/types"
)

type NetConf struct {
	types.NetConf

	Name       string      `json:"name"`
	CNIVersion string      `json:"cniVersion"`
	IPAM       *IPAMConfig `json:"ipam"`
	Cluster    Cluster     `json:"cluster"`

	// macvlan config
	MacVlan *MacVlanConf `json:"macVlan"`

	// bridge
	BrName       string `json:"bridge,omitempty"`
	IsGW         bool   `json:"isGateway,omitempty"`
	IsDefaultGW  bool   `json:"isDefaultGateway,omitempty"`
	ForceAddress bool   `json:"forceAddress,omitempty"`
	MTU          int    `json:"mtu"`
	IPMasq       bool   `json:"ipMasq,omitempty"`
	HairpinMode  bool   `json:"hairpinMode,omitempty"`
	PromiscMode  bool   `json:"promiscMode,omitempty"`
	Vlan         int    `json:"vlan,omitempty"`

	RuntimeConfig struct {
		IPs []string `json:"ips,omitempty"`
		Mac string   `json:"mac,omitempty"`
	} `json:"runtimeConfig,omitempty"`
	//Args *struct {
	//	A *IPAMArgs `json:"cni"`
	//} `json:"args"`
}

type MacVlanConf struct {
	Master string `json:"master,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Mac    string `json:"mac,omitempty"`
	MTU    int    `json:"mtu"`
}

type IPAMConfig struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	SocketPath string `json:"socket_path"`
}
