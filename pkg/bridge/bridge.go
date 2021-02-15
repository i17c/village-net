// Copyright 2014 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"encoding/json"
	"fmt"
	commontype "github.com/containernetworking/plugins/pkg/types"
	"github.com/vishvananda/netlink"
	"io/ioutil"
	"net"
	"syscall"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
)

// For testcases to force an error after IPAM has been performed
var debugPostIPAMError error

const defaultBrName = "cni0"

type NetConf struct {
	types.NetConf
	BrName       string `json:"bridge"`
	IsGW         bool   `json:"isGateway"`
	IsDefaultGW  bool   `json:"isDefaultGateway"`
	ForceAddress bool   `json:"forceAddress"`
	IPMasq       bool   `json:"ipMasq"`
	MTU          int    `json:"mtu"`
	HairpinMode  bool   `json:"hairpinMode"`
	PromiscMode  bool   `json:"promiscMode"`
	Vlan         int    `json:"vlan"`
}

type gwInfo struct {
	gws               []net.IPNet
	family            int
	defaultRouteFound bool
}

func loadNetConf(bytes []byte) (*NetConf, string, error) {
	n := &NetConf{
		BrName: defaultBrName,
	}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, "", fmt.Errorf("failed to load netconf: %v", err)
	}
	if n.Vlan < 0 || n.Vlan > 4094 {
		return nil, "", fmt.Errorf("invalid VLAN ID %d (must be between 0 and 4094)", n.Vlan)
	}
	return n, n.CNIVersion, nil
}

// calcGateways processes the results from the IPAM plugin and does the
// following for each IP family:
//    - Calculates and compiles a list of gateway addresses
//    - Adds a default route if needed
func calcGateways(result *current.Result, n *commontype.NetConf) (*gwInfo, *gwInfo, error) {

	gwsV4 := &gwInfo{}
	gwsV6 := &gwInfo{}

	for _, ipc := range result.IPs {

		// Determine if this config is IPv4 or IPv6
		var gws *gwInfo
		defaultNet := &net.IPNet{}
		switch {
		case ipc.Address.IP.To4() != nil:
			gws = gwsV4
			gws.family = netlink.FAMILY_V4
			defaultNet.IP = net.IPv4zero
		case len(ipc.Address.IP) == net.IPv6len:
			gws = gwsV6
			gws.family = netlink.FAMILY_V6
			defaultNet.IP = net.IPv6zero
		default:
			return nil, nil, fmt.Errorf("Unknown IP object: %v", ipc)
		}
		defaultNet.Mask = net.IPMask(defaultNet.IP)

		// All IPs currently refer to the container interface
		ipc.Interface = current.Int(2)

		// If not provided, calculate the gateway address corresponding
		// to the selected IP address
		if ipc.Gateway == nil && n.IsGW {
			ipc.Gateway = calcGatewayIP(&ipc.Address)
		}

		// Add a default route for this family using the current
		// gateway address if necessary.
		if n.IsDefaultGW && !gws.defaultRouteFound {
			for _, route := range result.Routes {
				if route.GW != nil && defaultNet.String() == route.Dst.String() {
					gws.defaultRouteFound = true
					break
				}
			}
			if !gws.defaultRouteFound {
				result.Routes = append(
					result.Routes,
					&types.Route{Dst: *defaultNet, GW: ipc.Gateway},
				)
				gws.defaultRouteFound = true
			}
		}

		// Append this gateway address to the list of gateways
		if n.IsGW {
			gw := net.IPNet{
				IP:   ipc.Gateway,
				Mask: ipc.Address.Mask,
			}
			gws.gws = append(gws.gws, gw)
		}
	}
	return gwsV4, gwsV6, nil
}

