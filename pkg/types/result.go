package types

import (
	"encoding/json"
	"fmt"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	types020 "github.com/containernetworking/cni/pkg/types/020"
	"github.com/containernetworking/cni/pkg/types/current"
	"io"
	"net"
	"os"
)

type Result struct {
	CNIVersion string            `json:"cniVersion,omitempty"`
	Interfaces []*Interface      `json:"interfaces,omitempty"`
	IPs        []*IPConfig       `json:"ips,omitempty"`
	Routes     []*cnitypes.Route `json:"routes,omitempty"`
	DNS        cnitypes.DNS      `json:"dns,omitempty"`
}

// Interface contains values about the created interfaces
type Interface struct {
	Name        string      `json:"name"`
	Mac         string      `json:"mac,omitempty"`
	Sandbox     string      `json:"sandbox,omitempty"`
	Type        string      `json:"type,omitempty"`
	IPs         []*IPConfig `json:"ips,omitempty"`
	IsDefaultGW bool        `json:"is_default_gw,omitempty"`
}

// IPConfig contains values necessary to configure an IP address on an interface
type IPConfig struct {
	// IP version, either "4" or "6"
	Version string
	Address net.IPNet
	Gateway net.IP
}

func (r Result) Version() string {
	return current.ImplementedSpecVersion
}

func (r *Result) convertTo020() (*types020.Result, error) {
	oldResult := &types020.Result{
		CNIVersion: types020.ImplementedSpecVersion,
		DNS:        r.DNS,
	}

	for _, ip := range r.IPs {
		// Only convert the first IP address of each version as 0.2.0
		// and earlier cannot handle multiple IP addresses
		if ip.Version == "4" && oldResult.IP4 == nil {
			oldResult.IP4 = &types020.IPConfig{
				IP:      ip.Address,
				Gateway: ip.Gateway,
			}
		} else if ip.Version == "6" && oldResult.IP6 == nil {
			oldResult.IP6 = &types020.IPConfig{
				IP:      ip.Address,
				Gateway: ip.Gateway,
			}
		}

		if oldResult.IP4 != nil && oldResult.IP6 != nil {
			break
		}
	}

	for _, route := range r.Routes {
		is4 := route.Dst.IP.To4() != nil
		if is4 && oldResult.IP4 != nil {
			oldResult.IP4.Routes = append(oldResult.IP4.Routes, cnitypes.Route{
				Dst: route.Dst,
				GW:  route.GW,
			})
		} else if !is4 && oldResult.IP6 != nil {
			oldResult.IP6.Routes = append(oldResult.IP6.Routes, cnitypes.Route{
				Dst: route.Dst,
				GW:  route.GW,
			})
		}
	}

	if oldResult.IP4 == nil && oldResult.IP6 == nil {
		return nil, fmt.Errorf("cannot convert: no valid IP addresses")
	}

	return oldResult, nil
}

func (r Result) GetAsVersion(version string) (cnitypes.Result, error) {
	switch version {
	case "0.3.0", "0.3.1", current.ImplementedSpecVersion:
		r.CNIVersion = version
		return r, nil
	case types020.SupportedVersions[0], types020.SupportedVersions[1], types020.SupportedVersions[2]:
		return r.convertTo020()
	}
	return nil, fmt.Errorf("error version %q", version)
}

func (r Result) Print() error {
	return r.PrintTo(os.Stdout)
}

func (r Result) PrintTo(writer io.Writer) error {
	data, err := json.MarshalIndent(r, "", "    ")
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}
