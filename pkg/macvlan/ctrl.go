package macvlan

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
	"github.com/j-keck/arping"
	"github.com/vishvananda/netlink"
	"net"
)

type ctrl struct {
	cniVersion string
	args       *skel.CmdArgs
	conf       *commontype.NetConf
	result     *current.Result
}

func (c ctrl) Add() error {

	netns, err := ns.GetNS(c.args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	defer netns.Close()

	macvlanInterface, err := createMacvlan(c.conf.MacVlan, c.args.IfName, netns)
	if err != nil {
		return err
	}

	// Delete link if err to avoid link leak in this ns
	defer func() {
		if err != nil {
			netns.Do(func(_ ns.NetNS) error {
				return ip.DelLinkByName(c.args.IfName)
			})
		}
	}()

	// Assume L2 interface only
	result := &current.Result{CNIVersion: c.cniVersion, Interfaces: []*current.Interface{macvlanInterface}}

	if c.result != nil {
		if len(c.result.IPs) == 0 {
			return errors.New("IPAM plugin returned missing IP config")
		}

		result.IPs = c.result.IPs
		result.Routes = c.result.Routes

		for _, ipc := range result.IPs {
			// All addresses apply to the container macvlan interface
			ipc.Interface = current.Int(0)
		}

		err = netns.Do(func(_ ns.NetNS) error {
			if err := ipam.ConfigureIface(c.args.IfName, result); err != nil {
				return err
			}

			contVeth, err := net.InterfaceByName(c.args.IfName)
			if err != nil {
				return fmt.Errorf("failed to look up %q: %v", c.args.IfName, err)
			}

			for _, ipc := range result.IPs {
				if ipc.Version == "4" {
					_ = arping.GratuitousArpOverIface(ipc.Address.IP, *contVeth)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		// For L2 just change interface status to up
		err = netns.Do(func(_ ns.NetNS) error {
			macvlanInterfaceLink, err := netlink.LinkByName(c.args.IfName)
			if err != nil {
				return fmt.Errorf("failed to find interface name %q: %v", macvlanInterface.Name, err)
			}

			if err := netlink.LinkSetUp(macvlanInterfaceLink); err != nil {
				return fmt.Errorf("failed to set %q UP: %v", c.args.IfName, err)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	result.DNS = c.conf.DNS

	return types.PrintResult(result, c.cniVersion)
}

func (c ctrl) Del() error {
	var err error
	if c.args.Netns == "" {
		return nil
	}

	// There is a netns so try to clean up. Delete can be called multiple times
	// so don't return an error if the device is already removed.
	err = ns.WithNetNSPath(c.args.Netns, func(_ ns.NetNS) error {
		if err := ip.DelLinkByName(c.args.IfName); err != nil {
			if err != ip.ErrLinkNotFound {
				return err
			}
		}
		return nil
	})

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
		return fmt.Errorf("Required prevResult missing")
	}

	if err := version.ParsePrevResult(&c.conf.NetConf); err != nil {
		return err
	}

	result, err := current.NewResultFromResult(c.conf.PrevResult)
	if err != nil {
		return err
	}

	var contMap current.Interface
	// Find interfaces for names whe know, macvlan device name inside container
	for _, intf := range result.Interfaces {
		if c.args.IfName == intf.Name {
			if c.args.Netns == intf.Sandbox {
				contMap = *intf
				continue
			}
		}
	}

	// The namespace must be the same as what was configured
	if c.args.Netns != contMap.Sandbox {
		return fmt.Errorf("Sandbox in prevResult %s doesn't match configured netns: %s",
			contMap.Sandbox, c.args.Netns)
	}

	m, err := netlink.LinkByName(c.conf.MacVlan.Master)
	if err != nil {
		return fmt.Errorf("failed to lookup master %q: %v", c.conf.MacVlan.Master, err)
	}

	// Check prevResults for ips, routes and dns against values found in the container
	if err := netns.Do(func(_ ns.NetNS) error {

		// Check interface against values found in the container
		err := validateCniContainerInterface(contMap, m.Attrs().Index, c.conf.MacVlan.Mode)
		if err != nil {
			return err
		}

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