func ensureAddr(br netlink.Link, family int, ipn *net.IPNet, forceAddress bool) error {
	addrs, err := netlink.AddrList(br, family)
	if err != nil && err != syscall.ENOENT {
		return fmt.Errorf("could not get list of IP addresses: %v", err)
	}

	ipnStr := ipn.String()
	for _, a := range addrs {

		// string comp is actually easiest for doing IPNet comps
		if a.IPNet.String() == ipnStr {
			return nil
		}

		// Multiple IPv6 addresses are allowed on the bridge if the
		// corresponding subnets do not overlap. For IPv4 or for
		// overlapping IPv6 subnets, reconfigure the IP address if
		// forceAddress is true, otherwise throw an error.
		if family == netlink.FAMILY_V4 || a.IPNet.Contains(ipn.IP) || ipn.Contains(a.IPNet.IP) {
			if forceAddress {
				if err = deleteAddr(br, a.IPNet); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%q already has an IP address different from %v", br.Attrs().Name, ipnStr)
			}
		}
	}

	addr := &netlink.Addr{IPNet: ipn, Label: ""}
	if err := netlink.AddrAdd(br, addr); err != nil && err != syscall.EEXIST {
		return fmt.Errorf("could not add IP address to %q: %v", br.Attrs().Name, err)
	}

	// Set the bridge's MAC to itself. Otherwise, the bridge will take the
	// lowest-numbered mac on the bridge, and will change as ifs churn
	if err := netlink.LinkSetHardwareAddr(br, br.Attrs().HardwareAddr); err != nil {
		return fmt.Errorf("could not set bridge's mac: %v", err)
	}

	return nil
}

func deleteAddr(br netlink.Link, ipn *net.IPNet) error {
	addr := &netlink.Addr{IPNet: ipn, Label: ""}

	if err := netlink.AddrDel(br, addr); err != nil {
		return fmt.Errorf("could not remove IP address from %q: %v", br.Attrs().Name, err)
	}

	return nil
}

func bridgeByName(name string) (*netlink.Bridge, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("could not lookup %q: %v", name, err)
	}
	br, ok := l.(*netlink.Bridge)
	if !ok {
		return nil, fmt.Errorf("%q already exists but is not a bridge", name)
	}
	return br, nil
}

func ensureBridge(brName string, mtu int, promiscMode, vlanFiltering bool) (*netlink.Bridge, error) {
	br := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: brName,
			MTU:  mtu,
			// Let kernel use default txqueuelen; leaving it unset
			// means 0, and a zero-length TX queue messes up FIFO
			// traffic shapers which use TX queue length as the
			// default packet limit
			TxQLen: -1,
		},
	}
	if vlanFiltering {
		br.VlanFiltering = &vlanFiltering
	}

	err := netlink.LinkAdd(br)
	if err != nil && err != syscall.EEXIST {
		return nil, fmt.Errorf("could not add %q: %v", brName, err)
	}

	if promiscMode {
		if err := netlink.SetPromiscOn(br); err != nil {
			return nil, fmt.Errorf("could not set promiscuous mode on %q: %v", brName, err)
		}
	}

	// Re-fetch link to read all attributes and if it already existed,
	// ensure it's really a bridge with similar configuration
	br, err = bridgeByName(brName)
	if err != nil {
		return nil, err
	}

	// we want to own the routes for this interface
	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", brName), "0")

	if err := netlink.LinkSetUp(br); err != nil {
		return nil, err
	}

	return br, nil
}

func ensureVlanInterface(br *netlink.Bridge, vlanId int) (netlink.Link, error) {
	name := fmt.Sprintf("%s.%d", br.Name, vlanId)

	brGatewayVeth, err := netlink.LinkByName(name)
	if err != nil {
		if err.Error() != "Link not found" {
			return nil, fmt.Errorf("failed to find interface %q: %v", name, err)
		}

		hostNS, err := ns.GetCurrentNS()
		if err != nil {
			return nil, fmt.Errorf("faild to find host namespace: %v", err)
		}

		_, brGatewayIface, err := setupVeth(hostNS, br, name, br.MTU, false, vlanId)
		if err != nil {
			return nil, fmt.Errorf("faild to create vlan gateway %q: %v", name, err)
		}

		brGatewayVeth, err = netlink.LinkByName(brGatewayIface.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup %q: %v", brGatewayIface.Name, err)
		}
	}

	return brGatewayVeth, nil
}

