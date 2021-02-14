package main

import (
	"fmt"
	"github.com/containernetworking/plugins/pkg/types"

	"encoding/json"
	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
)

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("village-ipam"))
}

func cmdAdd(args *skel.CmdArgs) error {
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

	// 获取 ip 并返回结果
	r := &types.Result{}
	r, err = ipamClient.AssignIp(args.ContainerID)
	if err != nil {
		return err
	}

	// Print result to stdout, in the format defined by the requested cniVersion.
	return cnitypes.PrintResult(r, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf := types.NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}

	// 初始化 Client
	ipamClient, err := NewIPAMClient(conf)
	if err != nil {
		return err
	}

	if err := ipamClient.DeleteIp(args.ContainerID); err != nil {
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
