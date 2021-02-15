package bridge

import (
	"errors"
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	commontype "github.com/containernetworking/plugins/pkg/types"
	"github.com/containernetworking/plugins/pkg/utils"
	"github.com/j-keck/arping"
	"github.com/vishvananda/netlink"
	"net"
	"time"
)

type ctrl struct {
	cniVersion string
	args       *skel.CmdArgs
	conf       *commontype.NetConf
	result     *current.Result
}

func (c ctrl) Add() error {
	if c.conf.IsDefaultGW {
		c.conf.IsGW = true
	}

	if c.conf.HairpinMode && c.conf.PromiscMode {
		return fmt.Errorf("cannot set hairpin mode and promiscous mode at the same time. ")
	}

	br, brInterface, err := setupBridge(c.conf)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(c.args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", c.args.Netns, err)
	}
	defer netns.Close()

	hostInterface, containerInterface, err := setupVeth(netns, br, c.args.IfName, c.conf.MTU, c.conf.HairpinMode, c.conf.Vlan)
	if err != nil {
		return err
	}

	// Assume L2 interface only
	result := &current.Result{CNIVersion: c.cniVersion, Interfaces: []*current.Interface{brInterface, hostInterface, containerInterface}}

	if c.result != nil {
		// Convert whatever the IPAM result was into the current Result type
		result.IPs = c.result.IPs
		result.Routes = c.result.Routes

		if len(result.IPs) == 0 {
			return errors.New("IPAM plugin returned missing IP config")
		}

		// Gather gateway information for each IP family
		gwsV4, gwsV6, err := calcGateways(result, c.conf)
		if err != nil {
			return err
		}

		// Configure the container hardware address and IP address(es)
		if err := netns.Do(func(_ ns.NetNS) error {
			// Disable IPv6 DAD just in case hairpin mode is enabled on the
			// bridge. Hairpin mode causes echos of neighbor solicitation
			// packets, which causes DAD failures.
			for _, ipc := range result.IPs {
				if ipc.Version == "6" && (c.conf.HairpinMode || c.conf.PromiscMode) {
					if err := disableIPV6DAD(c.args.IfName); err != nil {
						return err
					}
					break
				}
			}

			// Add the IP to the interface
			if err := ipam.ConfigureIface(c.args.IfName, result); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}

		// check bridge port state
		retries := []int{0, 50, 500, 1000, 1000}
		for idx, sleep := range retries {
			time.Sleep(time.Duration(sleep) * time.Millisecond)

			hostVeth, err := netlink.LinkByName(hostInterface.Name)
			if err != nil {
				return err
			}
			if hostVeth.Attrs().OperState == netlink.OperUp {
				break
			}

			if idx == len(retries)-1 {
				return fmt.Errorf("bridge port in error state: %s", hostVeth.Attrs().OperState)
			}
		}

		// Send a gratuitous arp
		if err := netns.Do(func(_ ns.NetNS) error {
			contVeth, err := net.InterfaceByName(c.args.IfName)
			if err != nil {
				return err
			}

			for _, ipc := range result.IPs {
				if ipc.Version == "4" {
					_ = arping.GratuitousArpOverIface(ipc.Address.IP, *contVeth)
				}
			}
			return nil
		}); err != nil {
			return err
		}

		if c.conf.IsGW {
			var firstV4Addr net.IP
			var vlanInterface *current.Interface
			// Set the IP address(es) on the bridge and enable forwarding
			for _, gws := range []*gwInfo{gwsV4, gwsV6} {
				for _, gw := range gws.gws {
					if gw.IP.To4() != nil && firstV4Addr == nil {
						firstV4Addr = gw.IP
					}
					if c.conf.Vlan != 0 {
						vlanIface, err := ensureVlanInterface(br, c.conf.Vlan)
						if err != nil {
							return fmt.Errorf("failed to create vlan interface: %v", err)
						}

						if vlanInterface == nil {
							vlanInterface = &current.Interface{Name: vlanIface.Attrs().Name,
								Mac: vlanIface.Attrs().HardwareAddr.String()}
							result.Interfaces = append(result.Interfaces, vlanInterface)
						}

						err = ensureAddr(vlanIface, gws.family, &gw, c.conf.ForceAddress)
						if err != nil {
							return fmt.Errorf("failed to set vlan interface for bridge with addr: %v", err)
						}
					} else {
						err = ensureAddr(br, gws.family, &gw, c.conf.ForceAddress)
						if err != nil {
							return fmt.Errorf("failed to set bridge addr: %v", err)
						}
					}
				}

				if gws.gws != nil {
					if err = enableIPForward(gws.family); err != nil {
						return fmt.Errorf("failed to enable forwarding: %v", err)
					}
				}
			}
		}

		if c.conf.IPMasq {
			chain := utils.FormatChainName(c.conf.Name, c.args.ContainerID)
			comment := utils.FormatComment(c.conf.Name, c.args.ContainerID)
			for _, ipc := range result.IPs {
				if err = ip.SetupIPMasq(&ipc.Address, chain, comment); err != nil {
					return err
				}
			}
		}
	}

	// Refetch the bridge since its MAC address may change when the first
	// veth is added or after its IP address is set
	br, err = bridgeByName(c.conf.BrName)
	if err != nil {
		return err
	}
	brInterface.Mac = br.Attrs().HardwareAddr.String()

	result.DNS = c.conf.DNS

	// Return an error requested by testcases, if any
	if debugPostIPAMError != nil {
		return debugPostIPAMError
	}

	return types.PrintResult(result, c.cniVersion)
}

func (c ctrl) Del() error {
	if c.args.Netns == "" {
		return nil
	}

	// There is a netns so try to clean up. Delete can be called multiple times
	// so don't return an error if the device is already removed.
	// If the device isn't there then don't try to clean up IP masq either.
	var (
		ipnets []*net.IPNet
		err    error
	)
	err = ns.WithNetNSPath(c.args.Netns, func(_ ns.NetNS) error {
		var err error
		ipnets, err = ip.DelLinkByNameAddr(c.args.IfName)
		if err != nil && err == ip.ErrLinkNotFound {
			return nil
		}
		return err
	})

	if err != nil {
		return err
	}

	isLayer3 := c.conf.IPAM.Type != ""
	if isLayer3 && c.conf.IPMasq {
		chain := utils.FormatChainName(c.conf.Name, c.args.ContainerID)
		comment := utils.FormatComment(c.conf.Name, c.args.ContainerID)
		for _, ipn := range ipnets {
			if err := ip.TeardownIPMasq(ipn, chain, comment); err != nil {
				return err
			}
		}
	}

	return err
}

func (c ctrl) Check() error {
	netns, err := ns.GetNS(c.args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", c.args.Netns, err)
	}
	defer netns.Close()

	// Parse previous result.
	if c.conf.NetConf.RawPrevResult == nil {
		return fmt.Errorf("Required prevResult missing ")
	}

	if err := version.ParsePrevResult(&c.conf.NetConf); err != nil {
		return err
	}

	result, err := current.NewResultFromResult(c.conf.PrevResult)
	if err != nil {
		return err
	}

	var errLink error
	var contCNI, vethCNI cniBridgeIf
	var brMap, contMap current.Interface

	// Find interfaces for names whe know, CNI Bridge and container
	for _, intf := range result.Interfaces {
		if c.conf.BrName == intf.Name {
			brMap = *intf
			continue
		} else if c.args.IfName == intf.Name {
			if c.args.Netns == intf.Sandbox {
				contMap = *intf
				continue
			}
		}
	}

	brCNI, err := validateCniBrInterface(brMap, c.conf)
	if err != nil {
		return err
	}

	// The namespace must be the same as what was configured
	if c.args.Netns != contMap.Sandbox {
		return fmt.Errorf("Sandbox in prevResult %s doesn't match configured netns: %s",
			contMap.Sandbox, c.args.Netns)
	}

	// Check interface against values found in the container
	if err := netns.Do(func(_ ns.NetNS) error {
		contCNI, errLink = validateCniContainerInterface(contMap)
		if errLink != nil {
			return errLink
		}
		return nil
	}); err != nil {
		return err
	}

	// Now look for veth that is peer with container interface.
	// Anything else wasn't created by CNI, skip it
	for _, intf := range result.Interfaces {
		// Skip this result if name is the same as cni bridge
		// It's either the cni bridge we dealt with above, or something with the
		// same name in a different namespace.  We just skip since it's not ours
		if brMap.Name == intf.Name {
			continue
		}

		// same here for container name
		if contMap.Name == intf.Name {
			continue
		}

		vethCNI, errLink = validateCniVethInterface(intf, brCNI, contCNI)
		if errLink != nil {
			return errLink
		}

		if vethCNI.found {
			// veth with container interface as peer and bridge as master found
			break
		}
	}

	if !brCNI.found {
		return fmt.Errorf("CNI created bridge %s in host namespace was not found", c.conf.BrName)
	}
	if !contCNI.found {
		return fmt.Errorf("CNI created interface in container %s not found", c.args.IfName)
	}
	if !vethCNI.found {
		return fmt.Errorf("CNI veth created for bridge %s was not found", c.conf.BrName)
	}

	// Check prevResults for ips, routes and dns against values found in the container
	if err := netns.Do(func(_ ns.NetNS) error {
		err = ip.ValidateExpectedInterfaceIPs(c.args.IfName, result.IPs)
		if err != nil {
			return err
		}

		err = ip.ValidateExpectedRoute(result.Routes)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func NewCtrl(args *skel.CmdArgs, conf *commontype.NetConf, cniVersion string, result *current.Result) *ctrl {
	return &ctrl{
		cniVersion: cniVersion,
		args:       args,
		conf:       conf,
		result:     result,
	}
}
