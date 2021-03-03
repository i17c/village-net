package controller

import (
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
	"sync"
)

type IpPool struct {
	mux sync.Mutex
}

func newIpPool() *IpPool {
	return &IpPool{}
}

func (i *IpPool) Allocate(args *skel.CmdArgs, result *current.Result) error {
	// todo
	return nil
}

func (i *IpPool) Release(args *skel.CmdArgs, reply *struct{}) error {
	// todo
	return nil
}

func (i *IpPool) Check(args *skel.CmdArgs, reply *struct{}) error {
	// todo
	return nil
}
