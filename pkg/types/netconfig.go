package types

type NetConf struct {
	Name       string      `json:"name"`
	CNIVersion string      `json:"cniVersion"`
	IPAM       *IPAMConfig `json:"ipam"`
	Kubernetes Kubernetes  `json:"kubernetes"`

	// macvlan config
	Master string `json:"master"`
	Mode   string `json:"mode"`
	MTU    int    `json:"mtu"`
	Mac    string `json:"mac,omitempty"`

	RuntimeConfig struct {
		IPs []string `json:"ips,omitempty"`
		Mac string   `json:"mac,omitempty"`
	} `json:"runtimeConfig,omitempty"`
	//Args *struct {
	//	A *IPAMArgs `json:"cni"`
	//} `json:"args"`
}

type IPAMConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
