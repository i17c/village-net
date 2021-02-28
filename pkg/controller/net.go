package controller

import (
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/node"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
	"net"
	"os"
)

var (
	_, ClusterCIDR, _ = net.ParseCIDR("10.244.0.0/16")
)

func init() {
	cidr := os.Getenv("CLUSTER_CIDR")
	if cidr != "" {
		_, configCIDR, err := net.ParseCIDR(cidr)
		if err == nil {
			ClusterCIDR = configCIDR
			return
		}
		klog.Errorf("read config cidr %s, parse failed: %s", cidr, err.Error())
	}

	ClusterCIDR.Mask.Size()
}

type NodeNetInfo struct {
	Hostname  string `json:"hostname"`
	PublicIP  string `json:"public_ip"`
	CIDR      string `json:"cidr"`
	BridgeIf  string `json:"bridge_if"`
	MacvlanIf string `json:"macvlan_if"`
}

func GetNodeNetInfo() (*NodeNetInfo, error) {
	brPubIp, err := node.BridgePublicIP(func(_ netlink.Addr) bool {
		return true
	})
	if err != nil {
		return nil, err
	}
	return &NodeNetInfo{
		Hostname:  node.HostName,
		PublicIP:  brPubIp.String(),
		CIDR:      "",
		BridgeIf:  node.BridgeParentInterface,
		MacvlanIf: node.MacVlanParentInterface,
	}, nil
}

type HostGw struct {
	hostname string
	link     *netlink.Link
}

func (h *HostGw) SyncNodeRoute(info *NodeNetInfo) error {
	if h.hostname == info.Hostname {
		return nil
	}
	_, cidr, err := net.ParseCIDR(info.CIDR)
	if err != nil {
		return err
	}

	return ip.AddRoute(cidr, net.ParseIP(info.PublicIP), *h.link)
}

func NewHostGW() (*HostGw, error) {
	link, err := netlink.LinkByName(node.BridgeParentInterface)
	if err != nil {
		return nil, err
	}
	return &HostGw{
		hostname: node.HostName,
		link:     &link,
	}, nil
}