func setupVeth(netns ns.NetNS, br *netlink.Bridge, ifName string, mtu int, hairpinMode bool, vlanID int) (*current.Interface, *current.Interface, error) {
	contIface := &current.Interface{}
	hostIface := &current.Interface{}

	err := netns.Do(func(hostNS ns.NetNS) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, containerVeth, err := ip.SetupVeth(ifName, mtu, hostNS)
		if err != nil {
			return err
		}
		contIface.Name = containerVeth.Name
		contIface.Mac = containerVeth.HardwareAddr.String()
		contIface.Sandbox = netns.Path()
		hostIface.Name = hostVeth.Name
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostIface.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup %q: %v", hostIface.Name, err)
	}
	hostIface.Mac = hostVeth.Attrs().HardwareAddr.String()

	// connect host veth end to the bridge
	if err := netlink.LinkSetMaster(hostVeth, br); err != nil {
		return nil, nil, fmt.Errorf("failed to connect %q to bridge %v: %v", hostVeth.Attrs().Name, br.Attrs().Name, err)
	}

	// set hairpin mode
	if err = netlink.LinkSetHairpin(hostVeth, hairpinMode); err != nil {
		return nil, nil, fmt.Errorf("failed to setup hairpin mode for %v: %v", hostVeth.Attrs().Name, err)
	}

	if vlanID != 0 {
		err = netlink.BridgeVlanAdd(hostVeth, uint16(vlanID), true, true, false, true)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to setup vlan tag on interface %q: %v", hostIface.Name, err)
		}
	}

	return hostIface, contIface, nil
}

func calcGatewayIP(ipn *net.IPNet) net.IP {
	nid := ipn.IP.Mask(ipn.Mask)
	return ip.NextIP(nid)
}

func setupBridge(n *commontype.NetConf) (*netlink.Bridge, *current.Interface, error) {
	vlanFiltering := false
	if n.Vlan != 0 {
		vlanFiltering = true
	}
	// create bridge if necessary
	br, err := ensureBridge(n.BrName, n.MTU, n.PromiscMode, vlanFiltering)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create bridge %q: %v", n.BrName, err)
	}

	return br, &current.Interface{
		Name: br.Attrs().Name,
		Mac:  br.Attrs().HardwareAddr.String(),
	}, nil
}

