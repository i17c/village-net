package main

import (
	"encoding/json"
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/types"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"net/rpc"
	"path/filepath"
)

const defaultSocketPath = "/run/cni/village.sock"

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("village-ipam"))
}

func cmdAdd(args *skel.CmdArgs) error {
	// 解析配置文件
	conf := types.NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	// 获取 ip 并返回结果
	r := &current.Result{}
	err := rpcCall("IpPool.Allocate", args, &r)
	if err != nil {
		return fmt.Errorf("failed to assignIp: %v", err)
	}

	// Print result to stdout, in the format defined by the requested cniVersion.
	return cnitypes.PrintResult(r, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf := types.NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	result := struct{}{}
	if err := rpcCall("IpPool.Release", args, &result); err != nil {
		return err
	}
	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	// 解析配置文件
	conf := types.NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	// 初始化 Client
	ipamClient, err := NewIPAMClient(conf)
	if err != nil {
		return err
	}

	// Look to see if there is at least one IP address allocated to the container
	containerIpFound, err := ipamClient.Check(args.ContainerID)
	if err != nil {
		return err
	}
	if containerIpFound == false {
		return fmt.Errorf("village-ipam: Failed to find address added by container %v", args.ContainerID)
	}

	return nil
}

func getSocketPath(stdinData []byte) (string, error) {
	conf := types.NetConf{}
	if err := json.Unmarshal(stdinData, &conf); err != nil {
		return "", fmt.Errorf("error parsing socket path conf: %v", err)
	}
	if conf.IPAM.SocketPath == "" {
		return defaultSocketPath, nil
	}
	return conf.IPAM.SocketPath, nil
}

func rpcCall(method string, args *skel.CmdArgs, result interface{}) error {
	socketPath, err := getSocketPath(args.StdinData)
	if err != nil {
		return fmt.Errorf("error obtaining socketPath: %v", err)
	}

	client, err := rpc.DialHTTP("unix", socketPath)
	if err != nil {
		return fmt.Errorf("error dialing village daemon: %v", err)
	}

	// The daemon may be running under a different working dir
	// so make sure the netns path is absolute.
	netns, err := filepath.Abs(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to make %q an absolute path: %v", args.Netns, err)
	}
	args.Netns = netns

	err = client.Call(method, args, result)
	if err != nil {
		return fmt.Errorf("error calling %v: %v", method, err)
	}

	return nil
}
