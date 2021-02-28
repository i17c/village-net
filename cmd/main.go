package main

import (
	goflags "flag"
	"fmt"
	"github.com/containernetworking/plugins/cmd/apps"
	"k8s.io/klog"
	"os"
)

func init() {
	klog.InitFlags(nil)
	apps.RootCmd.Flags().AddGoFlagSet(goflags.CommandLine)
}

func main() {
	goflags.Parse()
	defer klog.Flush()
	if err := apps.RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