// disableIPV6DAD disables IPv6 Duplicate Address Detection (DAD)
// for an interface, if the interface does not support enhanced_dad.
// We do this because interfaces with hairpin mode will see their own DAD packets
func disableIPV6DAD(ifName string) error {
	// ehanced_dad sends a nonce with the DAD packets, so that we can safely
	// ignore ourselves
	enh, err := ioutil.ReadFile(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/enhanced_dad", ifName))
	if err == nil && string(enh) == "1\n" {
		return nil
	}
	f := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_dad", ifName)
	return ioutil.WriteFile(f, []byte("0"), 0644)
}

func enableIPForward(family int) error {
	if family == netlink.FAMILY_V4 {
		return ip.EnableIP4Forward()
	}
	return ip.EnableIP6Forward()
}

type cniBridgeIf struct {
	Name        string
	ifIndex     int
	peerIndex   int
	masterIndex int
	found       bool
}

func validateInterface(intf current.Interface, expectInSb bool) (cniBridgeIf, netlink.Link, error) {

	ifFound := cniBridgeIf{found: false}
	if intf.Name == "" {
		return ifFound, nil, fmt.Errorf("Interface name missing ")
	}

	link, err := netlink.LinkByName(intf.Name)
	if err != nil {
		return ifFound, nil, fmt.Errorf("Interface name %s not found", intf.Name)
	}

	if expectInSb {
		if intf.Sandbox == "" {
			return ifFound, nil, fmt.Errorf("Interface %s is expected to be in a sandbox", intf.Name)
		}
	} else {
		if intf.Sandbox != "" {
			return ifFound, nil, fmt.Errorf("Interface %s should not be in sandbox", intf.Name)
		}
	}

	return ifFound, link, err
}

func validateCniBrInterface(intf current.Interface, n *commontype.NetConf) (cniBridgeIf, error) {

	brFound, link, err := validateInterface(intf, false)
	if err != nil {
		return brFound, err
	}

	_, isBridge := link.(*netlink.Bridge)
	if !isBridge {
		return brFound, fmt.Errorf("Interface %s does not have link type of bridge", intf.Name)
	}

	if intf.Mac != "" {
		if intf.Mac != link.Attrs().HardwareAddr.String() {
			return brFound, fmt.Errorf("Bridge interface %s Mac doesn't match: %s", intf.Name, intf.Mac)
		}
	}

	linkPromisc := link.Attrs().Promisc != 0
	if linkPromisc != n.PromiscMode {
		return brFound, fmt.Errorf("Bridge interface %s configured Promisc Mode %v doesn't match current state: %v ",
			intf.Name, n.PromiscMode, linkPromisc)
	}

	brFound.found = true
	brFound.Name = link.Attrs().Name
	brFound.ifIndex = link.Attrs().Index
	brFound.masterIndex = link.Attrs().MasterIndex

	return brFound, nil
}

func validateCniVethInterface(intf *current.Interface, brIf cniBridgeIf, contIf cniBridgeIf) (cniBridgeIf, error) {

	vethFound, link, err := validateInterface(*intf, false)
	if err != nil {
		return vethFound, err
	}

	_, isVeth := link.(*netlink.Veth)
	if !isVeth {
		// just skip it, it's not what CNI created
		return vethFound, nil
	}

	_, vethFound.peerIndex, err = ip.GetVethPeerIfindex(link.Attrs().Name)
	if err != nil {
		return vethFound, fmt.Errorf("Unable to obtain veth peer index for veth %s", link.Attrs().Name)
	}
	vethFound.ifIndex = link.Attrs().Index
	vethFound.masterIndex = link.Attrs().MasterIndex

	if vethFound.ifIndex != contIf.peerIndex {
		return vethFound, nil
	}

	if contIf.ifIndex != vethFound.peerIndex {
		return vethFound, nil
	}

	if vethFound.masterIndex != brIf.ifIndex {
		return vethFound, nil
	}

	if intf.Mac != "" {
		if intf.Mac != link.Attrs().HardwareAddr.String() {
			return vethFound, fmt.Errorf("Interface %s Mac doesn't match: %s not found", intf.Name, intf.Mac)
		}
	}

	vethFound.found = true
	vethFound.Name = link.Attrs().Name

	return vethFound, nil
}

func validateCniContainerInterface(intf current.Interface) (cniBridgeIf, error) {

	vethFound, link, err := validateInterface(intf, true)
	if err != nil {
		return vethFound, err
	}

	_, isVeth := link.(*netlink.Veth)
	if !isVeth {
		return vethFound, fmt.Errorf("Error: Container interface %s not of type veth", link.Attrs().Name)
	}
	_, vethFound.peerIndex, err = ip.GetVethPeerIfindex(link.Attrs().Name)
	if err != nil {
		return vethFound, fmt.Errorf("Unable to obtain veth peer index for veth %s", link.Attrs().Name)
	}
	vethFound.ifIndex = link.Attrs().Index

	if intf.Mac != "" {
		if intf.Mac != link.Attrs().HardwareAddr.String() {
			return vethFound, fmt.Errorf("Interface %s Mac %s doesn't match container Mac: %s", intf.Name, intf.Mac, link.Attrs().HardwareAddr)
		}
	}

	vethFound.found = true
	vethFound.Name = link.Attrs().Name

	return vethFound, nil
}
