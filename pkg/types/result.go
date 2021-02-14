package types

import (
	"encoding/json"
	"fmt"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"io"
	"net"
	"os"
)

const ImplementedSpecVersion string = "0.0.1"

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
	return ImplementedSpecVersion
}

func (r Result) GetAsVersion(version string) (cnitypes.Result, error) {
	switch version {
	case ImplementedSpecVersion:
		r.CNIVersion = version
		return r, nil
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
