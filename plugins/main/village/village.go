package main

import (
	"encoding/json"
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/bridge"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/macvlan"
	commontype "github.com/containernetworking/plugins/pkg/types"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"runtime"
	"strings"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

type Village struct {
	Bridge  IfCtrl
	MacVlan IfCtrl
}

func loadConf(data []byte) (*commontype.NetConf, string, error) {
	conf := &commontype.NetConf{}
	if err := json.Unmarshal(data, conf); err != nil {
		return nil, "", fmt.Errorf("failed to load netconf: %s", err.Error())
	}

	return conf, conf.CNIVersion, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	conf, cniVersion, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	var success = false
	var ctrl IfCtrl
	isLayer3 := conf.IPAM.Type != ""
	if !isLayer3 {
		ctrl = bridge.NewCtrl(args, conf, cniVersion, nil)
		return ctrl.Add()
	}

	result := &current.Result{}
	r, err := ipam.ExecAdd(conf.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}
	defer func() {
		if !success {
			_ = ipam.ExecDel(conf.IPAM.Type, args.StdinData)
		}
	}()

	result, err = current.NewResultFromResult(r)
	if err != nil {
		return err
	}

	brIf := &current.Result{
		CNIVersion: cniVersion,
		Routes:     result.Routes,
		DNS:        result.DNS,
	}
	mvIf := &current.Result{CNIVersion: cniVersion}
	for _, ipInfo := range result.IPs {
		ipBody := &current.IPConfig{
			Version: ipInfo.Version,
			Address: ipInfo.Address,
			Gateway: ipInfo.Gateway,
		}
		if ipInfo.Interface != nil {
			inf := result.Interfaces[*ipInfo.Interface]
			if strings.HasPrefix(inf.Name, "mv") {
				mvIf.Interfaces = append(mvIf.Interfaces, inf)
				idx := len(mvIf.Interfaces) - 1
				ipBody.Interface = &idx
				mvIf.IPs = append(mvIf.IPs, ipBody)
				continue
			}
			brIf.Interfaces = append(brIf.Interfaces, inf)
			idx := len(brIf.Interfaces) - 1
			ipBody.Interface = &idx
		}
		brIf.IPs = append(brIf.IPs, ipBody)
	}

	ctrl = bridge.NewCtrl(args, conf, cniVersion, brIf)
	if err := ctrl.Add(); err != nil {
		return err
	}

	defer func() {
		if !success {
			_ = ctrl.Del()
		}
	}()

	if len(mvIf.Interfaces) > 0 && conf.MacVlan != nil {
		args.IfName = "mv0"
		mvCtrl := macvlan.NewCtrl(args, conf, cniVersion, mvIf)
		if err := mvCtrl.Add(); err != nil {
			return err
		}
	}
	success = true
	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	conf, cniVersion, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	isLayer3 := conf.IPAM.Type != ""
	if isLayer3 {
		if err := ipam.ExecDel(conf.IPAM.Type, args.StdinData); err != nil {
			return err
		}
	}

	// FIXME
	func() {
		old := args.IfName
		args.IfName = "mv0"
		mvCtrl := macvlan.NewCtrl(args, conf, cniVersion, nil)
		_ = mvCtrl.Del()
		args.IfName = old
	}()

	ctrl := bridge.NewCtrl(args, conf, cniVersion, nil)
	if err := ctrl.Del(); err != nil {
		return err
	}

	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	conf, cniVersion, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	// run the IPAM plugin and get back the config to apply
	err = ipam.ExecCheck(conf.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	// FIXME
	func() {
		mvCtrl := macvlan.NewCtrl(args, conf, cniVersion, nil)
		_ = mvCtrl.Check()
	}()

	ctrl := bridge.NewCtrl(args, conf, cniVersion, nil)
	if err := ctrl.Check(); err != nil {
		return err
	}

	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("village"))
}
